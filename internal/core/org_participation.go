package core

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/MeshTender/MeshTender/internal/store"
)

// repeaterOrgContext resolves the {id} repeater (must be owned by the caller) and
// the {orgID} org slug (the caller must belong to it). It's the gate for the
// per-org participation toggle a repeater owner controls.
func (s *Handlers) repeaterOrgContext(w http.ResponseWriter, r *http.Request) (*store.Repeater, int64, bool) {
	owner := s.Auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	orgID, oerr := s.Store.OrgIDBySlug(r.Context(), chi.URLParam(r, "orgID"))
	if !ok || oerr != nil {
		s.NotFound(w, r)
		return nil, 0, false
	}
	rep, err := s.Store.GetRepeaterOwned(r.Context(), owner, id)
	if err != nil {
		s.NotFound(w, r)
		return nil, 0, false
	}
	if _, isMember, err := s.Store.OrgRole(r.Context(), orgID, owner); err != nil || !isMember {
		s.NotFound(w, r) // can only manage participation in orgs you belong to
		return nil, 0, false
	}
	return rep, orgID, true
}

// pageRepeaterOrgLimits is Settings › Organizations › one organization's access
// for this repeater: the Shared (participation) switch plus which of the commands the org
// may run are allowed to run on this box. No opt-in rows = permissive (every
// ceiling command checked). Editable regardless of participation, so an owner can
// pre-set limits before opting an org in.
func (s *Handlers) pageRepeaterOrgLimits(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.repeaterOrgContext(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	ceiling, err := s.orgCeilingCommands(r)
	if err != nil {
		s.ServerError(w, r, "could not load commands", err)
		return
	}
	// Must not swallow this error: an empty list reads as "permissive" (everything
	// checked), and saving that would clear a real restriction.
	optIn, err := s.Store.RepeaterOrgOptInCommandIDs(r.Context(), orgID, rep.ID)
	if err != nil {
		s.ServerError(w, r, "could not load commands", err)
		return
	}
	restricted := len(optIn) > 0
	checked := make(map[int64]bool, len(ceiling))
	if restricted {
		for _, cid := range optIn {
			checked[cid] = true
		}
	} else {
		for _, c := range ceiling {
			checked[c.ID] = true
		}
	}
	// Whether the repeater is currently opted out of this org: the limits still
	// save, but no one in the org can run anything until it's opted back in — the
	// modal says so.
	excluded, err := s.Store.IsRepeaterOrgExcluded(r.Context(), orgID, rep.ID)
	if err != nil {
		s.ServerError(w, r, "could not load participation", err)
		return
	}
	s.renderSettingsPage(w, r, rep, settingsOrgs, "org", map[string]any{
		"Org":        org,
		"Groups":     groupCommands(ceiling, checked),
		"Restricted": restricted,
		"Excluded":   excluded,
		"ShowAccess": true,
		"Next":       orgAccessNext(r),
	})
}

// handleSaveRepeaterOrgLimits saves an organization's access page: it sets the org's
// participation (the Shared switch) and the per-(repeater, org) command opt-in list
// together. A full selection (or the "Remove restriction" button) clears the list
// back to permissive so we don't persist a redundant full allowlist.
func (s *Handlers) handleSaveRepeaterOrgLimits(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.repeaterOrgContext(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// Participation: the Shared switch submits include=1 when on, nothing when off.
	exclude := r.FormValue("include") != "1"
	if err := s.Store.SetRepeaterOrgExcluded(r.Context(), orgID, rep.ID, exclude); err != nil {
		s.ServerError(w, r, "could not update participation", err)
		return
	}
	var chosen []int64
	// "Remove restriction" clears the list regardless of checkboxes.
	if r.FormValue("clear") == "" {
		ceiling, err := s.orgCeilingCommands(r)
		if err != nil {
			s.ServerError(w, r, "could not load commands", err)
			return
		}
		chosen = parseCommandIDs(r.Form["cmd"])
		// Selecting the full ceiling is equivalent to permissive — store nothing.
		if len(chosen) >= len(ceiling) {
			chosen = nil
		}
	}
	if err := s.Store.SetRepeaterOrgOptIn(r.Context(), orgID, rep.ID, chosen); err != nil {
		s.ServerError(w, r, "could not save limits", err)
		return
	}
	http.Redirect(w, r, orgAccessReturn(rep.PublicID, orgAccessNext(r)), http.StatusSeeOther) //nolint:gosec // G710: local path from a fixed set
}

// orgAccessNext is where an organization's access page returns to: "added" for
// the add-repeater wizard, which links here, else Settings › Organizations. A
// fixed set, never a URL from the request, so it can't redirect off-site.
func orgAccessNext(r *http.Request) string {
	if r.FormValue("next") == "added" {
		return "added"
	}
	return ""
}

// orgAccessReturn is the page orgAccessNext names.
func orgAccessReturn(publicID, next string) string {
	if next == "added" {
		return "/repeaters/" + publicID + "/added"
	}
	return settingsPath(publicID, settingsOrgs)
}

// orgCeilingCommands returns the catalog commands an org is ever permitted to run
// (member or admin tier) — the universe the per-repeater opt-in editor restricts
// within.
func (s *Handlers) orgCeilingCommands(r *http.Request) ([]*store.Command, error) {
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		return nil, err
	}
	var out []*store.Command
	for _, c := range catalog {
		if c.OrgMemberAllowed || c.OrgAdminAllowed {
			out = append(out, c)
		}
	}
	return out, nil
}

// parseCommandIDs parses form values into catalog command ids, skipping any that
// aren't valid integers.
func parseCommandIDs(values []string) []int64 {
	var ids []int64
	for _, v := range values {
		if cid, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, cid)
		}
	}
	return ids
}
