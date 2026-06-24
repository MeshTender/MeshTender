package marketing

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/jleight/meshtender/internal/store"
)

// pageRepeaterPublic renders a repeater's public page — the NFC/QR tap target —
// for anonymous visitors on the root host. It exists only when the owner opted
// in (expose_public_page); otherwise it 404s like any unknown resource. It shows
// only public-safe information: what the node is, where it is, the public
// documentation, and who stewards it. Internal notes never reach this surface.
func (s *Handlers) pageRepeaterPublic(w http.ResponseWriter, r *http.Request) {
	rep, err := s.Store.GetRepeaterPublic(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "could not load repeater", http.StatusInternalServerError)
		return
	}
	stewards, err := s.Store.ListStewards(r.Context(), rep.ID)
	if err != nil {
		http.Error(w, "could not load repeater", http.StatusInternalServerError)
		return
	}
	radio := fmt.Sprintf("%g MHz / %g kHz / SF%d / CR%d",
		float64(rep.RadioFreqHz)/1e6, float64(rep.RadioBwHz)/1e3, rep.RadioSF, rep.RadioCR)
	s.Render(w, r, "repeater_public.html", map[string]any{
		"Repeater": rep,
		"Radio":    radio,
		"Stewards": stewards,
	})
}
