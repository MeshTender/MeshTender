package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/jleight/meshtender/internal/mesh"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/wsbridge"
)

// confirmTimeout bounds a single confirm session (login + optional location
// fetch, each with retries). Sized to comfortably exceed the worst case of a
// few fully-failed exchanges at maxSendTries × perTryReply.
const confirmTimeout = 150 * time.Second

// Packet send tuning, shared by confirm and console. perTryReply is how long we
// wait for a reply before resending (a var so tests can shorten it);
// maxSendTries is the maximum number of sends per request.
var perTryReply = 10 * time.Second

const maxSendTries = 4

// pageConfirm renders the WebSerial confirm page for a repeater the user can access.
func (s *Server) pageConfirm(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
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
	s.render(w, r, "confirm.html", map[string]any{
		"Repeater": rep,
		"Debug":    r.URL.Query().Get("debug") == "1",
	})
}

// wsConfirm runs the live login round-trip over a WebSocket bridged to the
// browser's WebSerial-attached KISS modem.
func (s *Server) wsConfirm(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rep, err := s.store.GetRepeaterForUser(r.Context(), uid, id)
	if err != nil {
		http.Error(w, "no access", http.StatusForbidden)
		return
	}
	repeaterID, err := meshcore.NewIdentityFromHex(rep.PublicKeyHex)
	if err != nil {
		http.Error(w, "stored repeater key invalid", http.StatusInternalServerError)
		return
	}

	ws, err := websocket.Accept(w, r, nil) // same-origin (request host) authorized by default
	if err != nil {
		return
	}

	// A connection-lifetime context, independent of the request context which
	// is unsafe to use after Accept.
	ctx, cancel := context.WithTimeout(context.Background(), confirmTimeout)
	defer cancel()

	bridge := wsbridge.New(ctx, ws)
	// Disable TX flow control: it blocks SendData until the modem emits a
	// HW_RESP_TX_DONE event, which not all KISS firmwares send — causing a
	// spurious "tx done timeout". We don't need it; the repeater's reply is the
	// real confirmation we wait for.
	modem := hardware.NewKissModem(bridge, hardware.WithTxFlowControl(0))
	defer modem.Close()

	server := s.identity.Local()
	debug := r.URL.Query().Get("debug") == "1"

	// All sends (login + location queries) go through one exchanger: rate-limited,
	// monotonic timestamps, automatic retry of lost packets.
	ex := mesh.NewExchanger(modem, server, repeaterID, sendInterval, perTryReply, maxSendTries)
	modem.SetDataHandler(func(data []byte, _ float32, _ int8, _ bool) {
		ex.HandleData(data)
	})
	if debug {
		// Dump every inbound KISS frame as hex so we can see exactly what the
		// modem reports back (e.g. whether the repeater replies at all).
		bridge.SetObserver(func(f *hardware.KissFrame) {
			_ = bridge.Status("debug", fmt.Sprintf("rx frame cmd=0x%02x len=%d data=%x", f.Command, len(f.Data), f.Data))
		})
	}

	if err := modem.Connect(ctx); err != nil {
		_ = bridge.Status("error", "modem connect: "+err.Error())
		return
	}

	ready := make(chan struct{}, 1)

	// Socket read loop: binary = serial bytes, text = browser control frames.
	go func() {
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				bridge.MarkDead()
				cancel()
				return
			}
			switch typ {
			case websocket.MessageBinary:
				bridge.Feed(data)
			case websocket.MessageText:
				var msg struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(data, &msg) == nil && msg.Type == "ready" {
					select {
					case ready <- struct{}{}:
					default:
					}
				}
			}
		}
	}()

	// Wait for the browser to report the serial port is open.
	select {
	case <-ready:
	case <-ctx.Done():
		return
	}

	_ = bridge.Status("info", "Tuning radio…")
	if err := modem.SetRadio(&hardware.RadioConfig{
		FreqHz: uint32(rep.RadioFreqHz),
		BwHz:   uint32(rep.RadioBwHz),
		SF:     uint8(rep.RadioSF),
		CR:     uint8(rep.RadioCR),
	}); err != nil {
		_ = bridge.Status("error", "set radio: "+err.Error())
		return
	}

	// Log in (retried internally on lost packets).
	lr, err := ex.Login(ctx, "", func(attempt, max int) {
		if attempt == 1 {
			_ = bridge.Status("info", "Sending login to repeater…")
		} else {
			_ = bridge.Status("info", fmt.Sprintf("No reply yet — retrying login (%d/%d)…", attempt, max))
		}
	})
	if errors.Is(err, mesh.ErrNoReply) {
		_ = bridge.Status("timeout", "No reply from the repeater after several tries. Check that the modem is on the repeater's frequency/SF/BW and that MeshTender has been granted access (setperm).")
		return
	}
	if err != nil {
		return // context cancelled or a build/transmit error already reported
	}

	if err := s.store.SetRepeaterConfirmed(ctx, id, lr.IsAdmin, int16(lr.Permissions)); err != nil {
		_ = bridge.Status("error", "could not save confirmation: "+err.Error())
		return
	}
	if debug {
		_ = bridge.Status("debug", fmt.Sprintf("login reply fromPath=%v admin=%v perms=%d", lr.FromPath, lr.IsAdmin, lr.Permissions))
	}
	if !lr.IsAdmin {
		// Guests can't run CLI commands (including get lat/lon), so there's
		// nothing more to do — stop here rather than fruitlessly querying.
		_ = bridge.Status("warning", fmt.Sprintf("Repeater reached, but MeshTender only has GUEST access (permissions=%d). Guest is open to anyone with a blank password, so MeshTender can't administer this repeater — re-run `setperm <key> 3` to grant admin.", lr.Permissions))
		return
	}
	_ = bridge.Status("confirmed", "Repeater reached with admin access. ✓")

	// Optionally fetch and store the repeater's location (owner consented). Each
	// coordinate is a separate query so progress (and retries) are visible.
	if rep.StoreLocation {
		fetchCoord := func(label, cmd string) (float64, bool) {
			reply, err := ex.Command(ctx, cmd, func(attempt, max int) {
				if attempt == 1 {
					_ = bridge.Status("info", "Fetching "+label+"…")
				} else {
					_ = bridge.Status("info", fmt.Sprintf("Fetching %s — retry %d/%d…", label, attempt, max))
				}
			})
			if err != nil {
				return 0, false
			}
			return parseLocationFloat(reply)
		}
		lat, okLat := fetchCoord("latitude", "get lat")
		lon, okLon := fetchCoord("longitude", "get lon")
		if okLat && okLon {
			if err := s.store.SetRepeaterLocation(ctx, id, lat, lon); err != nil {
				_ = bridge.Status("error", "could not store location: "+err.Error())
			} else {
				_ = bridge.Status("info", fmt.Sprintf("Stored location: %.5f, %.5f", lat, lon))
			}
		} else {
			_ = bridge.Status("warning", "Could not read a location from the repeater.")
		}
	}
}

// parseLocationFloat parses a "get lat"/"get lon" reply like "> 37.7749".
func parseLocationFloat(reply string) (float64, bool) {
	s := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(reply), ">"))
	if i := strings.IndexAny(s, " \t\r\n"); i >= 0 {
		s = s[:i]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
