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
		s.ServerError(w, r, "could not load organizations", err)
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
	http.Redirect(w, r, "/orgs/"+org.Slug, http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// pageOrg shows an org's home. Members get the full management view; everyone
// else (anonymous or non-member) gets the public view. Members can preview the
// public view with ?view=public.
func (s *Handlers) pageOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	role, isMember, err := s.Store.OrgRole(r.Context(), id, uid)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	if !isMember || r.URL.Query().Get("view") == "public" {
		// Render the public org view here on the app host (not the root host),
		// where the user's session lives: a non-member gets a working "Join"
		// button (its POST hits the app host), and a member previewing with
		// ?view=public gets the "Back to member view" link. The root host serves
		// the same page to anonymous/external visitors.
		s.renderOrgPublic(w, r, org, isMember, role == "admin")
		return
	}
	members, err := s.Store.ListOrgMembers(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load members", err)
		return
	}
	repeaters, err := s.Store.ListOrgRepeaters(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load repeaters", err)
		return
	}
	links, err := s.Store.ListOrgLinks(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load links", err)
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
		"Nav":           s.OrgNavFor(r.Context(), web.OrgNavArgs{OrgID: org.ID, Name: org.Name, Slug: org.Slug, Active: "home", IsMember: true, IsAdmin: isAdmin, Manage: true}),
		"Role":          role,
		"IsAdmin":       isAdmin,
		"Members":       members,
		"Repeaters":     repeaters,
		"Links":         links,
		"Platforms":     store.LinkPlatforms(),
		"PlatformsJS":   web.LinkPlatformsJS(store.LinkPlatforms()),
		"HasMap":        mapped > 0,
		"MemberCount":   len(members),
		"RepeaterCount": len(repeaters),
		"AdminCount":    adminCount,
		"Self":          uid,
		"Error":         r.URL.Query().Get("error"),
	}
	// Custom domains are hidden for now (infrastructure not in place); the
	// org.html card and the /domains routes are disabled, so don't load them.
	s.Render(w, r, "org.html", data)
}

// renderOrgPublic renders the shared public org page on the app host for a
// signed-in user: a non-member sees a Join button, and a member (previewing via
// ?view=public) sees a "Back to member view" link. The data shape matches the
// marketing surface's anonymous rendering of the same template.
func (s *Handlers) renderOrgPublic(w http.ResponseWriter, r *http.Request, org *store.Org, isMember, isAdmin bool) {
	admins, err := s.Store.ListOrgAdmins(r.Context(), org.ID)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	memberCount, repeaterCount, err := s.Store.OrgCounts(r.Context(), org.ID)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	pubReps, err := s.Store.ListPublicRepeaters(r.Context(), org.ID)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	links, err := s.Store.ListOrgLinks(r.Context(), org.ID)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	uid := s.Auth.CurrentUserID(r.Context())
	s.Render(w, r, "org_public.html", map[string]any{
		"Org": org,
		// The public view never exposes the Members tab — membership isn't public,
		// only the admin list is — so build the nav as a non-member regardless of who
		// is viewing (a member previews the public page via ?view=public).
		"Nav": s.OrgNavFor(r.Context(), web.OrgNavArgs{
			OrgID: org.ID, Name: org.Name, Slug: org.Slug, Active: "home",
			IsMember: false, IsAdmin: isAdmin, Manage: false,
			CanGoToOrg: isMember, CanJoin: uid != 0 && !isMember,
		}),
		"Admins":        admins,
		"MemberCount":   memberCount,
		"RepeaterCount": repeaterCount,
		"Repeaters":     pubReps,
		"Links":         links,
		"HasMap":        len(pubReps) > 0,
		"IsMember":      isMember,
		"LoggedIn":      uid != 0,
		"CanJoin":       uid != 0 && !isMember,
	})
}

// pageOrgMembers lists an org's members (with role management for admins). It's
// members-only: the list carries personal info (names/usernames), so non-members
// get a 404 — there is no public version of this page.
func (s *Handlers) pageOrgMembers(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	role, isMember, err := s.Store.OrgRole(r.Context(), id, uid)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	if !isMember {
		s.NotFound(w, r)
		return
	}
	members, err := s.Store.ListOrgMembers(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load members", err)
		return
	}
	s.Render(w, r, "org_members.html", map[string]any{
		"Org":     org,
		"Nav":     s.OrgNavFor(r.Context(), web.OrgNavArgs{OrgID: org.ID, Name: org.Name, Slug: org.Slug, Active: "members", IsMember: true, IsAdmin: role == "admin", Manage: true}),
		"IsAdmin": role == "admin",
		"Members": members,
		"Self":    uid,
		"Error":   r.URL.Query().Get("error"),
	})
}

// pageOrgRepeaters lists an org's repeaters with a map. Members see every
// contributed repeater (with links); any other viewer sees only those opted into
// the public map. The root host serves the same page anonymously.
func (s *Handlers) pageOrgRepeaters(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	role, isMember, err := s.Store.OrgRole(r.Context(), id, uid)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	rv, err := web.BuildRepeatersView(r.Context(), s.Store, id, isMember)
	if err != nil {
		s.ServerError(w, r, "could not load repeaters", err)
		return
	}
	s.Render(w, r, "org_repeaters.html", map[string]any{
		"Org": org,
		"Nav": s.OrgNavFor(r.Context(), web.OrgNavArgs{
			OrgID: org.ID, Name: org.Name, Slug: org.Slug, Active: "repeaters",
			IsMember: isMember, IsAdmin: role == "admin", Manage: isMember,
			CanGoToOrg: isMember, CanJoin: uid != 0 && !isMember,
		}),
		"Reps": rv,
	})
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
	desc = web.Clip(desc, 2000)
	region = web.Clip(region, 120)
	if err := s.Store.UpdateOrg(r.Context(), id, slug, name, desc, region); errors.Is(err, store.ErrDuplicate) {
		orgErr(w, r, "That URL slug is already taken.")
		return
	} else if err != nil {
		orgErr(w, r, "Could not save changes.")
		return
	}
	// The slug may have changed; redirect to the new canonical URL.
	http.Redirect(w, r, "/orgs/"+slug, http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// handleSetOrgLinks replaces an org's whole set of social/site links from the
// repeatable rows posted by the profile editor (admin only). Rows with a blank
// URL are dropped, so removing a link is just clearing its row and saving.
func (s *Handlers) handleSetOrgLinks(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		orgErr(w, r, "Could not save links.")
		return
	}
	// The three fields are submitted as index-aligned parallel arrays: one entry
	// each per row, in row order.
	platforms := r.Form["link_platform"]
	labels := r.Form["link_label"]
	urls := r.Form["link_url"]
	var links []store.OrgLink
	for i, raw := range urls {
		u := strings.TrimSpace(raw)
		if u == "" {
			continue // empty row — skip it
		}
		platform := ""
		if i < len(platforms) {
			platform = platforms[i]
		}
		p, ok := store.OrgLinkPlatform(platform)
		if !ok {
			orgErr(w, r, "Choose a type for each link.")
			return
		}
		// Validate/canonicalise per kind, leaving `u` as the value to persist.
		switch p.Kind {
		case store.KindText: // Discord — a username shown as text, or an invite link.
			if store.LooksLikeURL(u) {
				u = store.NormalizeLinkURL(u)
				if !store.ValidLinkURL(u) {
					orgErr(w, r, "Enter a valid username or invite link.")
					return
				}
			} else {
				v := strings.TrimPrefix(u, "@")
				if v == "" || len(v) > 64 || strings.ContainsAny(v, " \t\n\r") {
					orgErr(w, r, "Enter a valid username (no spaces) or an invite link.")
					return
				}
				u = v
			}
		case store.KindHandle:
			canon, ok := p.CanonicalHandleURL(u)
			if !ok {
				orgErr(w, r, "Enter a valid "+p.Name+" username or profile URL.")
				return
			}
			u = canon
		default: // KindURL — accept a bare domain by assuming https:// before validating.
			u = store.NormalizeLinkURL(u)
			if !store.ValidLinkURL(u) {
				orgErr(w, r, "Each link must be a valid http:// or https:// URL.")
				return
			}
		}
		label := ""
		if i < len(labels) {
			label = strings.TrimSpace(labels[i])
		}
		u = web.Clip(u, 300)
		label = web.Clip(label, 60)
		links = append(links, store.OrgLink{Platform: platform, Label: label, URL: u})
		if len(links) >= store.MaxOrgLinks {
			break
		}
	}
	if err := s.Store.ReplaceOrgLinks(r.Context(), id, links); err != nil {
		orgErr(w, r, "Could not save links.")
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// requireOrgAdmin resolves {id} and verifies the current user is an org admin.
func (s *Handlers) requireOrgAdmin(w http.ResponseWriter, r *http.Request) (int64, bool) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		s.NotFound(w, r)
		return 0, false
	}
	admin, err := s.Store.IsOrgAdmin(r.Context(), id, uid)
	if err != nil || !admin {
		s.NotFound(w, r)
		return 0, false
	}
	return id, true
}

func (s *Handlers) handleLeaveOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		s.NotFound(w, r)
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

// pageJoinOrg shows the join confirmation. Because repeaters are shared with an
// org by default (opt-out), joining is an explicit choice: share all your current
// repeaters, or none of them.
func (s *Handlers) pageJoinOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), id)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	_, isMember, err := s.Store.OrgRole(r.Context(), id, uid)
	if err != nil {
		s.ServerError(w, r, "could not load organization", err)
		return
	}
	if isMember {
		http.Redirect(w, r, "/orgs/"+org.Slug, http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
		return
	}
	hasRepeaters, err := s.Store.OwnsAnyRepeater(r.Context(), uid)
	if err != nil {
		s.ServerError(w, r, "could not load repeaters", err)
		return
	}
	s.Render(w, r, "join_org.html", map[string]any{"Org": org, "HasRepeaters": hasRepeaters})
}

// handleJoinOrg adds the current user to an org as a member. mode "none" opts all
// of the user's current repeaters out of the org; otherwise they're shared (the
// default). Idempotent on membership.
func (s *Handlers) handleJoinOrg(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	if err := s.Store.AddOrgMember(r.Context(), id, uid, "member"); err != nil {
		orgErr(w, r, "Could not join.")
		return
	}
	if r.FormValue("mode") == "none" {
		if err := s.Store.ExcludeOwnerRepeatersFromOrg(r.Context(), id, uid); err != nil {
			orgErr(w, r, "Joined, but could not opt your repeaters out — adjust them on the sharing page.")
			return
		}
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r), http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}

// handleSetOrgMember promotes/demotes/removes a member (admin only).
func (s *Handlers) handleSetOrgMember(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	membersURL := "/orgs/" + orgParam(r) + "/members"
	memberErr := func(msg string) { web.RedirectErr(w, r, membersURL, msg) }
	targetID, err := strconv.ParseInt(chi.URLParam(r, "userID"), 10, 64)
	if err != nil {
		s.NotFound(w, r)
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
		memberErr("Unknown action.")
		return
	}
	if errors.Is(err, store.ErrLastAdmin) {
		memberErr("That would leave the organization with no admin.")
		return
	}
	if err != nil {
		memberErr("Could not update member.")
		return
	}
	http.Redirect(w, r, membersURL, http.StatusSeeOther) //nolint:gosec // G710: local path or config-pinned origin
}
