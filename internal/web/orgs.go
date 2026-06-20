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

// orgID resolves the {id} URL param (a slug) to the internal int64 primary key.
func (s *Server) orgID(r *http.Request) (int64, bool) {
	id, err := s.store.OrgIDBySlug(r.Context(), chi.URLParam(r, "id"))
	return id, err == nil
}

// orgParam returns the raw slug from the {id} URL param, for building redirects.
func orgParam(r *http.Request) string { return chi.URLParam(r, "id") }

func orgErr(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/orgs/"+orgParam(r)+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
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

// pageNewOrg renders the standalone "create an organization" form.
func (s *Server) pageNewOrg(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "new_org.html", map[string]any{
		"Error": r.URL.Query().Get("error"),
	})
}

// handleCreateOrg creates an org with the current user as its first admin.
func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 80 {
		http.Redirect(w, r, "/orgs/new?error="+url.QueryEscape("Enter an organization name."), http.StatusSeeOther)
		return
	}
	org, err := s.store.CreateOrg(r.Context(), name, uid)
	if err != nil {
		http.Redirect(w, r, "/orgs/new?error="+url.QueryEscape("Could not create organization."), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/orgs/"+org.Slug, http.StatusSeeOther)
}

// pageOrg shows an org's home. Members get the full management view; everyone
// else (anonymous or non-member) gets the public view. Members can preview the
// public view with ?view=public.
func (s *Server) pageOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
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
	mapped := 0
	for _, rp := range repeaters {
		if rp.HasLocation {
			mapped++
		}
	}
	adminCount := 0
	for _, m := range members {
		if m.Role == "admin" {
			adminCount++
		}
	}
	isAdmin := role == "admin"
	data := map[string]any{
		"Org":           org,
		"Role":          role,
		"IsAdmin":       isAdmin,
		"Members":       members,
		"Repeaters":     repeaters,
		"HasMap":        mapped > 0,
		"MemberCount":   len(members),
		"RepeaterCount": len(repeaters),
		"AdminCount":    adminCount,
		"Self":          uid,
		"Error":         r.URL.Query().Get("error"),
	}
	if isAdmin {
		domains, err := s.store.ListOrgDomains(r.Context(), id)
		if err != nil {
			http.Error(w, "could not load domains", http.StatusInternalServerError)
			return
		}
		data["Domains"] = domains
		data["TXTName"] = txtRecordPrefix // org appends its hostname for the full record name
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
	uid := s.auth.CurrentUserID(r.Context())
	s.render(w, r, "org_public.html", map[string]any{
		"Org":           org,
		"Admins":        admins,
		"MemberCount":   memberCount,
		"RepeaterCount": repeaterCount,
		"Repeaters":     pubReps,
		"HasMap":        len(pubReps) > 0,
		"IsMember":      isMember,
		"LoggedIn":      uid != 0,
		"CanJoin":       uid != 0 && !isMember,
	})
}

// handleEditOrg updates an org's slug, name, description, and region (admin only).
func (s *Server) handleEditOrg(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.ToLower(strings.TrimSpace(r.FormValue("slug")))
	desc := strings.TrimSpace(r.FormValue("description"))
	region := strings.TrimSpace(r.FormValue("region"))
	if name == "" || len(name) > 80 {
		orgErr(w, r, "Enter an organization name.")
		return
	}
	if !store.ValidOrgSlug(slug) {
		orgErr(w, r, "Slug must be 3–40 lowercase letters, numbers, and hyphens (and not reserved).")
		return
	}
	if len(desc) > 2000 {
		desc = desc[:2000]
	}
	if len(region) > 120 {
		region = region[:120]
	}
	if err := s.store.UpdateOrg(r.Context(), id, slug, name, desc, region); errors.Is(err, store.ErrDuplicate) {
		orgErr(w, r, "That URL slug is already taken.")
		return
	} else if err != nil {
		orgErr(w, r, "Could not save changes.")
		return
	}
	// The slug may have changed; redirect to the new canonical URL.
	http.Redirect(w, r, "/orgs/"+slug, http.StatusSeeOther)
}

// requireOrgAdmin resolves {id} and verifies the current user is an org admin.
func (s *Server) requireOrgAdmin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
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
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.store.RemoveOrgMember(r.Context(), id, uid)
	if errors.Is(err, store.ErrLastAdmin) {
		orgErr(w, r, "You're the last admin — promote someone else first.")
		return
	}
	if err != nil {
		orgErr(w, r, "Could not leave.")
		return
	}
	http.Redirect(w, r, "/orgs", http.StatusSeeOther)
}

// handleJoinOrg adds the current user to an org as a member. Orgs are publicly
// listed and anyone signed in may join directly from the org page (idempotent).
func (s *Server) handleJoinOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := s.store.AddOrgMember(r.Context(), id, uid, "member"); err != nil {
		orgErr(w, r, "Could not join.")
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r), http.StatusSeeOther)
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
		orgErr(w, r, "Unknown action.")
		return
	}
	if errors.Is(err, store.ErrLastAdmin) {
		orgErr(w, r, "That would leave the org with no admin.")
		return
	}
	if err != nil {
		orgErr(w, r, "Could not update member.")
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r), http.StatusSeeOther)
}
