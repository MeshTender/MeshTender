package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
)

func orgIDParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil
}

func orgErr(w http.ResponseWriter, r *http.Request, orgID int64, msg string) {
	http.Redirect(w, r, "/orgs/"+strconv.FormatInt(orgID, 10)+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
}

// pageOrgs is the public organization directory. Everyone sees the list; signed-
// in users also get the create form and a marker on orgs they belong to.
func (s *Server) pageOrgs(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	all, err := s.store.ListPublicOrgs(r.Context())
	if err != nil {
		http.Error(w, "could not load orgs", http.StatusInternalServerError)
		return
	}
	data := map[string]any{
		"All":      all,
		"LoggedIn": uid != 0,
		"Error":    r.URL.Query().Get("error"),
	}
	if uid != 0 {
		mine, err := s.store.ListOrgsForUser(r.Context(), uid)
		if err != nil {
			http.Error(w, "could not load orgs", http.StatusInternalServerError)
			return
		}
		memberOf := map[int64]string{}
		for _, m := range mine {
			memberOf[m.Org.ID] = m.Role
		}
		data["MemberOf"] = memberOf
	}
	s.render(w, r, "orgs.html", data)
}

// handleCreateOrg creates an org with the current user as its first admin.
func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 80 {
		http.Redirect(w, r, "/orgs?error="+url.QueryEscape("Enter an organization name."), http.StatusSeeOther)
		return
	}
	org, err := s.store.CreateOrg(r.Context(), name, uid)
	if err != nil {
		http.Redirect(w, r, "/orgs?error="+url.QueryEscape("Could not create organization."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/orgs/"+strconv.FormatInt(org.ID, 10), http.StatusSeeOther)
}

// pageOrg shows an org's home. Members get the full management view; everyone
// else (anonymous or non-member) gets the public view. Members can preview the
// public view with ?view=public.
func (s *Server) pageOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := orgIDParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	org, err := s.store.GetOrg(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	role, isMember, err := s.store.OrgRole(r.Context(), id, uid)
	if err != nil {
		http.Error(w, "could not load org", http.StatusInternalServerError)
		return
	}
	if !isMember || r.URL.Query().Get("view") == "public" {
		s.renderOrgPublic(w, r, org, isMember)
		return
	}
	members, err := s.store.ListOrgMembers(r.Context(), id)
	if err != nil {
		http.Error(w, "could not load members", http.StatusInternalServerError)
		return
	}
	repeaters, err := s.store.ListOrgRepeaters(r.Context(), id)
	if err != nil {
		http.Error(w, "could not load repeaters", http.StatusInternalServerError)
		return
	}
	hasMap := false
	for _, rp := range repeaters {
		if rp.HasLocation {
			hasMap = true
			break
		}
	}
	isAdmin := role == "admin"
	data := map[string]any{
		"Org":       org,
		"Role":      role,
		"IsAdmin":   isAdmin,
		"Members":   members,
		"Repeaters": repeaters,
		"HasMap":    hasMap,
		"Self":      uid,
		"Error":     r.URL.Query().Get("error"),
	}
	if isAdmin {
		invites, err := s.store.ListOrgInvites(r.Context(), id)
		if err != nil {
			http.Error(w, "could not load invites", http.StatusInternalServerError)
			return
		}
		// Build full URLs for display.
		type inviteView struct {
			ID          int64
			Description string
			Link        string
		}
		var views []inviteView
		for _, inv := range invites {
			views = append(views, inviteView{inv.ID, inv.Description, s.absoluteURL(r, "/org-invite/"+inv.Token)})
		}
		data["Invites"] = views
	}
	s.render(w, r, "org.html", data)
}

// renderOrgPublic renders the public-facing org page (name, description, admins,
// counts, and a map of repeaters opted into public display).
func (s *Server) renderOrgPublic(w http.ResponseWriter, r *http.Request, org *store.Org, isMember bool) {
	members, err := s.store.ListOrgMembers(r.Context(), org.ID)
	if err != nil {
		http.Error(w, "could not load org", http.StatusInternalServerError)
		return
	}
	var admins []string
	for _, m := range members {
		if m.Role == "admin" {
			admins = append(admins, m.Name())
		}
	}
	memberCount, repeaterCount, err := s.store.OrgCounts(r.Context(), org.ID)
	if err != nil {
		http.Error(w, "could not load org", http.StatusInternalServerError)
		return
	}
	pubReps, err := s.store.ListPublicMapRepeaters(r.Context(), org.ID)
	if err != nil {
		http.Error(w, "could not load org", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "org_public.html", map[string]any{
		"Org":           org,
		"Admins":        admins,
		"MemberCount":   memberCount,
		"RepeaterCount": repeaterCount,
		"Repeaters":     pubReps,
		"HasMap":        len(pubReps) > 0,
		"IsMember":      isMember,
	})
}

// handleEditOrg updates an org's name and description (admin only).
func (s *Server) handleEditOrg(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	desc := strings.TrimSpace(r.FormValue("description"))
	if name == "" || len(name) > 80 {
		orgErr(w, r, id, "Enter an organization name.")
		return
	}
	if len(desc) > 2000 {
		desc = desc[:2000]
	}
	if err := s.store.UpdateOrg(r.Context(), id, name, desc); err != nil {
		orgErr(w, r, id, "Could not save changes.")
		return
	}
	http.Redirect(w, r, "/orgs/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// requireOrgAdmin resolves {id} and verifies the current user is an org admin.
func (s *Server) requireOrgAdmin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := orgIDParam(r)
	if !ok {
		http.NotFound(w, r)
		return 0, false
	}
	admin, err := s.store.IsOrgAdmin(r.Context(), id, uid)
	if err != nil || !admin {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *Server) handleLeaveOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := orgIDParam(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.store.RemoveOrgMember(r.Context(), id, uid)
	if errors.Is(err, store.ErrLastAdmin) {
		orgErr(w, r, id, "You're the last admin — promote someone else first.")
		return
	}
	if err != nil {
		orgErr(w, r, id, "Could not leave.")
		return
	}
	http.Redirect(w, r, "/orgs", http.StatusSeeOther)
}

func (s *Server) handleCreateOrgInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	desc := strings.TrimSpace(r.FormValue("description"))
	if len(desc) > 100 {
		desc = desc[:100]
	}
	if _, err := s.store.CreateOrgInvite(r.Context(), id, desc); err != nil {
		orgErr(w, r, id, "Could not create link.")
		return
	}
	http.Redirect(w, r, "/orgs/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleDeleteOrgInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	inviteID, err := strconv.ParseInt(r.FormValue("invite_id"), 10, 64)
	if err != nil {
		orgErr(w, r, id, "Invalid link.")
		return
	}
	if err := s.store.DeleteOrgInvite(r.Context(), id, inviteID); err != nil {
		orgErr(w, r, id, "Could not revoke link.")
		return
	}
	http.Redirect(w, r, "/orgs/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// handleSetOrgMember promotes/demotes/removes a member (admin only).
func (s *Server) handleSetOrgMember(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	targetID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch r.FormValue("action") {
	case "promote":
		err = s.store.SetOrgMemberRole(r.Context(), id, targetID, "admin")
	case "demote":
		err = s.store.SetOrgMemberRole(r.Context(), id, targetID, "member")
	case "remove":
		err = s.store.RemoveOrgMember(r.Context(), id, targetID)
	default:
		orgErr(w, r, id, "Unknown action.")
		return
	}
	if errors.Is(err, store.ErrLastAdmin) {
		orgErr(w, r, id, "That would leave the org with no admin.")
		return
	}
	if err != nil {
		orgErr(w, r, id, "Could not update member.")
		return
	}
	http.Redirect(w, r, "/orgs/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// --- join via invite ---

func (s *Server) pageOrgInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	uid := s.auth.CurrentUserID(r.Context())

	org, err := s.store.OrgByInviteToken(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, "org_invite.html", map[string]any{"State": "invalid"})
		return
	}
	if err != nil {
		http.Error(w, "could not load invite", http.StatusInternalServerError)
		return
	}
	data := map[string]any{"Org": org, "Token": token}
	switch {
	case uid == 0:
		data["State"] = "auth_required"
		data["Next"] = "/org-invite/" + token
	default:
		_, isMember, err := s.store.OrgRole(r.Context(), org.ID, uid)
		if err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		if isMember {
			data["State"] = "already"
		} else {
			data["State"] = "confirm"
		}
	}
	s.render(w, r, "org_invite.html", data)
}

func (s *Server) handleAcceptOrgInvite(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	uid := s.auth.CurrentUserID(r.Context())
	org, err := s.store.OrgByInviteToken(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, "org_invite.html", map[string]any{"State": "invalid"})
		return
	}
	if err != nil {
		http.Error(w, "could not load invite", http.StatusInternalServerError)
		return
	}
	if err := s.store.AddOrgMember(r.Context(), org.ID, uid, "member"); err != nil {
		http.Error(w, "could not join", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/orgs/"+strconv.FormatInt(org.ID, 10), http.StatusSeeOther)
}
