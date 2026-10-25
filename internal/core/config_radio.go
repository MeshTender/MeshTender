package core

import (
	"errors"
	"net/http"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// handleSetRadio saves the org's radio settings from Settings › Profile, or
// clears them when the form asks to — refused while a profile still references
// them. A rejected form re-shows what was entered with the error.
func (s *Handlers) handleSetRadio(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	back := settingsURL(org.Slug, "profile")
	if r.FormValue("clear") == "1" {
		err := s.Store.SetOrgRadio(r.Context(), orgID, nil)
		if errors.Is(err, store.ErrRadioInUse) {
			s.renderSettings(w, r, org, "profile", map[string]any{
				"RadioError": "A profile uses these settings through its {{ radio.* }} placeholders. Remove them from every profile before clearing the radio settings.",
			})
			return
		}
		if err != nil {
			s.ServerError(w, r, "could not clear radio settings", err)
			return
		}
		web.RedirectFlash(w, r, back, "ok", "Radio settings cleared.")
		return
	}
	radio, ok := parseRadio(r)
	if !ok {
		// Re-show what was entered (a value that didn't parse comes back as zero).
		s.renderSettings(w, r, org, "profile", map[string]any{
			"RadioFields": newRadioFields(radio, true), "RadioError": radioFormError,
		})
		return
	}
	if err := s.Store.SetOrgRadio(r.Context(), orgID, &radio); err != nil {
		s.ServerError(w, r, "could not save radio settings", err)
		return
	}
	web.RedirectFlash(w, r, back, "ok", "Radio settings saved.")
}
