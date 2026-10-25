package core

import (
	"errors"
	"net/http"

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/store"
)

// pageRadioEdit renders the org radio settings editor, prefilled with the
// org's radio or, when it has none, the default preset.
func (s *Handlers) pageRadioEdit(w http.ResponseWriter, r *http.Request) {
	orgID, ok := s.requireOrgAdmin(w, r)
	if !ok {
		return
	}
	org, err := s.Store.GetOrg(r.Context(), orgID)
	if err != nil {
		s.NotFound(w, r)
		return
	}
	radio, err := s.Store.OrgRadio(r.Context(), orgID)
	if err != nil {
		s.ServerError(w, r, "could not load radio settings", err)
		return
	}
	shown := defaultRadio()
	if radio != nil {
		shown = *radio
	}
	s.renderRadioEdit(w, r, org, shown, radio != nil, "")
}

// renderRadioEdit renders the editor (the initial GET and the error re-render):
// the modal fragment for htmx, the standalone page otherwise.
func (s *Handlers) renderRadioEdit(w http.ResponseWriter, r *http.Request, org *store.Org, shown config.RadioDefaults, isSet bool, errMsg string) {
	data := map[string]any{
		"Org":    org,
		"Nav":    s.orgAdminNav(r, org),
		"Action": "/orgs/" + org.Slug + "/config/radio",
		"Radio":  newRadioFields(shown, true),
		"IsSet":  isSet,
		"Error":  errMsg,
	}
	if r.Header.Get("HX-Request") != "" {
		data["Layout"] = "config-radio-modal"
	}
	s.Render(w, r, "config_radio_edit.html", data)
}

// handleSetRadio saves the org's radio settings, or clears them when the form
// asks to — refused while a profile still references them.
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
	if r.FormValue("clear") == "1" {
		err := s.Store.SetOrgRadio(r.Context(), orgID, nil)
		if errors.Is(err, store.ErrRadioInUse) {
			current, lerr := s.Store.OrgRadio(r.Context(), orgID)
			if lerr != nil || current == nil {
				s.ServerError(w, r, "could not load radio settings", lerr)
				return
			}
			s.renderRadioEdit(w, r, org, *current, true,
				"A profile uses these settings through its {{ radio.* }} placeholders. Remove them from every profile before clearing the radio settings.")
			return
		}
		if err != nil {
			s.ServerError(w, r, "could not clear radio settings", err)
			return
		}
		s.hxRedirect(w, r, "/orgs/"+orgParam(r)+"/config")
		return
	}
	radio, ok := parseRadio(r)
	if !ok {
		current, err := s.Store.OrgRadio(r.Context(), orgID)
		if err != nil {
			s.ServerError(w, r, "could not load radio settings", err)
			return
		}
		// Re-show what was entered (a value that didn't parse comes back as zero).
		s.renderRadioEdit(w, r, org, radio, current != nil, radioFormError)
		return
	}
	if err := s.Store.SetOrgRadio(r.Context(), orgID, &radio); err != nil {
		s.ServerError(w, r, "could not save radio settings", err)
		return
	}
	s.hxRedirect(w, r, "/orgs/"+orgParam(r)+"/config")
}
