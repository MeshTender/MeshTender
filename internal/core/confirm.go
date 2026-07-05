package core

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
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/jleight/meshtender/internal/mesh"
	"github.com/jleight/meshtender/internal/web"
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

// applyUserPath seeds the exchanger with a caller-supplied route (the optional
// ?path= query param from the confirm/console page) so the login and commands
// route directly with flood fallback. A malformed path is reported and ignored
// (we fall back to flood) rather than failing the session. It returns whether a
// path was set, so the caller can report whether that path actually worked.
func applyUserPath(ex *mesh.Exchanger, r *http.Request, bridge *wsbridge.Conn) bool {
	raw := r.URL.Query().Get("path")
	if raw == "" {
		return false
	}
	path, pathLen, err := mesh.ParsePath(raw)
	if err != nil {
		_ = bridge.Status("warning", "Ignoring the path you entered ("+err.Error()+") — using flood.")
		return false
	}
	if path == nil {
		return false
	}
	ex.SetPath(path, pathLen)
	_ = bridge.Status("info", "Using the path you specified (direct routing, with flood fallback).")
	return true
}

// reportPathOutcome logs whether the login reached the repeater over the
// user-supplied path (a direct RESPONSE reply) or had to fall back to flood (a
// PATH return reply). Only meaningful when a path was set and login succeeded.
func reportPathOutcome(bridge *wsbridge.Conn, lr *mesh.LoginResponse) {
	if lr.FromPath {
		_ = bridge.Status("warning", "The path you specified didn't get through — reached the repeater by flood instead.")
	} else {
		_ = bridge.Status("info", "Reached the repeater directly over the path you specified. ✓")
	}
}

// pageConfirm renders the WebSerial confirm page for a repeater the user can access.
func (s *Handlers) pageConfirm(w http.ResponseWriter, r *http.Request) {
	rep, _, ok := s.requireRepeaterAccess(w, r)
	if !ok {
		return
	}
	s.Render(w, r, "confirm.html", map[string]any{
		"Repeater": rep,
		"Debug":    r.URL.Query().Get("debug") == "1",
	})
}

// wsConfirm runs the live login round-trip over a WebSocket bridged to the
// browser's WebSerial-attached KISS modem.
func (s *Handlers) wsConfirm(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rep, err := s.Store.GetRepeaterForUser(r.Context(), uid, id)
	if err != nil {
		http.Error(w, "no access", http.StatusForbidden)
		return
	}
	repeaterID, err := meshcore.NewIdentityFromHex(rep.PublicKeyHex)
	if err != nil {
		s.ServerError(w, r, "stored repeater key invalid", err)
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
	defer func() { _ = modem.Close() }()

	server := s.Identity.Local()
	debug := r.URL.Query().Get("debug") == "1"

	// All sends (login + location queries) go through one exchanger: rate-limited,
	// monotonic timestamps, automatic retry of lost packets.
	ex := mesh.NewExchanger(modem, server, repeaterID, sendInterval, perTryReply, maxSendTries)
	modem.SetDataHandler(func(data []byte, _ float32, _ int8, _ bool) {
		ex.HandleData(data)
	})
	userPathSet := applyUserPath(ex, r, bridge)
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
		FreqHz: uint32(rep.RadioFreqHz), //nolint:gosec // G115: radio config value is bounded (preset-constrained)
		BwHz:   uint32(rep.RadioBwHz),   //nolint:gosec // G115: radio config value is bounded (preset-constrained)
		SF:     uint8(rep.RadioSF),      //nolint:gosec // G115: radio config value is bounded (preset-constrained)
		CR:     uint8(rep.RadioCR),      //nolint:gosec // G115: radio config value is bounded (preset-constrained)
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
	if userPathSet {
		reportPathOutcome(bridge, lr)
	}

	if err := s.Store.SetRepeaterConfirmed(ctx, id, uid, lr.IsAdmin, int16(lr.Permissions)); err != nil {
		web.LogError(r, "confirm: save confirmation", err, "repeater_id", id)
		_ = bridge.Status("error", "could not save confirmation: "+err.Error())
		return
	}
	if debug {
		_ = bridge.Status("debug", fmt.Sprintf("login reply fromPath=%v admin=%v perms=%d", lr.FromPath, lr.IsAdmin, lr.Permissions))
	}
	if !lr.IsAdmin {
		// Guests can't run CLI commands (including get lat/lon), so there's
		// nothing more to do — stop here rather than fruitlessly querying.
		msg := fmt.Sprintf("Repeater reached, but MeshTender only has GUEST access (permissions=%d). Guest is open to anyone with a blank password, so MeshTender can't administer this repeater — re-run `%s` to grant admin.", lr.Permissions, s.Identity.SetPermCommand())
		if s.Cfg.RootHost != "" {
			msg += " See " + s.Origin(r, s.Cfg.RootHost) + "/docs#setperm for help."
		}
		_ = bridge.Status("warning", msg)
		return
	}
	_ = bridge.Status("confirmed", "Repeater reached with admin access. ✓")

	// Fetch and store the repeater's location. Each coordinate is a separate query
	// so progress (and retries) are visible.
	{
		fetchCoord := func(label, cmd string, accept func(text string) bool) (float64, bool) {
			reply, err := ex.CommandAccept(ctx, cmd, accept, func(attempt, max int) {
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
		lat, okLat := fetchCoord("latitude", "get lat", nil)
		// A slow latitude fetch is retried, which makes the repeater re-run "get
		// lat" and emit duplicate replies; one can straggle in during the "get
		// lon" wait and be misread as the longitude (storing lat,lat). Since the
		// two coordinates differ, reject a longitude reply whose value equals the
		// latitude we just read and keep waiting for the genuine reply.
		lon, okLon := fetchCoord("longitude", "get lon", func(text string) bool {
			f, ok := parseLocationFloat(text)
			if ok && okLat && f == lat {
				if debug {
					_ = bridge.Status("debug", "ignored a stale 'get lat' reply while awaiting longitude")
				}
				return false
			}
			return true
		})
		if okLat && okLon {
			if err := s.Store.SetRepeaterLocation(ctx, id, lat, lon); err != nil {
				web.LogError(r, "confirm: store location", err, "repeater_id", id)
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
