package core

import (
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"image/color"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// pageAddRepeater drives the add-repeater wizard. Step 1 ("grant") is a
// mandatory acknowledgment that the owner has granted MeshTender admin on the
// repeater; step 2 ("details") collects the repeater's name/key/radio. The two
// post-creation steps (confirm, contribute) live on pageRepeaterAdded.
func (s *Handlers) pageAddRepeater(w http.ResponseWriter, r *http.Request) {
	step := r.URL.Query().Get("step")
	if step != "details" {
		step = "grant"
	}
	s.Render(w, r, "add_repeater.html", map[string]any{
		"Step":            step,
		"ServerPubKey":    s.Identity.PublicKeyHex(),
		"SetPermCommand":  s.Identity.SetPermCommand(),
		"RevokeCommand":   s.Identity.RevokePermCommand(),
		"Defaults":        defaultPreset(),
		"Presets":         radioPresets,
		"DefaultPresetID": defaultPresetID,
		"Error":           r.URL.Query().Get("error"),
	})
}

// pageRepeater shows a repeater's details: status, radio config, location,
// recent activity, and the actions available to the viewer. Owners get full
// management; users with shared/org access see a read-only view plus console.
func (s *Handlers) pageRepeater(w http.ResponseWriter, r *http.Request) {
	rep, id, ok := s.requireRepeaterAccess(w, r)
	if !ok {
		return
	}

	isOwner := !rep.Shared
	radio := fmt.Sprintf("%g MHz / %g kHz / SF%d / CR%d",
		float64(rep.RadioFreqHz)/1e6, float64(rep.RadioBwHz)/1e3, rep.RadioSF, rep.RadioCR)
	contactURI := repeaterContactURI(rep)
	data := map[string]any{
		"Repeater":   rep,
		"IsOwner":    isOwner,
		"Radio":      radio,
		"ContactURI": contactURI,
		"Error":      r.URL.Query().Get("error"),
	}
	// QR code that adds the repeater as a MeshCore contact. Embedded as a data
	// URI so it needs no extra route or asset; if encoding fails the page just
	// renders without it. Light modules on a transparent quiet zone so it sits on
	// the dark card instead of a stark white block (scanners decode inverted QR).
	if qr, err := qrcode.New(contactURI, qrcode.Medium); err == nil {
		qr.BackgroundColor = color.Transparent
		qr.ForegroundColor = color.RGBA{R: 0x8a, G: 0x97, B: 0xa8, A: 0xff}
		if png, err := qr.PNG(256); err == nil {
			data["ContactQR"] = template.URL("data:image/png;base64," + base64.StdEncoding.EncodeToString(png))
		}
	}
	if isOwner {
		orgs, err := s.Store.ListRepeaterOrgs(r.Context(), id)
		if err != nil {
			http.Error(w, "could not load organizations", http.StatusInternalServerError)
			return
		}
		recent, err := s.Store.ListCommandLog(r.Context(), id, 8)
		if err != nil {
			http.Error(w, "could not load activity", http.StatusInternalServerError)
			return
		}
		data["Orgs"] = orgs
		data["Recent"] = recent
	}
	s.Render(w, r, "repeater.html", data)
}

// repeaterContactURI builds the meshcore:// deep link that adds the repeater as
// a contact in the MeshCore app. type=2 is MeshCore's repeater contact type.
func repeaterContactURI(rep *store.Repeater) string {
	q := url.Values{}
	q.Set("name", rep.Name)
	q.Set("public_key", rep.PublicKeyHex)
	q.Set("type", "2")
	return "meshcore://contact/add?" + q.Encode()
}

func addErr(w http.ResponseWriter, r *http.Request, msg string) {
	web.RedirectErr(w, r, "/repeaters/add?step=details", msg)
}

// pageRepeaterAdded is the wizard's final two steps for a freshly-added
// repeater: optionally confirm it with a modem now, and optionally contribute
// it to an organization the owner belongs to.
func (s *Handlers) pageRepeaterAdded(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	rep, _, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	orgs, err := s.Store.ListOrgsForUser(r.Context(), uid)
	if err != nil {
		http.Error(w, "could not load orgs", http.StatusInternalServerError)
		return
	}
	s.Render(w, r, "repeater_added.html", map[string]any{
		"Repeater":      rep,
		"Orgs":          orgs,
		"RevokeCommand": s.Identity.RevokePermCommand(),
	})
}

// handleAddRepeater registers a new repeater (unconfirmed) for the current user.
func (s *Handlers) handleAddRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())

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
	rep, err := s.Store.CreateRepeater(r.Context(), &store.Repeater{
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
func (s *Handlers) pageEditRepeater(w http.ResponseWriter, r *http.Request) {
	rep, _, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	s.Render(w, r, "edit_repeater.html", map[string]any{
		"Repeater":       rep,
		"Presets":        radioPresets,
		"SelectedPreset": presetIDFor(config.RadioDefaults{FreqHz: uint32(rep.RadioFreqHz), BwHz: uint32(rep.RadioBwHz), SF: uint8(rep.RadioSF), CR: uint8(rep.RadioCR)}),
		"RevokeCommand":  s.Identity.RevokePermCommand(),
		"Error":          r.URL.Query().Get("error"),
	})
}

// handleEditRepeater saves changes to an owned repeater's settings.
func (s *Handlers) handleEditRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	editErr := func(msg string) {
		web.RedirectErr(w, r, "/repeaters/"+repeaterParam(r)+"/edit", msg)
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
	if err := s.Store.UpdateRepeater(r.Context(), uid, id, name, freq, bw, sf, cr, storeLocation, publicMap); err != nil {
		editErr("Could not save changes.")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// pageDeleteRepeater shows a confirmation page before deleting, reminding the
// owner that removing the repeater here does not revoke MeshTender's access on
// the device.
func (s *Handlers) pageDeleteRepeater(w http.ResponseWriter, r *http.Request) {
	rep, _, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	s.Render(w, r, "delete_repeater.html", map[string]any{
		"Repeater":      rep,
		"RevokeCommand": s.Identity.RevokePermCommand(),
	})
}

// handleDeleteRepeater removes a repeater the current user owns.
func (s *Handlers) handleDeleteRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		dashErr(w, r, "Invalid repeater.")
		return
	}
	if err := s.Store.DeleteRepeaterOwned(r.Context(), uid, id); err != nil {
		dashErr(w, r, "Could not delete repeater (only the owner can).")
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func dashErr(w http.ResponseWriter, r *http.Request, msg string) {
	web.RedirectErr(w, r, "/", msg)
}
