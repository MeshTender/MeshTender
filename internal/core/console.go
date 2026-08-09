package core

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

	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/web"
	"github.com/MeshTender/MeshTender/internal/wsbridge"
)

const (
	consoleIdleTimeout = 5 * time.Minute
	// consoleEndTimeout bounds the deferred EndConsoleSession stamp that runs when
	// a console handler returns (including on shutdown drain). It uses a fresh
	// background context — the session context is already canceled by then — so
	// the stamp still lands. WSDrainTimeout MUST exceed this (plus unwind slack) or
	// the drain gives up before the stamp completes and the session is orphaned as
	// "in progress" forever (see TestDrainWebSockets).
	consoleEndTimeout = 5 * time.Second
	// maxCommandLen bounds a single CLI command; MeshCore commands are short and
	// a LoRa frame is tiny, so anything longer is malformed/abusive.
	maxCommandLen = 200
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

// arityVariadic marks a command that accepts any number of arguments (e.g. a
// rest-of-line free-text arg like "set name <text>", or "region def …").
const arityVariadic = -1

// resolveCommand maps typed CLI text to the exact catalog command the firmware
// would run, matched by command TOKEN and argument count (arity). It returns nil
// when no command matches — callers MUST treat nil as "deny", never as "send
// anyway".
//
// Security model: authorization is by the exact (token, arity) tuple, never a
// loose prefix. A command's token is the literal words before its first "<arg>"
// (e.g. "set tx", "region put", "setperm"); arity is the count of remaining
// whitespace tokens, or -1 for variadic. The firmware (a) runs exactly one
// command per message — no ';'/newline chaining — and (b) tokenizes on the same
// whitespace and overloads commands by arg count (e.g. setperm/2 sets a
// permission, setperm/1 removes it). Matching the same way means an authorized
// command can never be re-interpreted by the device as a different, ungranted
// one. The longest matching token wins ("region put" over "region"), so a
// shorter command can't shadow a more specific one. Args themselves are not
// constrained — granting a command grants all of its argument values.
func resolveCommand(typed string, catalog []*store.Command) *store.Command {
	fields := strings.Fields(typed)
	if len(fields) == 0 {
		return nil
	}
	var best *store.Command
	bestTokenLen := -1
	for _, c := range catalog {
		token := strings.Fields(commandPrefix(c.Template))
		if len(token) == 0 || len(token) > len(fields) {
			continue
		}
		if !equalWords(fields[:len(token)], token) {
			continue
		}
		argc := len(fields) - len(token)
		if c.Arity != arityVariadic && argc != c.Arity {
			continue
		}
		if len(token) > bestTokenLen {
			best, bestTokenLen = c, len(token)
		}
	}
	return best
}

func equalWords(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// validCommandText reports whether s is a single line of printable text — the
// only shape a CLI command can legitimately take. Rejecting control characters
// (newlines especially) is defense in depth: the firmware runs one command per
// message today, but we never want to forward bytes that could split or be
// re-interpreted, and it keeps the command log/echo clean.
func validCommandText(s string) bool {
	if strings.TrimSpace(s) == "" || len(s) > maxCommandLen {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// allowedCommands returns the catalog commands the user may run on the repeater.
// It filters the catalog by store.ListSendableCommandIDs — the same authorization
// query the runtime gate (store.CanSendCommand) uses — so the sidebar list can
// never disagree with what the user is actually permitted to send. This covers
// owners, stewards, share grants, AND org participation (an org admin/member with
// access via a shared org previously got an empty sidebar despite being able to
// run commands).
func (s *Handlers) allowedCommands(ctx context.Context, rep *store.Repeater, userID int64, catalog []*store.Command) []*store.Command {
	ids, err := s.Store.ListSendableCommandIDs(ctx, userID, rep.ID)
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

// locationCommandKeys are the catalog commands the console's "Fetch location"
// action transmits. The button is a shortcut for running these two, not a way
// around the permission model: it is authorized and audited exactly as though the
// user had typed them, so a person who may not run "get lat" cannot cause one to
// be transmitted by clicking instead.
var locationCommandKeys = [2]string{"get.lat", "get.lon"}

// locationCommands resolves the pair from the catalog. ok is false if either is
// missing — the catalog is data, so a row can be retired, and a fetch that can't
// name the commands it sends can't audit them either.
func locationCommands(catalog []*store.Command) (lat, lon *store.Command, ok bool) {
	for _, c := range catalog {
		switch c.Key {
		case locationCommandKeys[0]:
			lat = c
		case locationCommandKeys[1]:
			lon = c
		}
	}
	return lat, lon, lat != nil && lon != nil
}

// canFetchLocation reports whether every location command is in the user's
// allowed set, which is what decides whether the console offers the button. The
// server checks again before sending (see fetchLocation) — this only keeps the UI
// from advertising an action that would be refused.
func canFetchLocation(allowed []*store.Command) bool {
	have := 0
	for _, c := range allowed {
		for _, key := range locationCommandKeys {
			if c.Key == key {
				have++
			}
		}
	}
	return have == len(locationCommandKeys)
}

func (s *Handlers) pageConsole(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	rep, _, ok := s.requireRepeaterAccess(w, r)
	if !ok {
		return
	}
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		s.ServerError(w, r, "could not load commands", err)
		return
	}
	allowed := s.allowedCommands(r.Context(), rep, uid, catalog)
	sort.Slice(allowed, func(i, j int) bool { return allowed[i].Template < allowed[j].Template })
	// Only offer the "Apply organization configuration" action when this repeater
	// actually participates in an org that has a saved configuration.
	configOrgs, err := s.Store.ListRepeaterConfigOrgs(r.Context(), rep.ID)
	if err != nil {
		s.ServerError(w, r, "could not load organizations", err)
		return
	}
	s.Render(w, r, "console.html", map[string]any{
		"Repeater":         rep,
		"Commands":         allowed,
		"CanFetchLocation": canFetchLocation(allowed),
		"ShowConfig":       len(configOrgs) > 0,
		"Debug":            r.URL.Query().Get("debug") == "1",
	})
}

// wsConsole runs an interactive command session over the modem bridge.
func (s *Handlers) wsConsole(w http.ResponseWriter, r *http.Request) {
	uid := s.Auth.CurrentUserID(r.Context())
	id, ok := s.repeaterID(r)
	if !ok {
		s.NotFound(w, r)
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
	catalog, err := s.Store.ListCommands(r.Context())
	if err != nil {
		s.ServerError(w, r, "could not load commands", err)
		return
	}

	// Track the socket so shutdown can drain it (http.Server.Shutdown doesn't close
	// hijacked/WebSocket conns). Add before Accept so a shutdown racing the upgrade
	// still waits for this handler.
	s.wsWG.Add(1)
	defer s.wsWG.Done()

	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	// Derive from the server's WS context so shutdown cancels the session.
	ctx, cancel := context.WithCancel(s.wsCtx)
	defer cancel()
	idle := time.AfterFunc(consoleIdleTimeout, cancel)
	defer idle.Stop()

	bridge := wsbridge.New(ctx, ws)
	modem := hardware.NewKissModem(bridge, hardware.WithTxFlowControl(0))
	defer func() { _ = modem.Close() }()
	server := s.Identity.Local()

	// All commands go through one exchanger: rate-limited, monotonic timestamps,
	// automatic retry of lost packets.
	ex := mesh.NewExchanger(modem, server, repeaterID, sendInterval, perTryReply, maxSendTries)
	modem.SetDataHandler(func(data []byte, _ float32, _ int8, _ bool) {
		ex.HandleData(data)
	})
	userPathSet := applyUserPath(ex, r, bridge)
	debug := r.URL.Query().Get("debug") == "1"
	if debug {
		// Dump every inbound KISS frame as hex so we can see exactly what the modem
		// reports back (e.g. whether the repeater replies at all).
		bridge.SetObserver(func(f *hardware.KissFrame) {
			_ = bridge.Status("debug", fmt.Sprintf("rx frame cmd=0x%02x len=%d data=%x", f.Command, len(f.Data), f.Data))
		})
	}

	// Group this connection's commands into a session (required for logging).
	sessionID, err := s.Store.StartConsoleSession(ctx, id, uid)
	if err != nil {
		web.LogError(r, "console: start session", err, "repeater_id", id)
		_ = bridge.Status("error", "could not start session")
		return
	}
	defer func() {
		endCtx, c := context.WithTimeout(context.Background(), consoleEndTimeout)
		defer c()
		_ = s.Store.EndConsoleSession(endCtx, sessionID)
	}()

	if err := modem.Connect(ctx); err != nil {
		// The user's own local modem — keep the detail for troubleshooting, and log
		// it so operators see connection failures.
		web.LogError(r, "console: modem connect", err, "repeater_id", id)
		_ = bridge.Status("error", "modem connect: "+err.Error())
		return
	}

	ready := make(chan struct{}, 1)
	cmdCh := make(chan string, 8)
	locCh := make(chan struct{}, 1) // "getloc" requests: fetch the device's coordinates

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
				case "getloc":
					select {
					case locCh <- struct{}{}:
					default:
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
		FreqHz: uint32(rep.RadioFreqHz), //nolint:gosec // G115: radio config value is bounded (preset-constrained)
		BwHz:   uint32(rep.RadioBwHz),   //nolint:gosec // G115: radio config value is bounded (preset-constrained)
		SF:     uint8(rep.RadioSF),      //nolint:gosec // G115: radio config value is bounded (preset-constrained)
		CR:     uint8(rep.RadioCR),      //nolint:gosec // G115: radio config value is bounded (preset-constrained)
	}); err != nil {
		web.LogError(r, "console: set radio", err, "repeater_id", id)
		_ = bridge.Status("error", "set radio: "+err.Error())
		return
	}

	// Establish the session (flood) and learn the route home so commands can use
	// direct routing. If login gets no reply we proceed anyway — the repeater may
	// still have us cached as an admin client from an earlier session.
	_ = bridge.Status("info", "Establishing session…")
	lr, err := ex.Login(ctx, "", func(attempt, max int) {
		if attempt > 1 {
			_ = bridge.Status("info", fmt.Sprintf("No reply yet — retrying (%d/%d)…", attempt, max))
		}
	})
	switch {
	case errors.Is(err, mesh.ErrNoReply):
		_ = bridge.Status("warning", "Couldn't reach the repeater to establish a session — commands will still be attempted (flood), but may not work if it doesn't recognize MeshTender.")
	case err != nil:
		return // context canceled
	default:
		if userPathSet {
			reportPathOutcome(bridge, lr)
		}
		// A successful login proves we reached the repeater, so connecting the console
		// IS how a repeater becomes confirmed — for guest access too, which records the
		// access level. This is cheap (no extra
		// packets). Fetching the location is deferred to an explicit "getloc" request
		// so a plain console session doesn't pay for a location round-trip it doesn't
		// need; the page offers a "Fetch location" button that sends it.
		if err := s.Store.SetRepeaterConfirmed(ctx, id, uid, lr.IsAdmin, int16(lr.Permissions)); err != nil {
			web.LogError(r, "console: save confirmation", err, "repeater_id", id)
		} else if lr.IsAdmin {
			_ = bridge.Status("confirmed", "Repeater confirmed with admin access. ✓")
		} else {
			// Guests can't run CLI commands, so say so rather than leaving the user to
			// discover it one refused command at a time.
			_ = bridge.Status("warning", fmt.Sprintf("Repeater reached, but MeshTender only has GUEST access (permissions=%d). Guest is open to anyone with a blank password, so MeshTender can't administer this repeater — re-run `%s` to grant admin.", lr.Permissions, s.Identity.SetPermCommand()))
		}
	}
	_ = bridge.Status("info", "Connected. Ready for commands.")

	// permitted reports whether this user may send cmd on this repeater, using the
	// one authorization query the sidebar is also built from. An error is treated as
	// a refusal: a permission check that didn't run hasn't granted anything.
	permitted := func(cmd *store.Command, text string) bool {
		ok, err := s.Store.CanSendCommand(ctx, uid, id, cmd.ID)
		if err != nil {
			web.LogError(r, "console: permission check", err, "repeater_id", id, "command_id", cmd.ID)
			_ = bridge.Status("error", "permission check failed")
			return false
		}
		if !ok {
			_ = bridge.Status("denied", "Not permitted: "+text)
		}
		return ok
	}

	// logBeforeSend records a command in the audit log BEFORE it is transmitted, and
	// reports whether sending may proceed. If the log write fails the command must
	// not go out: an unlogged command on a shared repeater is worse than a refused
	// one, because the owner can't see it happened. Every path that transmits to the
	// repeater goes through here — typed commands and the "Fetch location" button
	// alike — so there is one place that invariant lives.
	logBeforeSend := func(text string, cmd *store.Command) (int64, bool) {
		logID, err := s.Store.LogCommand(ctx, id, uid, sessionID, cmd.ID, text)
		if err != nil {
			web.LogError(r, "console: log command", err, "repeater_id", id, "command_id", cmd.ID)
			_ = bridge.Status("error", "Could not record the command — not sending it. Please try again.")
			return 0, false
		}
		return logID, true
	}

	markReply := func(logID int64, reply string) {
		if logID == 0 {
			return
		}
		if err := s.Store.MarkCommandReply(ctx, logID, reply); err != nil {
			web.LogError(r, "console: mark command reply", err, "log_id", logID)
		}
	}

	runCommand := func(text string) {
		idle.Reset(consoleIdleTimeout)
		if !validCommandText(text) {
			_ = bridge.Status("denied", "Invalid command.")
			return
		}
		cmd := resolveCommand(text, catalog)
		if cmd == nil {
			_ = bridge.Status("denied", "Unknown command: "+text)
			return
		}
		if !permitted(cmd, text) {
			return
		}

		logID, ok := logBeforeSend(text, cmd)
		if !ok {
			return
		}

		reply, err := ex.Command(ctx, text, func(attempt, max int) {
			if attempt == 1 {
				_ = bridge.Status("sent", "→ "+text)
			} else {
				_ = bridge.Status("info", fmt.Sprintf("no reply — retrying (%d/%d)…", attempt, max))
			}
		})
		switch {
		case err == nil:
			markReply(logID, reply)
			_ = bridge.Status("reply", reply)
			// A "get lat"/"get lon" the user ran themselves carries the repeater's
			// current coordinate — capture it so the stored location tracks what the
			// device reports, without a separate fetch. Keyed on the resolved catalog
			// token (not raw text) so only these two commands trigger it.
			s.storeCoordFromReply(ctx, r, cmd, reply, id, bridge)
		case errors.Is(err, mesh.ErrNoReply):
			_ = bridge.Status("noreply", "No reply received after several tries — the command may still have run.")
		default:
			// context canceled or a build/transmit error
		}
	}

	// fetchLocation serves the "Fetch location" button. It reads the repeater's
	// coordinates by transmitting real "get lat"/"get lon" commands, so it is
	// authorized and audited exactly as typing them would be — the button saves the
	// user two round-trips of typing, it does not grant anything extra. Both
	// commands are required: a caller permitted to read only one coordinate would
	// otherwise half-fetch a location.
	fetchLocation := func() {
		latCmd, lonCmd, ok := locationCommands(catalog)
		if !ok {
			_ = bridge.Status("error", "The command catalog has no location commands, so the location can't be read.")
			return
		}
		for _, c := range []*store.Command{latCmd, lonCmd} {
			if !permitted(c, c.Template) {
				return
			}
		}
		audit := locationAudit{
			before: func(text string) (int64, bool) {
				cmd := latCmd
				if text == lonCmd.Template {
					cmd = lonCmd
				}
				return logBeforeSend(text, cmd)
			},
			after: markReply,
		}
		if _, _, ok := s.fetchAndStoreLocation(ctx, r, ex, bridge, id, false, audit); ok {
			_ = bridge.Status("location", "Location updated from the repeater.")
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case text := <-cmdCh:
			runCommand(text)
		case <-locCh:
			// Handled in the same loop as commands so it never drives the exchanger
			// concurrently with a command. Emits a "location" status on success so an
			// open config panel refreshes its region commands.
			idle.Reset(consoleIdleTimeout)
			fetchLocation()
		}
	}
}

// storeCoordFromReply persists the coordinate reported by a "get lat"/"get lon"
// command the user ran directly in the console, so the stored location tracks
// what the device reports without a separate fetch. It matches on the resolved
// catalog token (not the raw typed text) so only those two commands trigger it,
// and no-ops for any other command or an unparseable reply. A single "get lat"
// updates only the latitude — SetRepeaterLatitude/Longitude write one column so
// reading one coordinate never clobbers the other. On success it emits a
// "location" status so an open config panel refreshes its region commands.
func (s *Handlers) storeCoordFromReply(ctx context.Context, r *http.Request, cmd *store.Command, reply string, id int64, bridge *wsbridge.Conn) {
	value, ok := parseLocationFloat(reply)
	if !ok {
		return
	}
	var update func(context.Context, int64, float64) error
	switch commandPrefix(cmd.Template) {
	case "get lat":
		update = s.Store.SetRepeaterLatitude
	case "get lon":
		update = s.Store.SetRepeaterLongitude
	default:
		return
	}
	if err := update(ctx, id, value); err != nil {
		web.LogError(r, "console: store coordinate", err, "repeater_id", id, "command_id", cmd.ID)
		return
	}
	_ = bridge.Status("location", "Location updated from the repeater.")
}
