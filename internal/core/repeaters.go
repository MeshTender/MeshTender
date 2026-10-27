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

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
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
		"Step":           step,
		"Method":         method,
		"ServerPubKey":   s.Identity.PublicKeyHex(),
		"SetPermCommand": s.Identity.SetPermCommand(),
		"RevokeCommand":  s.Identity.RevokePermCommand(),
		"Radio":          newRadioFields(defaultRadio(), true),
		"RadioSerial":    newRadioFields(defaultRadio(), false), // serial-setup.js reads the fields by id
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
// for the serial-setup config selector, in a single query. Best-effort: on a
// store error it returns nil (the selector renders with no orgs).
func (s *Handlers) setupOrgOptions(r *http.Request) []setupOrgOption {
	uid := s.Auth.CurrentUserID(r.Context())
	orgs, err := s.Store.ListOrgProfileNamesForUser(r.Context(), uid)
	if err != nil {
		return nil
	}
	out := make([]setupOrgOption, 0, len(orgs))
	for _, o := range orgs {
		out = append(out, setupOrgOption{ID: o.OrgID, Name: o.OrgName, Profiles: o.Profiles})
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
	}
	// When the owner has published a public page, surface its link plus a QR code
	// for it.
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
			s.ServerError(w, r, "could not load organizations", err)
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
	rep, id, ok := s.requireRepeaterOwned(w, r)
	if !ok {
		return
	}
	// Same per-org data (with participation/limit state) the share page uses, so
	// the wizard's "Manage access" buttons and status badges match it exactly.
	orgs, err := s.Store.ListRepeaterOrgMemberships(r.Context(), id)
	if err != nil {
		s.ServerError(w, r, "could not load organizations", err)
		return
	}
	s.Render(w, r, "repeater_added.html", map[string]any{
		"Next":     "added", // org-access-rows: Manage access comes back here
		"Repeater": rep,
		"Orgs":     orgs,
	})
}

// maxRepeaterNameLen bounds a repeater name to what MeshCore firmware can store
// and advertise. A node's name lives in a 32-byte buffer (`node_name[32]` in
// CommonCLI.h) and `set name` copies into it with StrHelper::strncpy(...,
// sizeof(node_name)), whose loop runs while buf_sz > 1 and then writes the NUL
// terminator — so 31 usable bytes. A longer name is silently truncated on the
// device, so we reject it here rather than record a name the hardware won't
// actually advertise. (Verified against the MeshCore firmware source.)
const maxRepeaterNameLen = 31

// cleanRepeaterName trims s and reports whether it is a usable repeater name:
// non-empty and within the firmware's byte limit.
func cleanRepeaterName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	return s, s != "" && len(s) <= maxRepeaterNameLen
}

// handleAddRepeater registers a new repeater (unconfirmed) for the current user.
func (s *Handlers) handleAddRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())

	name, ok := cleanRepeaterName(r.FormValue("name"))
	pubHex := strings.ToLower(strings.TrimSpace(r.FormValue("public_key")))
	if !ok {
		addErr(w, r, "Repeater name is required and must be 31 characters or fewer.")
		return
	}
	// Validate the public key is a real 32-byte MeshCore key.
	if _, err := meshcore.NewIdentityFromHex(pubHex); err != nil {
		addErr(w, r, "Public key must be 64 hex characters (a 32-byte MeshCore key).")
		return
	}

	freq, bw, sf, cr, ok := parseRadioForm(r)
	if !ok {
		addErr(w, r, radioFormError)
		return
	}

	rep, err := s.Store.CreateRepeater(r.Context(), &store.Repeater{
		OwnerID:          uid,
		Name:             name,
		PublicKeyHex:     pubHex,
		RadioFreqHz:      freq,
		RadioBwHz:        bw,
		RadioSF:          int16(sf),
		RadioCR:          int16(cr),
		ShowOnPublicOrg:  r.FormValue("show_on_public_org") != "",
		ExposePublicPage: r.FormValue("expose_public_page") != "",
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

// radioFormError is shown when the radio fields don't parse or are out of the
// range the firmware accepts (config.RadioDefaults.Valid).
const radioFormError = "Radio parameters must be ones MeshCore accepts: 150–2500 MHz, 7–500 kHz bandwidth, SF 5–12, CR 5–8."

// parseRadio reads and validates the shared radio fields ("radio-fields"
// template). Frequency and bandwidth are entered in MHz/kHz (matching the
// presets) and returned as Hz.
func parseRadio(r *http.Request) (config.RadioDefaults, bool) {
	mhz, e1 := strconv.ParseFloat(r.FormValue("radio_freq_mhz"), 64)
	khz, e2 := strconv.ParseFloat(r.FormValue("radio_bw_khz"), 64)
	sf, e3 := strconv.ParseUint(r.FormValue("radio_sf"), 10, 8)
	cr, e4 := strconv.ParseUint(r.FormValue("radio_cr"), 10, 8)
	// Range-check in float space first: converting an out-of-range float to an
	// integer is implementation-defined in Go.
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || !(mhz > 0 && mhz < 4000) || !(khz > 0 && khz < 4000) {
		return config.RadioDefaults{}, false
	}
	rd := config.RadioDefaults{FreqHz: uint32(math.Round(mhz * 1e6)), BwHz: uint32(math.Round(khz * 1e3)), SF: uint8(sf), CR: uint8(cr)}
	return rd, rd.Valid()
}

// parseRadioForm is parseRadio in a repeater record's column types.
func parseRadioForm(r *http.Request) (freq, bw int64, sf, cr int16, ok bool) {
	rd, ok := parseRadio(r)
	return int64(rd.FreqHz), int64(rd.BwHz), int16(rd.SF), int16(rd.CR), ok
}

// handleEditRepeater saves changes to an owned repeater's settings.
func (s *Handlers) handleEditRepeater(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		s.NotFound(w, r)
		return
	}
	editErr := func(msg string) {
		web.RedirectErr(w, r, settingsPath(repeaterParam(r), settingsDetails), msg)
	}
	name, ok := cleanRepeaterName(r.FormValue("name"))
	if !ok {
		editErr("Repeater name is required and must be 31 characters or fewer.")
		return
	}
	freq, bw, sf, cr, valid := parseRadioForm(r)
	if !valid {
		editErr(radioFormError)
		return
	}
	showOnPublicOrg := r.FormValue("show_on_public_org") != ""
	exposePublicPage := r.FormValue("expose_public_page") != ""
	if err := s.Store.UpdateRepeater(r.Context(), uid, id, name, freq, bw, sf, cr, showOnPublicOrg, exposePublicPage); err != nil {
		editErr("Could not save changes.")
		return
	}
	web.RedirectOK(w, r, settingsPath(repeaterParam(r), settingsDetails), "Details saved.")
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
