package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/jleight/meshtender/internal/mesh"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/wsbridge"
)

// confirmTimeout bounds a single confirm session.
const confirmTimeout = 60 * time.Second

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
	var confirmed atomic.Bool
	debug := r.URL.Query().Get("debug") == "1"

	if debug {
		// Dump every inbound KISS frame as hex so we can see exactly what the
		// modem reports back (e.g. whether the repeater replies at all).
		bridge.SetObserver(func(f *hardware.KissFrame) {
			_ = bridge.Status("debug", fmt.Sprintf("rx frame cmd=0x%02x len=%d data=%x", f.Command, len(f.Data), f.Data))
		})
	}

	modem.SetDataHandler(func(data []byte, snr float32, rssi int8, hasSig bool) {
		lr, err := mesh.DecodeLoginResponse(server, repeaterID, data)
		if errors.Is(err, mesh.ErrNotForUs) {
			if debug {
				meta := ""
				if hasSig {
					meta = fmt.Sprintf(" (SNR %.1f dB, RSSI %d)", snr, rssi)
				}
				_ = bridge.Status("debug", fmt.Sprintf("heard a packet not addressed to us%s: %x", meta, data))
			}
			return // overheard traffic; keep listening
		}
		if err != nil {
			_ = bridge.Status("error", "decode failed: "+err.Error())
			return
		}
		if confirmed.Swap(true) {
			return // already handled
		}
		if debug {
			_ = bridge.Status("debug", fmt.Sprintf("reply decrypted (fromPath=%v): %x → admin=%v perms=%d", lr.FromPath, lr.Plaintext, lr.IsAdmin, lr.Permissions))
		}
		if err := s.store.SetRepeaterConfirmed(ctx, id, lr.IsAdmin, int16(lr.Permissions)); err != nil {
			_ = bridge.Status("error", "could not save confirmation: "+err.Error())
			return
		}
		if lr.IsAdmin {
			_ = bridge.Status("confirmed", "Repeater reached with admin access. ✓")
		} else {
			_ = bridge.Status("warning", fmt.Sprintf("Repeater reached, but MeshTender only has GUEST access (permissions=%d). Guest is open to anyone with a blank password, so MeshTender can't administer this repeater — re-run `setperm <key> 3` to grant admin.", lr.Permissions))
		}
		cancel()
	})

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

	_ = bridge.Status("info", "Sending login to repeater…")
	pkt, err := mesh.BuildLoginPacket(server, repeaterID, "", time.Now())
	if err != nil {
		_ = bridge.Status("error", "build login: "+err.Error())
		return
	}
	// With TX flow control disabled, SendData returns as soon as the frame is
	// handed to the modem; an error here means the write itself failed.
	if err := modem.SendData(pkt); err != nil {
		_ = bridge.Status("error", "transmit: "+err.Error())
		return
	}

	// Wait for the repeater's reply or the overall timeout.
	_ = bridge.Status("info", "Login sent. Waiting for the repeater to reply…")
	<-ctx.Done()
	if !confirmed.Load() {
		_ = bridge.Status("timeout", "No reply from the repeater. Check that the modem is on the repeater's frequency/SF/BW and that MeshTender has been granted access (setperm).")
	}
}
