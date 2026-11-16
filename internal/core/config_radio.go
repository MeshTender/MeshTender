package core

import (
	"errors"
	"net/http"

	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
)

// handleSetRadio saves the org's radio settings from Settings › Radio, or
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
	back := settingsURL(org.Slug, "radio")
	if r.FormValue("clear") == "1" {
		err := s.Store.SetOrgRadio(r.Context(), orgID, nil)
		if errors.Is(err, store.ErrRadioInUse) {
			s.renderSettings(w, r, org, "radio", map[string]any{
				"RadioError": "A profile uses these settings through its {{ radio.* }} placeholders. Remove them from every profile before clearing the radio settings.",
			})
			return
		}
		if err != nil {
			s.ServerError(w, r, "could not clear radio settings", err)
			return
		}
		web.RedirectOK(w, r, back, "Radio settings cleared.")
		return
	}
	radio, ok := parseRadio(r)
	if !ok {
		// Re-show what was entered (a value that didn't parse comes back as zero).
		s.renderSettings(w, r, org, "radio", map[string]any{
			"RadioFields": newRadioFields(radio, true), "RadioError": radioFormError,
		})
		return
	}
	if err := s.Store.SetOrgRadio(r.Context(), orgID, &radio); err != nil {
		s.ServerError(w, r, "could not save radio settings", err)
		return
	}
	web.RedirectOK(w, r, back, "Radio settings saved.")
}

// handleSetPathHash saves the org's path hash mode from Settings › Radio, or
// clears it when "Not set" is chosen — refused while a profile still uses
// {{ path.hash.mode }}.
func (s *Handlers) handleSetPathHash(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	back := settingsURL(org.Slug, "radio")
	var mode *int
	switch v := r.FormValue("path_hash_mode"); v {
	case "":
	case "0", "1", "2":
		m := int(v[0] - '0')
		mode = &m
	default:
		s.renderSettings(w, r, org, "radio", map[string]any{"PathHashError": "Pick a path hash size of 1, 2 or 3 bytes."})
		return
	}
	err = s.Store.SetOrgPathHashMode(r.Context(), orgID, mode)
	if errors.Is(err, store.ErrPathHashInUse) {
		s.renderSettings(w, r, org, "radio", map[string]any{
			"PathHashError": "A profile uses this through {{ path.hash.mode }}. Remove it from every profile before clearing the path hash size.",
		})
		return
	}
	if err != nil {
		s.ServerError(w, r, "could not save the path hash size", err)
		return
	}
	web.RedirectOK(w, r, back, "Path hash size saved.")
}
