package core

import (
	"net/http"

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
// or clears the opt-out.
func (s *Handlers) handleSetRepeaterOrg(w http.ResponseWriter, r *http.Request) {
	rep, orgID, ok := s.repeaterOrgContext(w, r)
	if !ok {
		return
	}
	exclude := r.FormValue("action") == "exclude"
	if err := s.Store.SetRepeaterOrgExcluded(r.Context(), orgID, rep.ID, exclude); err != nil {
		s.ServerError(w, r, "could not update participation", err)
		return
	}
	http.Redirect(w, r, sharePath(rep.PublicID), http.StatusSeeOther)
}
