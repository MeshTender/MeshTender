package core

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// orgID resolves the {id} URL param (a slug) to the internal int64 primary key.
func (s *Handlers) orgID(r *http.Request) (int64, bool) {
	id, err := s.Store.OrgIDBySlug(r.Context(), chi.URLParam(r, "id"))
	return id, err == nil
}

// orgParam returns the raw slug from the {id} URL param, for building redirects.
func orgParam(r *http.Request) string { return chi.URLParam(r, "id") }

func orgErr(w http.ResponseWriter, r *http.Request, msg string) {
	web.RedirectErr(w, r, "/orgs/"+orgParam(r), msg)
}

// pageMyOrgs lists the organizations the signed-in user belongs to (the app
// host's /orgs). Public discovery lives on the root host; a "Discover" button
// links there.
func (s *Handlers) pageMyOrgs(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	mine, err := s.Store.ListOrgsForUser(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load orgs", http.StatusInternalServerError)
		return
	}
	s.Render(w, r, "my_orgs.html", map[string]any{
		"Orgs":  mine,
		"Error": r.URL.Query().Get("error"),
	})
}

// pageNewOrg renders the standalone "create an organization" form.
func (s *Handlers) pageNewOrg(w http.ResponseWriter, r *http.Request) {
	s.Render(w, r, "new_org.html", map[string]any{
		"Error": r.URL.Query().Get("error"),
	})
}

// handleCreateOrg creates an org with the current user as its first admin.
func (s *Handlers) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 80 {
		web.RedirectErr(w, r, "/orgs/new", "Enter an organization name.")
		return
	}
	org, err := s.Store.CreateOrg(r.Context(), name, uid)
	if err != nil {
		web.RedirectErr(w, r, "/orgs/new", "Could not create organization.")
		return
	}
	http.Redirect(w, r, "/orgs/"+org.Slug, http.StatusSeeOther)
}

// pageOrg shows an org's home. Members get the full management view; everyone
// else (anonymous or non-member) gets the public view. Members can preview the
// public view with ?view=public.
func (s *Handlers) pageOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	role, isMember, err := s.Store.OrgRole(r.Context(), id, uid)
	if err != nil {
		http.Error(w, "could not load org", http.StatusInternalServerError)
		return
	}
	if !isMember || r.URL.Query().Get("view") == "public" {
		// Public org content lives on the root host (marketing surface).
		http.Redirect(w, r, s.Origin(r, s.Cfg.RootHost)+"/orgs/"+org.Slug, http.StatusSeeOther)
		return
	}
	members, err := s.Store.ListOrgMembers(r.Context(), id)
	if err != nil {
		http.Error(w, "could not load members", http.StatusInternalServerError)
		return
	}
	repeaters, err := s.Store.ListOrgRepeaters(r.Context(), id)
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
		domains, err := s.Store.ListOrgDomains(r.Context(), id)
		if err != nil {
			http.Error(w, "could not load domains", http.StatusInternalServerError)
			return
		}
		data["Domains"] = domains
		data["TXTName"] = txtRecordPrefix // org appends its hostname for the full record name
	}
	s.Render(w, r, "org.html", data)
}

// handleEditOrg updates an org's slug, name, description, and region (admin only).
func (s *Handlers) handleEditOrg(w http.ResponseWriter, r *http.Request) {
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
	if err := s.Store.UpdateOrg(r.Context(), id, slug, name, desc, region); errors.Is(err, store.ErrDuplicate) {
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
func (s *Handlers) requireOrgAdmin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return 0, false
	}
	admin, err := s.Store.IsOrgAdmin(r.Context(), id, uid)
	if err != nil || !admin {
		http.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *Handlers) handleLeaveOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.Store.RemoveOrgMember(r.Context(), id, uid)
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
func (s *Handlers) handleJoinOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := s.Store.AddOrgMember(r.Context(), id, uid, "member"); err != nil {
		orgErr(w, r, "Could not join.")
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r), http.StatusSeeOther)
}

// handleSetOrgMember promotes/demotes/removes a member (admin only).
func (s *Handlers) handleSetOrgMember(w http.ResponseWriter, r *http.Request) {
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
		err = s.Store.SetOrgMemberRole(r.Context(), id, targetID, "admin")
	case "demote":
		err = s.Store.SetOrgMemberRole(r.Context(), id, targetID, "member")
	case "remove":
		err = s.Store.RemoveOrgMember(r.Context(), id, targetID)
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
