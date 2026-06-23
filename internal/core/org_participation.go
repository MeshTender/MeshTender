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
		http.NotFound(w, r)
		return nil, 0, false
	}
	rep, err := s.Store.GetRepeaterOwned(r.Context(), owner, id)
	if err != nil {
		http.NotFound(w, r)
		return nil, 0, false
	}
	if _, isMember, err := s.Store.OrgRole(r.Context(), orgID, owner); err != nil || !isMember {
		http.NotFound(w, r) // can only manage participation in orgs you belong to
		return nil, 0, false
	}
	return rep, orgID, true
}

// handleSetRepeaterOrg opts a repeater into or out of an org (owner only). A
// repeater participates in every org its owner belongs to by default; this writes
// or clears the opt-out.
func (s *Handlers) handleSetRepeaterOrg(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.repeaterOrgContext(w, r)
	if !ok {
		return
	}
	exclude := r.FormValue("action") == "exclude"
	if err := s.Store.SetRepeaterOrgExcluded(r.Context(), orgID, rep.ID, exclude); err != nil {
		http.Error(w, "could not update participation", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, sharePath(rep.PublicID), http.StatusSeeOther)
}

// pageOrgCommands lets a member restrict, for one org, which of the commands that
// org is permitted to run actually run on the member's repeaters. No restriction
// (the default) means every command in the org's ceiling can run.
func (s *Handlers) pageOrgCommands(w http.ResponseWriter, r *http.Request) {
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
	if _, isMember, err := s.Store.OrgRole(r.Context(), id, uid); err != nil || !isMember {
		http.NotFound(w, r)
		return
	}
	ceiling, err := s.orgCeilingCommands(r)
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}
	optIn, _ := s.Store.OrgOptInCommandIDs(r.Context(), id, uid)
	restricted := len(optIn) > 0
	// Permissive (no list) shows everything checked: all ceiling commands may run.
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
	s.Render(w, r, "org_commands.html", map[string]any{
		"Org":        org,
		"Groups":     groupCommands(ceiling, checked),
		"Restricted": restricted,
	})
}

// handleSaveOrgCommands saves the member's per-org opt-in list. If every ceiling
// command is selected (or none of the modes restrict), the list is cleared back to
// permissive so we don't persist a redundant full allowlist.
func (s *Handlers) handleSaveOrgCommands(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.orgID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, isMember, err := s.Store.OrgRole(r.Context(), id, uid); err != nil || !isMember {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// "Remove restriction" clears the list regardless of checkboxes.
	if r.FormValue("clear") != "" {
		if err := s.Store.SetOrgOptIn(r.Context(), id, uid, nil); err != nil {
			http.Error(w, "could not save", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/orgs/"+orgParam(r)+"/my-commands", http.StatusSeeOther)
		return
	}
	ceiling, err := s.orgCeilingCommands(r)
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}
	chosen := parseCommandIDs(r.Form["cmd"])
	// Selecting the full ceiling is equivalent to permissive — store nothing.
	if len(chosen) >= len(ceiling) {
		chosen = nil
	}
	if err := s.Store.SetOrgOptIn(r.Context(), id, uid, chosen); err != nil {
		http.Error(w, "could not save", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/orgs/"+orgParam(r)+"/my-commands", http.StatusSeeOther)
}

// orgCeilingCommands returns the catalog commands an org is ever permitted to run
// (member or admin tier) — the universe the opt-in editor restricts within.
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

// parseCommandIDs parses a slice of form values into catalog command ids, skipping
// any that aren't valid integers.
func parseCommandIDs(values []string) []int64 {
	var ids []int64
	for _, v := range values {
		if cid, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, cid)
		}
	}
	return ids
}
