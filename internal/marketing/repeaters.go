package marketing

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// pageRepeaterPublic renders a repeater's public page —
// for anonymous visitors on the root host. It exists only when the owner opted
// in (expose_public_page); otherwise it 404s like any unknown resource. It shows
// only public-safe information: what the node is, where it is, the public
// documentation, and who stewards it. Internal notes never reach this surface.
func (s *Handlers) pageRepeaterPublic(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Store.GetRepeaterPublic(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) {
		s.NotFound(w, r)
		return
	}
	if err != nil {
		s.ServerError(w, r, "could not load repeater", err)
		return
	}
	stewards, err := s.Store.ListStewards(r.Context(), rep.ID)
	if err != nil {
		s.ServerError(w, r, "could not load repeater", err)
		return
	}
	radio := fmt.Sprintf("%g MHz / %g kHz / SF%d / CR%d",
		float64(rep.RadioFreqHz)/1e6, float64(rep.RadioBwHz)/1e3, rep.RadioSF, rep.RadioCR)
	// If the viewer is signed in (via the identity beacon) and actually has access
	// to this repeater, offer a jump into the app instead of a sign-in CTA they
	// don't need. Owners get "Manage", other shared users get "Go to". See
	// docs/auth-cross-host.md.
	canManage, isOwner := false, false
	if uid := s.Auth.CurrentUserID(r.Context()); uid != 0 {
		if rep.OwnerID == uid {
			canManage, isOwner = true, true
		} else if _, err := s.Store.GetRepeaterForUser(r.Context(), uid, rep.ID); err == nil {
			canManage = true
		}
	}
	orgs, err := s.Store.ListRepeaterOrgs(r.Context(), rep.ID)
	if err != nil {
		s.ServerError(w, r, "could not load repeater", err)
		return
	}
	s.Render(w, r, "repeater_public.html", map[string]any{
		"Repeater":      rep,
		"Radio":         radio,
		"Stewards":      stewards,
		"CanManage":     canManage,
		"IsOwner":       isOwner,
		"Orgs":          orgs,
		"DocPublicHTML": web.Markdown(rep.DocPublic),
	})
}
