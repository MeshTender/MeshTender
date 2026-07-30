package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
)

// TestConsoleRoundTrip drives the command console end to end: the test plays the
// browser (WebSocket), the KISS modem (framing), and the repeater (decrypts the
// command, replies). Gated on MESHTENDER_TEST_DATABASE_URL (db name ends _test).
func TestConsoleRoundTrip(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)

	masterKey := testMasterKey
	idSvc, err := identity.LoadOrCreate(ctx, st, masterKey)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	authSvc, err := auth.New(st, st.Pool(), testAuthConfig())
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := NewServer(st, authSvc, idSvc, testConfig())
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "alice")

	repeater, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatalf("repeater identity: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Test", PublicKeyHex: repeater.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/repeaters/" + rep.PublicID + "/console/ws"
	hdr := http.Header{}
	if cs := jar.Cookies(mustURL(t, ts.URL)); len(cs) > 0 {
		var parts []string
		for _, c := range cs {
			parts = append(parts, c.Name+"="+c.Value)
		}
		hdr.Set("Cookie", strings.Join(parts, "; "))
	}
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	ws, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	rw, rwcancel := context.WithTimeout(ctx, 15*time.Second)
	defer rwcancel()

	must := func(err error, msg string) {
		if err != nil {
			t.Fatalf("%s: %v", msg, err)
		}
	}
	must(ws.Write(rw, websocket.MessageText, []byte(`{"type":"ready"}`)), "ready")
	must(ws.Write(rw, websocket.MessageText, []byte(`{"type":"cmd","text":"ver"}`)), "cmd")

	serverID := idSvc.Local().Identity
	shared, err := repeater.SharedSecret(serverID)
	must(err, "shared")
	const replyText = "> v1.2.3 (test build)"

	// The console logs in first (flood, to learn the route), then sends the
	// command (direct). Reply to both as the repeater, then collect the reply
	// status pushed back to the browser.
	var buf []byte
	loggedIn := false
	cmdReplied := false
	got := ""
	for got == "" {
		typ, data, err := ws.Read(rw)
		must(err, "ws read")
		if typ == websocket.MessageText {
			var m struct{ State, Message string }
			if json.Unmarshal(data, &m) == nil {
				if m.State == "error" || m.State == "denied" || m.State == "noreply" {
					t.Fatalf("unexpected status %q: %s", m.State, m.Message)
				}
				if m.State == "reply" {
					got = m.Message
				}
			}
			continue
		}
		buf = append(buf, data...)
		frames, rest, _ := hardware.ExtractFrames(buf)
		buf = rest
		for _, f := range frames {
			if f.Command != hardware.KISS_CMD_DATA {
				continue // skip SetRadio hardware frames
			}
			pkt, err := meshcore.PacketFromBytes(f.Data)
			if err != nil {
				continue
			}
			switch pkt.PayloadType() {
			case meshcore.PayloadTypeAnonReq:
				if loggedIn {
					continue
				}
				loggedIn = true
				resp := make([]byte, 13)
				binary.LittleEndian.PutUint32(resp[:4], 1_700_002_000)
				resp[6] = 1 // admin
				resp[7] = 3
				body := append([]byte{0x00, meshcore.PayloadTypeResponse}, resp...)
				enc, _ := meshcore.EncryptThenMAC(shared, body)
				p := &meshcore.Path{Destination: serverID.Hash()[0], Source: repeater.Hash()[0], MAC: [2]byte{enc[0], enc[1]}, EncryptedPayload: enc[2:]}
				payload, _ := p.ToBytes()
				lp := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypePath, 0), Payload: payload}
				raw, _ := lp.ToBytes()
				must(ws.Write(rw, websocket.MessageBinary, hardware.EncodeDataFrame(raw)), "login reply")
			case meshcore.PayloadTypeTxtMsg:
				if cmdReplied {
					continue
				}
				tm, err := meshcore.TextMessageFromBytes(pkt.Payload)
				must(err, "parse text message")
				plain := tm.Decrypt(shared)
				if plain == nil || string(plain[5:8]) != "ver" {
					t.Fatalf("decoded command = %q, want ver", string(plain[5:]))
				}
				cmdReplied = true
				replyPlain := meshcore.BuildTextPlaintext(time.Unix(1_700_002_000, 0), 1<<2, []byte(replyText))
				rtm, err := meshcore.NewTextMessage(repeater, serverID, replyPlain, shared)
				must(err, "reply text message")
				payload, _ := rtm.ToBytes()
				rpkt := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}
				raw, _ := rpkt.ToBytes()
				must(ws.Write(rw, websocket.MessageBinary, hardware.EncodeDataFrame(raw)), "send reply")
			}
		}
	}

	if got != replyText {
		t.Fatalf("reply = %q, want %q", got, replyText)
	}

	// The command should be logged with ack + response.
	entries, err := st.ListCommandLog(ctx, rep.ID, 10)
	must(err, "list log")
	if len(entries) != 1 {
		t.Fatalf("log entries = %d, want 1", len(entries))
	}
	e := entries[0]
	if e.CommandText != "ver" || !e.AckReceived || e.ResponseText == nil || *e.ResponseText != replyText {
		t.Fatalf("log entry = %+v (response=%v)", e, e.ResponseText)
	}

	// Connecting from the console with a successful admin login confirms the
	// repeater — the same as running the dedicated confirm flow.
	confirmed, err := st.GetRepeaterForUser(ctx, user.ID, rep.ID)
	must(err, "reload repeater")
	if !confirmed.Confirmed {
		t.Fatal("console connect (admin login) did not confirm the repeater")
	}
}

// TestConsoleGetLatUpdatesLocation: running "get lat" directly in the console
// captures the reported coordinate into the stored location, updating only the
// latitude (leaving longitude untouched). The test plays browser, modem, and
// repeater as in TestConsoleRoundTrip.
func TestConsoleGetLatUpdatesLocation(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)

	masterKey := testMasterKey
	idSvc, err := identity.LoadOrCreate(ctx, st, masterKey)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	authSvc, err := auth.New(st, st.Pool(), testAuthConfig())
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := NewServer(st, authSvc, idSvc, testConfig())
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "geographer")
	repeater, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatalf("repeater identity: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Test", PublicKeyHex: repeater.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/repeaters/" + rep.PublicID + "/console/ws"
	hdr := http.Header{}
	if cs := jar.Cookies(mustURL(t, ts.URL)); len(cs) > 0 {
		var parts []string
		for _, c := range cs {
			parts = append(parts, c.Name+"="+c.Value)
		}
		hdr.Set("Cookie", strings.Join(parts, "; "))
	}
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	ws, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	rw, rwcancel := context.WithTimeout(ctx, 15*time.Second)
	defer rwcancel()
	must := func(err error, msg string) {
		if err != nil {
			t.Fatalf("%s: %v", msg, err)
		}
	}
	must(ws.Write(rw, websocket.MessageText, []byte(`{"type":"ready"}`)), "ready")
	must(ws.Write(rw, websocket.MessageText, []byte(`{"type":"cmd","text":"get lat"}`)), "cmd")

	serverID := idSvc.Local().Identity
	shared, err := repeater.SharedSecret(serverID)
	must(err, "shared")
	const replyText = "> 37.7749"

	// Reply to login (flood) then to the "get lat" command. The server stamps the
	// latitude after pushing the "reply" and emits a "location" status; waiting for
	// that status guarantees the DB write has completed before we assert.
	var buf []byte
	loggedIn := false
	cmdReplied := false
	located := false
	for !located {
		typ, data, err := ws.Read(rw)
		must(err, "ws read")
		if typ == websocket.MessageText {
			var m struct{ State, Message string }
			if json.Unmarshal(data, &m) == nil {
				if m.State == "error" || m.State == "denied" || m.State == "noreply" {
					t.Fatalf("unexpected status %q: %s", m.State, m.Message)
				}
				if m.State == "location" {
					located = true
				}
			}
			continue
		}
		buf = append(buf, data...)
		frames, rest, _ := hardware.ExtractFrames(buf)
		buf = rest
		for _, f := range frames {
			if f.Command != hardware.KISS_CMD_DATA {
				continue
			}
			pkt, err := meshcore.PacketFromBytes(f.Data)
			if err != nil {
				continue
			}
			switch pkt.PayloadType() {
			case meshcore.PayloadTypeAnonReq:
				if loggedIn {
					continue
				}
				loggedIn = true
				resp := make([]byte, 13)
				binary.LittleEndian.PutUint32(resp[:4], 1_700_002_000)
				resp[6] = 1 // admin
				resp[7] = 3
				body := append([]byte{0x00, meshcore.PayloadTypeResponse}, resp...)
				enc, _ := meshcore.EncryptThenMAC(shared, body)
				p := &meshcore.Path{Destination: serverID.Hash()[0], Source: repeater.Hash()[0], MAC: [2]byte{enc[0], enc[1]}, EncryptedPayload: enc[2:]}
				payload, _ := p.ToBytes()
				lp := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypePath, 0), Payload: payload}
				raw, _ := lp.ToBytes()
				must(ws.Write(rw, websocket.MessageBinary, hardware.EncodeDataFrame(raw)), "login reply")
			case meshcore.PayloadTypeTxtMsg:
				if cmdReplied {
					continue
				}
				tm, err := meshcore.TextMessageFromBytes(pkt.Payload)
				must(err, "parse text message")
				plain := tm.Decrypt(shared)
				if plain == nil || !strings.HasPrefix(string(plain[5:]), "get lat") {
					t.Fatalf("decoded command = %q, want get lat", string(plain[5:]))
				}
				cmdReplied = true
				replyPlain := meshcore.BuildTextPlaintext(time.Unix(1_700_002_000, 0), 1<<2, []byte(replyText))
				rtm, err := meshcore.NewTextMessage(repeater, serverID, replyPlain, shared)
				must(err, "reply text message")
				payload, _ := rtm.ToBytes()
				rpkt := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}
				raw, _ := rpkt.ToBytes()
				must(ws.Write(rw, websocket.MessageBinary, hardware.EncodeDataFrame(raw)), "send reply")
			}
		}
	}

	// Only the latitude should be stored; longitude stays NULL (we never read it).
	got, err := st.GetRepeaterForUser(ctx, user.ID, rep.ID)
	must(err, "reload repeater")
	if got.Latitude == nil || *got.Latitude != 37.7749 {
		t.Fatalf("stored latitude = %v, want 37.7749", got.Latitude)
	}
	if got.Longitude != nil {
		t.Fatalf("stored longitude = %v, want nil (get lat must not touch longitude)", got.Longitude)
	}
}

// TestConsoleGuestLoginConfirms: connecting the console to a repeater that only
// grants GUEST access still records the confirmation (with is_admin=false) and
// warns the user — the same as the dedicated confirm flow did. It must NOT emit a
// "confirmed" (admin) status.
func TestConsoleGuestLoginConfirms(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)

	masterKey := testMasterKey
	idSvc, err := identity.LoadOrCreate(ctx, st, masterKey)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	authSvc, err := auth.New(st, st.Pool(), testAuthConfig())
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := NewServer(st, authSvc, idSvc, testConfig())
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "guestuser")
	repeater, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatalf("repeater identity: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Test", PublicKeyHex: repeater.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/repeaters/" + rep.PublicID + "/console/ws"
	hdr := http.Header{}
	if cs := jar.Cookies(mustURL(t, ts.URL)); len(cs) > 0 {
		var parts []string
		for _, c := range cs {
			parts = append(parts, c.Name+"="+c.Value)
		}
		hdr.Set("Cookie", strings.Join(parts, "; "))
	}
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	ws, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	rw, rwcancel := context.WithTimeout(ctx, 15*time.Second)
	defer rwcancel()
	must := func(err error, msg string) {
		if err != nil {
			t.Fatalf("%s: %v", msg, err)
		}
	}
	must(ws.Write(rw, websocket.MessageText, []byte(`{"type":"ready"}`)), "ready")

	serverID := idSvc.Local().Identity
	shared, err := repeater.SharedSecret(serverID)
	must(err, "shared")

	// Reply to login as a guest (is_admin=0), then wait for the warning the console
	// emits for guest access. It must never report a "confirmed" (admin) status.
	var buf []byte
	loggedIn := false
	warned := false
	for !warned {
		typ, data, err := ws.Read(rw)
		must(err, "ws read")
		if typ == websocket.MessageText {
			var m struct{ State, Message string }
			if json.Unmarshal(data, &m) == nil {
				if m.State == "confirmed" {
					t.Fatalf("guest login must not report admin-confirmed, got %q", m.Message)
				}
				if m.State == "error" {
					t.Fatalf("unexpected error status: %s", m.Message)
				}
				if m.State == "warning" && strings.Contains(m.Message, "GUEST") {
					warned = true
				}
			}
			continue
		}
		buf = append(buf, data...)
		frames, rest, _ := hardware.ExtractFrames(buf)
		buf = rest
		for _, f := range frames {
			if f.Command != hardware.KISS_CMD_DATA {
				continue
			}
			pkt, err := meshcore.PacketFromBytes(f.Data)
			if err != nil {
				continue
			}
			if pkt.PayloadType() == meshcore.PayloadTypeAnonReq && !loggedIn {
				loggedIn = true
				resp := make([]byte, 13)
				binary.LittleEndian.PutUint32(resp[:4], 1_700_002_000)
				resp[6] = 0 // guest (not admin)
				resp[7] = 1
				body := append([]byte{0x00, meshcore.PayloadTypeResponse}, resp...)
				enc, _ := meshcore.EncryptThenMAC(shared, body)
				p := &meshcore.Path{Destination: serverID.Hash()[0], Source: repeater.Hash()[0], MAC: [2]byte{enc[0], enc[1]}, EncryptedPayload: enc[2:]}
				payload, _ := p.ToBytes()
				lp := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypePath, 0), Payload: payload}
				raw, _ := lp.ToBytes()
				must(ws.Write(rw, websocket.MessageBinary, hardware.EncodeDataFrame(raw)), "login reply")
			}
		}
	}

	// The guest connection still records the confirmation, with guest access.
	got, err := st.GetRepeaterForUser(ctx, user.ID, rep.ID)
	must(err, "reload repeater")
	if !got.Confirmed {
		t.Fatal("guest login did not confirm the repeater")
	}
	if !got.AccessKnown() || got.IsAdmin() {
		t.Fatalf("access level = admin?%v known?%v, want guest (known, not admin)", got.IsAdmin(), got.AccessKnown())
	}
}

// TestConsoleAuditFailureRefusesCommand: if a command can't be recorded to the
// audit log, the console must refuse to send it to the device rather than execute
// an unlogged command. We drop command_log so LogCommand fails, then send a
// command the owner is permitted to run; the console must report an error and
// never transmit the command packet.
func TestConsoleAuditFailureRefusesCommand(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)

	masterKey := testMasterKey
	idSvc, err := identity.LoadOrCreate(ctx, st, masterKey)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	authSvc, err := auth.New(st, st.Pool(), testAuthConfig())
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := NewServer(st, authSvc, idSvc, testConfig())
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "auditor")
	repeater, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatalf("repeater identity: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Test", PublicKeyHex: repeater.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	// Make LogCommand fail. Nothing before the command loop writes to command_log.
	if _, err := st.Pool().Exec(ctx, `DROP TABLE command_log`); err != nil {
		t.Fatalf("drop command_log: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/repeaters/" + rep.PublicID + "/console/ws"
	hdr := http.Header{}
	if cs := jar.Cookies(mustURL(t, ts.URL)); len(cs) > 0 {
		var parts []string
		for _, c := range cs {
			parts = append(parts, c.Name+"="+c.Value)
		}
		hdr.Set("Cookie", strings.Join(parts, "; "))
	}
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	defer dcancel()
	ws, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	rw, rwcancel := context.WithTimeout(ctx, 15*time.Second)
	defer rwcancel()
	must := func(err error, msg string) {
		if err != nil {
			t.Fatalf("%s: %v", msg, err)
		}
	}
	must(ws.Write(rw, websocket.MessageText, []byte(`{"type":"ready"}`)), "ready")
	must(ws.Write(rw, websocket.MessageText, []byte(`{"type":"cmd","text":"ver"}`)), "cmd")

	// We never answer the login as the repeater, so after ErrNoReply the console
	// proceeds and tries to run the queued command. LogCommand fails, so it must
	// report an error and must NOT transmit the command (no TxtMsg packet). Login's
	// own AnonReq packets are expected and ignored.
	var buf []byte
	for {
		typ, data, err := ws.Read(rw)
		must(err, "ws read")
		if typ == websocket.MessageText {
			var m struct{ State, Message string }
			if json.Unmarshal(data, &m) == nil && m.State == "error" && strings.Contains(m.Message, "Could not record") {
				return // refused as required
			}
			continue
		}
		buf = append(buf, data...)
		frames, rest, _ := hardware.ExtractFrames(buf)
		buf = rest
		for _, f := range frames {
			if f.Command != hardware.KISS_CMD_DATA {
				continue
			}
			pkt, err := meshcore.PacketFromBytes(f.Data)
			if err != nil {
				continue
			}
			if pkt.PayloadType() == meshcore.PayloadTypeTxtMsg {
				t.Fatal("command was transmitted to the device despite the audit-log failure")
			}
		}
	}
}
