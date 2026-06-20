package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/store"
)

// pageAddRepeater drives the add-repeater wizard. Step 1 ("grant") is a
// mandatory acknowledgment that the owner has granted MeshTender admin on the
// repeater; step 2 ("details") collects the repeater's name/key/radio. The two
// post-creation steps (confirm, contribute) live on pageRepeaterAdded.
func (s *Server) pageAddRepeater(w http.ResponseWriter, r *http.Request) {
	step := r.URL.Query().Get("step")
	if step != "details" {
		step = "grant"
	}
	s.render(w, r, "add_repeater.html", map[string]any{
		"Step":            step,
		"ServerPubKey":    s.identity.PublicKeyHex(),
		"SetPermCommand":  s.identity.SetPermCommand(),
		"RevokeCommand":   s.identity.RevokePermCommand(),
		"Defaults":        s.cfg.DefaultRadio,
		"Presets":         radioPresets,
		"DefaultPresetID": defaultPresetID(s.cfg.DefaultRadio),
		"Error":           r.URL.Query().Get("error"),
	})
}

// pageRepeater shows a repeater's details: status, radio config, location,
// recent activity, and the actions available to the viewer. Owners get full
// management; users with shared/org access see a read-only view plus console.
func (s *Server) pageRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rep, err := s.store.GetRepeaterForUser(r.Context(), uid, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "could not load repeater", http.StatusInternalServerError)
		return
	}

	isOwner := !rep.Shared
	radio := fmt.Sprintf("%g MHz / %g kHz / SF%d / CR%d",
		float64(rep.RadioFreqHz)/1e6, float64(rep.RadioBwHz)/1e3, rep.RadioSF, rep.RadioCR)
	data := map[string]any{
		"Repeater": rep,
		"IsOwner":  isOwner,
		"Radio":    radio,
		"Error":    r.URL.Query().Get("error"),
	}
	if isOwner {
		orgs, err := s.store.ListRepeaterOrgs(r.Context(), id)
		if err != nil {
			http.Error(w, "could not load organizations", http.StatusInternalServerError)
			return
		}
		recent, err := s.store.ListCommandLog(r.Context(), id, 8)
		if err != nil {
			http.Error(w, "could not load activity", http.StatusInternalServerError)
			return
		}
		data["Orgs"] = orgs
		data["Recent"] = recent
	}
	s.render(w, r, "repeater.html", data)
}

func addErr(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/repeaters/add?step=details&error="+url.QueryEscape(msg), http.StatusSeeOther)
}

// pageRepeaterAdded is the wizard's final two steps for a freshly-added
// repeater: optionally confirm it with a modem now, and optionally contribute
// it to an organization the owner belongs to.
func (s *Server) pageRepeaterAdded(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rep, err := s.store.GetRepeaterOwned(r.Context(), uid, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	orgs, err := s.store.ListOrgsForUser(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load orgs", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "repeater_added.html", map[string]any{
		"Repeater":      rep,
		"Orgs":          orgs,
		"RevokeCommand": s.identity.RevokePermCommand(),
	})
}

// handleAddRepeater registers a new repeater (unconfirmed) for the current user.
func (s *Server) handleAddRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())

	name := strings.TrimSpace(r.FormValue("name"))
	pubHex := strings.ToLower(strings.TrimSpace(r.FormValue("public_key")))
	if name == "" {
		addErr(w, r, "Repeater name is required.")
		return
	}
	// Validate the public key is a real 32-byte MeshCore key.
	if _, err := meshcore.NewIdentityFromHex(pubHex); err != nil {
		addErr(w, r, "Public key must be 64 hex characters (a 32-byte MeshCore key).")
		return
	}

	freq, bw, sf, cr, ok := parseRadioForm(r)
	if !ok {
		addErr(w, r, "Radio parameters must be valid numbers.")
		return
	}

	storeLocation := r.FormValue("store_location") != ""
	rep, err := s.store.CreateRepeater(r.Context(), &store.Repeater{
		OwnerID:       uid,
		Name:          name,
		PublicKeyHex:  pubHex,
		RadioFreqHz:   freq,
		RadioBwHz:     bw,
		RadioSF:       int16(sf),
		RadioCR:       int16(cr),
		StoreLocation: storeLocation,
		PublicMap:     storeLocation && r.FormValue("public_map") != "",
	})
	if errors.Is(err, store.ErrDuplicate) {
		addErr(w, r, "You already added a repeater with that public key.")
		return
	}
	if err != nil {
		addErr(w, r, "Could not add repeater.")
		return
	}
	// Continue the wizard: offer to confirm and contribute.
	http.Redirect(w, r, "/repeaters/"+rep.PublicID+"/added", http.StatusSeeOther)
}

// parseRadioForm reads and validates the radio fields from a repeater form.
func parseRadioForm(r *http.Request) (freq, bw int64, sf, cr int16, ok bool) {
	f, e1 := strconv.ParseInt(r.FormValue("radio_freq_hz"), 10, 64)
	b, e2 := strconv.ParseInt(r.FormValue("radio_bw_hz"), 10, 64)
	s, e3 := strconv.ParseInt(r.FormValue("radio_sf"), 10, 16)
	c, e4 := strconv.ParseInt(r.FormValue("radio_cr"), 10, 16)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || f <= 0 || b <= 0 {
		return 0, 0, 0, 0, false
	}
	return f, b, int16(s), int16(c), true
}

// pageEditRepeater shows the edit form for an owned repeater.
func (s *Server) pageEditRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rep, err := s.store.GetRepeaterOwned(r.Context(), uid, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "edit_repeater.html", map[string]any{
		"Repeater":       rep,
		"Presets":        radioPresets,
		"SelectedPreset": defaultPresetID(config.RadioDefaults{FreqHz: uint32(rep.RadioFreqHz), BwHz: uint32(rep.RadioBwHz), SF: uint8(rep.RadioSF), CR: uint8(rep.RadioCR)}),
		"RevokeCommand":  s.identity.RevokePermCommand(),
		"Error":          r.URL.Query().Get("error"),
	})
}

// handleEditRepeater saves changes to an owned repeater's settings.
func (s *Server) handleEditRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	editErr := func(msg string) {
		http.Redirect(w, r, "/repeaters/"+repeaterParam(r)+"/edit?error="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		editErr("Repeater name is required.")
		return
	}
	freq, bw, sf, cr, valid := parseRadioForm(r)
	if !valid {
		editErr("Radio parameters must be valid numbers.")
		return
	}
	storeLocation := r.FormValue("store_location") != ""
	publicMap := r.FormValue("public_map") != ""
	if err := s.store.UpdateRepeater(r.Context(), uid, id, name, freq, bw, sf, cr, storeLocation, publicMap); err != nil {
		editErr("Could not save changes.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// pageDeleteRepeater shows a confirmation page before deleting, reminding the
// owner that removing the repeater here does not revoke MeshTender's access on
// the device.
func (s *Server) pageDeleteRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rep, err := s.store.GetRepeaterOwned(r.Context(), uid, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "delete_repeater.html", map[string]any{
		"Repeater":      rep,
		"RevokeCommand": s.identity.RevokePermCommand(),
	})
}

// handleDeleteRepeater removes a repeater the current user owns.
func (s *Server) handleDeleteRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		dashErr(w, r, "Invalid repeater.")
		return
	}
	if err := s.store.DeleteRepeaterOwned(r.Context(), uid, id); err != nil {
		dashErr(w, r, "Could not delete repeater (only the owner can).")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func dashErr(w http.ResponseWriter, r *http.Request, msg string) {
	http.Redirect(w, r, "/?error="+url.QueryEscape(msg), http.StatusSeeOther)
}
