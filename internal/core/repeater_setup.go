package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/store"
)

// The "set up from scratch over USB serial" path of the add-repeater wizard.
//
// Unlike the KISS/console path (which bridges a companion modem to the server
// and sends encrypted CLI over LoRa), here the browser talks the repeater's own
// plain-text serial CLI directly. The server's only jobs are (1) generating the
// ordered command list for the device and (2) creating the repeater record once
// the run succeeds. The repeater's private key is generated in the browser and
// never sent to the server — only its public key is.

// identityPlaceholder is the line the client replaces with the real
// `set prv.key <128-hex>` before running. We emit a placeholder rather than the
// key because the key never reaches the server. `<key>` keeps the preview
// readable; the client substitutes it and refuses to run if any `<…>` remains.
const identityPlaceholder = "set prv.key <key>"

// setupCommandsRequest is the JSON body the serial setup page posts to build the
// command list. OrgID 0 means "no org" (standalone repeater, radio from preset).
type setupCommandsRequest struct {
	Name    string   `json:"name"`
	OrgID   int64    `json:"orgId"`
	Profile string   `json:"profile"` // profile name within the org, optional
	Lat     *float64 `json:"lat"`
	Lon     *float64 `json:"lon"`
	// Radio preset values (MHz/kHz), used only when OrgID == 0.
	FreqMHz float64 `json:"freqMhz"`
	BwKHz   float64 `json:"bwKhz"`
	SF      int     `json:"sf"`
	CR      int     `json:"cr"`
}

// handleSetupCommands builds the ordered CLI command list for a from-scratch
// serial setup and returns it as JSON. The identity command is emitted as a
// placeholder for the client to fill in with the locally-generated private key.
func (s *Handlers) handleSetupCommands(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())

	var req setupCommandsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	var cmds []string
	cmds = append(cmds, "set name "+req.Name)

	// Resolve the authoritative radio settings (echoed back so the client can
	// store them on the repeater record). Radio + region come from the chosen org
	// config; a standalone repeater (no org) uses the radio preset and gets no
	// region hierarchy.
	radio := setupRadio{FreqMHz: req.FreqMHz, BwKHz: req.BwKHz, SF: req.SF, CR: req.CR}
	if req.OrgID != 0 {
		if _, isMember, err := s.Store.OrgRole(r.Context(), req.OrgID, uid); err != nil || !isMember {
			http.Error(w, "no access to that organization", http.StatusForbidden)
			return
		}
		var steps []string
		if req.Profile != "" {
			s2, err := s.profileSteps(r.Context(), req.OrgID, req.Profile)
			if err != nil {
				http.Error(w, "could not load profile", http.StatusInternalServerError)
				return
			}
			steps = s2
		}
		cmds = append(cmds, steps...)
		// Prefer the radio the profile sets on the device; if it sets none, fall
		// back to the default preset and add the command so the device still ends
		// up tunable (and the record radio is accurate).
		if rad, ok := parseProfileRadio(steps); ok {
			radio = rad
		} else {
			radio = defaultSetupRadio()
			cmds = append(cmds, radioCommand(radio))
		}
		regions, err := s.Store.ListRegions(r.Context(), req.OrgID)
		if err != nil {
			http.Error(w, "could not load regions", http.StatusInternalServerError)
			return
		}
		rootAllow, err := s.Store.RootAllowFlood(r.Context(), req.OrgID)
		if err != nil {
			http.Error(w, "could not load regions", http.StatusInternalServerError)
			return
		}
		cmds = append(cmds, store.RegionDefCommands(regions, rootAllow, req.Lat, req.Lon)...)
	} else {
		if radio.FreqMHz <= 0 || radio.BwKHz <= 0 {
			http.Error(w, "radio settings are required", http.StatusBadRequest)
			return
		}
		cmds = append(cmds, radioCommand(radio))
	}

	// Location: only set when the user picked a point.
	if req.Lat != nil && req.Lon != nil {
		cmds = append(cmds,
			"set lat "+strconv.FormatFloat(*req.Lat, 'f', 6, 64),
			"set lon "+strconv.FormatFloat(*req.Lon, 'f', 6, 64))
	}

	// Identity (client-spliced), then grant MeshTender admin, then reboot to
	// apply the new identity and radio.
	cmds = append(cmds, identityPlaceholder)
	cmds = append(cmds, s.Identity.SetPermCommand())
	cmds = append(cmds, "reboot")

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(setupCommandsResponse{
		Commands:            cmds,
		IdentityPlaceholder: identityPlaceholder,
		Radio:               radio,
	})
}

// setupRadio is a resolved radio configuration in the firmware's command units
// (MHz / kHz), echoed to the client so it can persist it on the record.
type setupRadio struct {
	FreqMHz float64 `json:"freqMhz"`
	BwKHz   float64 `json:"bwKhz"`
	SF      int     `json:"sf"`
	CR      int     `json:"cr"`
}

// setupCommandsResponse is the JSON handleSetupCommands returns: the ordered CLI
// lines (with identityPlaceholder standing in for the private-key command the
// client splices locally) and the resolved radio the client persists on save.
type setupCommandsResponse struct {
	Commands            []string   `json:"commands"`
	IdentityPlaceholder string     `json:"identityPlaceholder"`
	Radio               setupRadio `json:"radio"`
}

// setupCompleteResponse is the JSON handleSetupComplete returns after creating
// the repeater record: where the client should navigate next.
type setupCompleteResponse struct {
	Redirect string `json:"redirect"`
}

func defaultSetupRadio() setupRadio {
	p := defaultPreset()
	return setupRadio{FreqMHz: float64(p.FreqHz) / 1e6, BwKHz: float64(p.BwHz) / 1e3, SF: p.SF, CR: p.CR}
}

// parseProfileRadio extracts radio settings from a profile's `set radio
// <f,bw,sf,cr>` step, if present. Returns ok=false when the profile sets no
// radio (so the caller can fall back to a default).
func parseProfileRadio(steps []string) (setupRadio, bool) {
	for _, line := range steps {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "set radio ")
		if !ok {
			continue
		}
		parts := strings.Split(strings.TrimSpace(rest), ",")
		if len(parts) != 4 {
			continue
		}
		f, e1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		bw, e2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		sf, e3 := strconv.Atoi(strings.TrimSpace(parts[2]))
		cr, e4 := strconv.Atoi(strings.TrimSpace(parts[3]))
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		return setupRadio{FreqMHz: f, BwKHz: bw, SF: sf, CR: cr}, true
	}
	return setupRadio{}, false
}

// radioCommand renders the MeshCore `set radio <f,bw,sf,cr>` line. Frequency is
// in MHz and bandwidth in kHz (matching the firmware's units for this command).
func radioCommand(rad setupRadio) string {
	return "set radio " +
		strconv.FormatFloat(rad.FreqMHz, 'f', -1, 64) + "," +
		strconv.FormatFloat(rad.BwKHz, 'f', -1, 64) + "," +
		strconv.Itoa(rad.SF) + "," + strconv.Itoa(rad.CR)
}

// profileSteps returns the runnable command lines of a named profile in an org
// (comment-only steps are skipped).
func (s *Handlers) profileSteps(ctx context.Context, orgID int64, name string) ([]string, error) {
	profiles, err := s.Store.ListProfiles(ctx, orgID)
	if err != nil {
		return nil, err
	}
	for _, p := range profiles {
		if p.Name != name {
			continue
		}
		var out []string
		for _, st := range p.Steps {
			if st.IsComment() {
				continue
			}
			out = append(out, st.CommandLine)
		}
		return out, nil
	}
	return nil, nil
}

// handleSetupComplete creates the repeater record after a successful serial run.
// The private key stays in the browser; only the public key arrives here.
func (s *Handlers) handleSetupComplete(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())

	name := strings.TrimSpace(r.FormValue("name"))
	pubHex := strings.ToLower(strings.TrimSpace(r.FormValue("public_key")))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if _, err := meshcore.NewIdentityFromHex(pubHex); err != nil {
		http.Error(w, "public key must be 64 hex characters", http.StatusBadRequest)
		return
	}
	freq, bw, sf, cr, ok := parseRadioForm(r)
	if !ok {
		http.Error(w, "radio parameters must be valid numbers", http.StatusBadRequest)
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
		http.Error(w, "you already added a repeater with that public key", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "could not add repeater", http.StatusInternalServerError)
		return
	}

	// Persist the location the user picked on the map (we just wrote it to the
	// device, so we know it without an over-the-air confirm).
	if lat, err1 := strconv.ParseFloat(r.FormValue("lat"), 64); err1 == nil {
		if lon, err2 := strconv.ParseFloat(r.FormValue("lon"), 64); err2 == nil {
			_ = s.Store.SetRepeaterLocation(r.Context(), rep.ID, lat, lon)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(setupCompleteResponse{
		Redirect: "/repeaters/" + rep.PublicID + "/added",
	})
}
