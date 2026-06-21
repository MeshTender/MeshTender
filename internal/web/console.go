package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/coder/websocket"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/jleight/meshtender/internal/mesh"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/wsbridge"
)

const (
	consoleIdleTimeout = 5 * time.Minute
	// sendInterval is the minimum spacing between our LoRa transmissions, so a
	// user can't flood the shared mesh through their modem.
	sendInterval = time.Second
)

// commandPrefix is the literal leading part of a catalog template (before any
// "<arg>"), e.g. "set tx <0-22>" → "set tx".
func commandPrefix(template string) string {
	if i := strings.Index(template, "<"); i >= 0 {
		template = template[:i]
	}
	return strings.TrimSpace(template)
}

// resolveCommand maps typed CLI text to its catalog command by longest matching
// literal prefix, so "set tx 20" resolves to set.tx (not a bare "set").
func resolveCommand(typed string, catalog []*store.Command) *store.Command {
	typed = strings.TrimSpace(typed)
	var best *store.Command
	bestLen := -1
	for _, c := range catalog {
		prefix := commandPrefix(c.Template)
		if prefix == "" {
			continue
		}
		if typed == prefix || strings.HasPrefix(typed, prefix+" ") {
			if len(prefix) > bestLen {
				best, bestLen = c, len(prefix)
			}
		}
	}
	return best
}

// allowedCommands returns the catalog commands the user may run on the repeater:
// all of them for the owner, else the share-granted subset.
func (s *Server) allowedCommands(ctx context.Context, rep *store.Repeater, userID int64, catalog []*store.Command) []*store.Command {
	if rep.OwnerID == userID {
		return catalog
	}
	ids, err := s.store.ListShareCommandIDs(ctx, rep.ID, userID)
	if err != nil {
		return nil
	}
	allowed := make(map[int64]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	var out []*store.Command
	for _, c := range catalog {
		if allowed[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) pageConsole(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	rep, _, ok := s.requireRepeaterAccess(w, r)
	if !ok {
		return
	}
	catalog, err := s.store.ListCommands(r.Context())
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}
	allowed := s.allowedCommands(r.Context(), rep, uid, catalog)
	sort.Slice(allowed, func(i, j int) bool { return allowed[i].Template < allowed[j].Template })
	s.render(w, r, "console.html", map[string]any{
		"Repeater": rep,
		"Commands": allowed,
	})
}

// wsConsole runs an interactive command session over the modem bridge.
func (s *Server) wsConsole(w http.ResponseWriter, r *http.Request) {
	uid := s.auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
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
	catalog, err := s.store.ListCommands(r.Context())
	if err != nil {
		http.Error(w, "could not load commands", http.StatusInternalServerError)
		return
	}

	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idle := time.AfterFunc(consoleIdleTimeout, cancel)
	defer idle.Stop()

	bridge := wsbridge.New(ctx, ws)
	modem := hardware.NewKissModem(bridge, hardware.WithTxFlowControl(0))
	defer modem.Close()
	server := s.identity.Local()

	// All commands go through one exchanger: rate-limited, monotonic timestamps,
	// automatic retry of lost packets.
	ex := mesh.NewExchanger(modem, server, repeaterID, sendInterval, perTryReply, maxSendTries)
	modem.SetDataHandler(func(data []byte, _ float32, _ int8, _ bool) {
		ex.HandleData(data)
	})

	// Group this connection's commands into a session (required for logging).
	sessionID, err := s.store.StartConsoleSession(ctx, id, uid)
	if err != nil {
		_ = bridge.Status("error", "could not start session")
		return
	}
	defer func() {
		endCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = s.store.EndConsoleSession(endCtx, sessionID)
	}()

	if err := modem.Connect(ctx); err != nil {
		_ = bridge.Status("error", "modem connect: "+err.Error())
		return
	}

	ready := make(chan struct{}, 1)
	cmdCh := make(chan string, 8)

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
					Text string `json:"text"`
				}
				if json.Unmarshal(data, &msg) != nil {
					continue
				}
				switch msg.Type {
				case "ready":
					select {
					case ready <- struct{}{}:
					default:
					}
				case "cmd":
					select {
					case cmdCh <- msg.Text:
					default:
						_ = bridge.Status("error", "busy — wait for the previous command")
					}
				}
			}
		}
	}()

	select {
	case <-ready:
	case <-ctx.Done():
		return
	}

	// Tune the modem to the repeater's channel.
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

	// Establish the session (flood) and learn the route home so commands can use
	// direct routing. If login gets no reply we proceed anyway — the repeater may
	// still have us cached as an admin client from an earlier session.
	_ = bridge.Status("info", "Establishing session…")
	if _, err := ex.Login(ctx, "", func(attempt, max int) {
		if attempt > 1 {
			_ = bridge.Status("info", fmt.Sprintf("No reply yet — retrying (%d/%d)…", attempt, max))
		}
	}); errors.Is(err, mesh.ErrNoReply) {
		_ = bridge.Status("warning", "Couldn't reach the repeater to establish a session — commands will still be attempted (flood), but may not work if it doesn't recognize MeshTender.")
	} else if err != nil {
		return // context cancelled
	}
	_ = bridge.Status("info", "Connected. Ready for commands.")

	runCommand := func(text string) {
		idle.Reset(consoleIdleTimeout)
		cmd := resolveCommand(text, catalog)
		if cmd == nil {
			_ = bridge.Status("denied", "Unknown command: "+text)
			return
		}
		allowed, err := s.store.CanSendCommand(ctx, uid, id, cmd.ID)
		if err != nil {
			_ = bridge.Status("error", "permission check failed")
			return
		}
		if !allowed {
			_ = bridge.Status("denied", "Not permitted: "+text)
			return
		}

		logID, _ := s.store.LogCommand(ctx, id, uid, sessionID, cmd.ID, text)

		reply, err := ex.Command(ctx, text, func(attempt, max int) {
			if attempt == 1 {
				_ = bridge.Status("sent", "→ "+text)
			} else {
				_ = bridge.Status("info", fmt.Sprintf("no reply — retrying (%d/%d)…", attempt, max))
			}
		})
		switch {
		case err == nil:
			if logID != 0 {
				_ = s.store.MarkCommandReply(ctx, logID, reply)
			}
			_ = bridge.Status("reply", reply)
		case errors.Is(err, mesh.ErrNoReply):
			_ = bridge.Status("noreply", "No reply received after several tries — the command may still have run.")
		default:
			// context cancelled or a build/transmit error
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case text := <-cmdCh:
			runCommand(text)
		}
	}
}
