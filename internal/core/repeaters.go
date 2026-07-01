package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

// setupOrgOption is an org the user belongs to, with the names of its config
// profiles, offered in the serial-setup config selector.
type setupOrgOption struct {
	ID       int64
	Name     string
	Profiles []string
}

// pageAddRepeater drives the add-repeater wizard:
//
//	method  — choose how you're connecting (USB serial vs KISS/remote).
//	consent — acknowledge MeshTender admin access (it grants this for you on the
//	          serial path; you run it yourself on the KISS path).
//	serial  — configure a brand-new repeater over USB and run the setup.
//	details — register an already-on-network repeater (the original KISS flow).
//
// The two post-creation steps (confirm, contribute) live on pageRepeaterAdded.
func (s *Handlers) pageAddRepeater(w http.ResponseWriter, r *http.Request) {
	step := r.URL.Query().Get("step")
	switch step {
	case "consent", "serial", "details":
	default:
		step = "method"
	}
	method := r.URL.Query().Get("method")
	if method != "serial" {
		method = "kiss"
	}
	data := map[string]any{
		"Step":            step,
		"Method":          method,
		"ServerPubKey":    s.Identity.PublicKeyHex(),
		"SetPermCommand":  s.Identity.SetPermCommand(),
		"RevokeCommand":   s.Identity.RevokePermCommand(),
		"Defaults":        defaultPreset(),
		"Presets":         radioPresets,
		"DefaultPresetID": defaultPresetID,
		"Error":           r.URL.Query().Get("error"),
	}
	if step == "serial" {
		orgs := s.setupOrgOptions(r)
		data["Orgs"] = orgs
		if b, err := json.Marshal(orgs); err == nil {
			data["OrgsJS"] = template.JS(b) //nolint:gosec // G203: b is json.Marshal output (Go escapes <>& in JS context)
		} else {
			data["OrgsJS"] = template.JS("[]")
		}
	}
	s.Render(w, r, "add_repeater.html", data)
}

// setupOrgOptions lists the current user's orgs with their config profile names,
// for the serial-setup config selector. Best-effort: an org whose profiles fail
// to load is included with none.
func (s *Handlers) setupOrgOptions(r *http.Request) []setupOrgOption {
	uid := s.Auth.CurrentUserID(r.Context())
	memberships, err := s.Store.ListOrgsForUser(r.Context(), uid)
	if err != nil {
		return nil
	}
	out := make([]setupOrgOption, 0, len(memberships))
	for _, m := range memberships {
		opt := setupOrgOption{ID: m.Org.ID, Name: m.Org.Name}
		if profiles, err := s.Store.ListProfiles(r.Context(), m.Org.ID); err == nil {
			for _, p := range profiles {
				opt.Profiles = append(opt.Profiles, p.Name)
			}
		}
		out = append(out, opt)
	}
	return out
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
		"Nav":        web.RepeaterNav(rep.PublicID, rep.Name, rep.OwnerName(), isOwner, "overview"),
		"Radio":      radio,
		"ContactURI": contactURI,
		"Error":      r.URL.Query().Get("error"),
	}
	// When the owner has published a public page, surface its link plus a QR code
	// to print and place inside the enclosure (the NFC/QR tap target).
	if isOwner && rep.ExposePublicPage {
		publicURL := s.Origin(r, s.rootHost()) + "/r/" + rep.PublicID
		data["PublicPageURL"] = publicURL
		if qr, ok := web.QRDataURI(publicURL); ok {
			data["PublicPageQR"] = qr
		}
	}
	// QR code that adds the repeater as a MeshCore contact. Embedded as a data
	// URI so it needs no extra route or asset; if encoding fails the page just
	// renders without it.
	if qr, ok := web.QRDataURI(contactURI); ok {
		data["ContactQR"] = qr
	}
	if isOwner {
		orgs, err := s.Store.ListRepeaterOrgs(r.Context(), id)
		if err != nil {
			http.Error(w, "could not load organizations", http.StatusInternalServerError)
			return
		}
		data["Orgs"] = orgs
	}
	s.Render(w, r, "repeater.html", data)
}

// repeaterContactURI builds the meshcore:// deep link that adds the repeater as
// a contact in the MeshCore app. type=2 is MeshCore's repeater contact type.
func repeaterContactURI(rep *store.Repeater) string {
	return web.MeshCoreContactURI(rep.Name, rep.PublicKeyHex, int(meshcore.AdvertTypeRepeater))
}

func addErr(w http.ResponseWriter, r *http.Request, msg string) {
	web.RedirectErr(w, r, "/repeaters/add?step=details", msg)
}

// pageRepeaterAdded is the wizard's final step (step 3) for a freshly-added
// repeater: optionally confirm it with a modem now, and manage which organizations
// it's shared with. People-sharing lives on the repeater's own sharing page.
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
		"Repeater": rep,
		"Orgs":     orgs,
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

	rep, err := s.Store.CreateRepeater(r.Context(), &store.Repeater{
		OwnerID:         uid,
		Name:            name,
		PublicKeyHex:    pubHex,
		RadioFreqHz:     freq,
		RadioBwHz:       bw,
		RadioSF:         int16(sf),
		RadioCR:         int16(cr),
		ShowOnPublicOrg: r.FormValue("show_on_public_org") != "",
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
// Frequency and bandwidth are entered in MHz/kHz (matching the region presets)
// and stored as Hz.
func parseRadioForm(r *http.Request) (freq, bw int64, sf, cr int16, ok bool) {
	mhz, e1 := strconv.ParseFloat(r.FormValue("radio_freq_mhz"), 64)
	khz, e2 := strconv.ParseFloat(r.FormValue("radio_bw_khz"), 64)
	s, e3 := strconv.ParseInt(r.FormValue("radio_sf"), 10, 16)
	c, e4 := strconv.ParseInt(r.FormValue("radio_cr"), 10, 16)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || mhz <= 0 || khz <= 0 {
		return 0, 0, 0, 0, false
	}
	return int64(math.Round(mhz * 1e6)), int64(math.Round(khz * 1e3)), int16(s), int16(c), true
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
		"SelectedPreset": presetIDFor(config.RadioDefaults{FreqHz: uint32(rep.RadioFreqHz), BwHz: uint32(rep.RadioBwHz), SF: uint8(rep.RadioSF), CR: uint8(rep.RadioCR)}), //nolint:gosec // G115: radio config value is bounded (preset-constrained)
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
	showOnPublicOrg := r.FormValue("show_on_public_org") != ""
	exposePublicPage := r.FormValue("expose_public_page") != ""
	if err := s.Store.UpdateRepeater(r.Context(), uid, id, name, freq, bw, sf, cr, showOnPublicOrg, exposePublicPage); err != nil {
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

// rootHost returns the host that serves public pages (the marketing/root host),
// falling back to the primary host if no separate root host is configured.
func (s *Handlers) rootHost() string {
	if s.Cfg.RootHost != "" {
		return s.Cfg.RootHost
	}
	return s.Cfg.PrimaryHost
}
