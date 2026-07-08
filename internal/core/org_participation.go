package core

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
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

// handleSetRepeaterOrg opts a repeater into or out of an org (owner only). A
// repeater participates in every org its owner belongs to by default; this writes
// or clears the opt-out. Driven by the share page's per-org "Shared" switch: a
// checked switch submits include=1 (participate), unchecked submits nothing (opt
// out).
func (s *Handlers) handleSetRepeaterOrg(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.repeaterOrgContext(w, r)
	if !ok {
		return
	}
	exclude := r.FormValue("include") != "1"
	if err := s.Store.SetRepeaterOrgExcluded(r.Context(), orgID, rep.ID, exclude); err != nil {
		s.ServerError(w, r, "could not update participation", err)
		return
	}
	http.Redirect(w, r, sharePath(rep.PublicID), http.StatusSeeOther)
}

// pageRepeaterOrgLimits renders the per-org command-limits modal fragment for one
// repeater: which of the commands the org may run are allowed to run on this box.
// No opt-in rows = permissive (every ceiling command checked). Editable regardless
// of participation, so an owner can pre-set limits before opting an org back in.
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
	s.Render(w, r, "share_org_limits.html", map[string]any{
		"Repeater":   rep,
		"Org":        org,
		"Groups":     groupCommands(ceiling, checked),
		"Restricted": restricted,
		"ShowAccess": true,
		"Layout":     "org-limits-modal",
	})
}

// handleSaveRepeaterOrgLimits saves the per-(repeater, org) command opt-in list. A
// full selection (or the "Remove restriction" button) clears it back to permissive
// so we don't persist a redundant full allowlist.
func (s *Handlers) handleSaveRepeaterOrgLimits(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.repeaterOrgContext(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
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
	http.Redirect(w, r, sharePath(rep.PublicID), http.StatusSeeOther)
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
