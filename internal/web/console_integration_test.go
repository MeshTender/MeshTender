package web

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	meshcore "github.com/meshcore-go/meshcore-go"
	"github.com/meshcore-go/meshcore-go/hardware"

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
)

// TestConsoleRoundTrip drives the command console end to end: the test plays the
// browser (WebSocket), the KISS modem (framing), and the repeater (decrypts the
// command, replies). Gated on MESHTENDER_TEST_DATABASE_URL (db name ends _test).
func TestConsoleRoundTrip(t *testing.T) {
	dsn := os.Getenv("MESHTENDER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set MESHTENDER_TEST_DATABASE_URL to run this integration test")
	}
	if u, err := url.Parse(dsn); err != nil || !strings.HasSuffix(strings.TrimPrefix(u.Path, "/"), "_test") {
		t.Fatalf("refusing to run: test DB name must end in _test (got %q)", dsn)
	}
	ctx := context.Background()

	st, err := store.New(ctx, dsn)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := st.Pool().Exec(ctx,
		`TRUNCATE users, repeaters, repeater_shares, repeater_invites, share_commands, command_log, webauthn_credentials, server_identity, sessions RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	var masterKey [32]byte
	_, _ = rand.Read(masterKey[:])
	idSvc, err := identity.LoadOrCreate(ctx, st, masterKey)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	authSvc, err := auth.New(st, st.Pool(), auth.Config{RPID: "localhost", RPDisplayName: "t", RPOrigins: []string{"http://localhost"}})
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := NewServer(st, authSvc, idSvc, &config.Config{})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	resp, err := client.PostForm(ts.URL+"/signup/password", url.Values{"username": {"alice"}, "password": {"supersecret"}})
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	resp.Body.Close()
	user, err := st.GetUserByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}

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

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/repeaters/" + strconv.FormatInt(rep.ID, 10) + "/console/ws"
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
	const replyText = "> v1.2.3 (test build)"

	// Read until we receive the command packet, reply as the repeater, then
	// collect the "reply" status pushed back to the browser.
	var buf []byte
	repliedSent := false
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
		// Binary: KISS frames from the modem. Find the command data frame, reply once.
		if repliedSent {
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
			if err != nil || pkt.PayloadType() != meshcore.PayloadTypeTxtMsg {
				continue
			}
			// Decode the command to confirm it round-trips.
			tm, err := meshcore.TextMessageFromBytes(pkt.Payload)
			must(err, "parse text message")
			shared, err := repeater.SharedSecret(serverID)
			must(err, "shared")
			plain := tm.Decrypt(shared)
			if plain == nil || string(plain[5:8]) != "ver" {
				t.Fatalf("decoded command = %q, want ver", string(plain[5:]))
			}
			// Reply as the repeater: a TXT_MSG datagram with [ts][flags][text].
			replyPlain := meshcore.BuildTextPlaintext(time.Unix(1_700_002_000, 0), 1<<2, []byte(replyText))
			rtm, err := meshcore.NewTextMessage(repeater, serverID, replyPlain, shared)
			must(err, "reply text message")
			payload, err := rtm.ToBytes()
			must(err, "reply bytes")
			rpkt := &meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}
			raw, err := rpkt.ToBytes()
			must(err, "reply packet")
			must(ws.Write(rw, websocket.MessageBinary, hardware.EncodeDataFrame(raw)), "send reply")
			repliedSent = true
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
}
