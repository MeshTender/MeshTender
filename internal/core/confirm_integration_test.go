package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
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

// TestConfirmRoundTrip drives the full confirm path in-process, standing in for
// the browser (WebSocket), the KISS modem (KISS framing), and the repeater
// (MeshCore crypto). It is gated on MESHTENDER_TEST_DATABASE_URL so a plain
// `go test` never truncates a real database.
func TestConfirmRoundTrip(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)

	var masterKey [32]byte
	_, _ = rand.Read(masterKey[:])
	idSvc, err := identity.LoadOrCreate(ctx, st, masterKey)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}

	authSvc, err := auth.New(st, st.Pool(), testAuthConfig())
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	cfg := testConfig()
	srv, err := NewServer(st, authSvc, idSvc, cfg)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// --- sign up a user (password) and capture the session cookie ---
	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "tester")

	// --- register a repeater whose private key we (the test) hold ---
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

	// --- dial the confirm WebSocket with the auth cookie ---
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/repeaters/" + rep.PublicID + "/ws"
	hdr := http.Header{}
	if cs := jar.Cookies(mustURL(t, ts.URL)); len(cs) > 0 {
		var parts []string
		for _, c := range cs {
			parts = append(parts, c.Name+"="+c.Value)
		}
		hdr.Set("Cookie", strings.Join(parts, "; "))
	}

	dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
	defer dialCancel()
	ws, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	rwCtx, rwCancel := context.WithTimeout(ctx, 15*time.Second)
	defer rwCancel()

	// Tell the server the (simulated) serial port is open.
	if err := ws.Write(rwCtx, websocket.MessageText, []byte(`{"type":"ready"}`)); err != nil {
		t.Fatalf("send ready: %v", err)
	}

	serverID := idSvc.Local().Identity

	// Read server→browser traffic until we see the login data frame, then reply
	// as the repeater. Also collect status messages.
	var loginRaw []byte
	var buf []byte
	for loginRaw == nil {
		typ, data, err := ws.Read(rwCtx)
		if err != nil {
			t.Fatalf("ws read (awaiting login): %v", err)
		}
		if typ == websocket.MessageText {
			continue // status updates (info/tuning); ignore here
		}
		buf = append(buf, data...)
		frames, rest, _ := hardware.ExtractFrames(buf)
		buf = rest
		for _, f := range frames {
			if f.Command != hardware.KISS_CMD_DATA {
				continue // skip SetRadio hardware frame
			}
			pkt, err := meshcore.PacketFromBytes(f.Data)
			if err != nil || pkt.PayloadType() != meshcore.PayloadTypeAnonReq {
				continue
			}
			loginRaw = f.Data
		}
	}

	// Verify the login really decrypts under the repeater↔server secret.
	pkt, _ := meshcore.PacketFromBytes(loginRaw)
	anon, err := meshcore.AnonReqFromBytes(pkt.Payload)
	if err != nil {
		t.Fatalf("parse anon req: %v", err)
	}
	if anon.Destination != repeater.Hash()[0] {
		t.Fatalf("login addressed to 0x%02x, want repeater 0x%02x", anon.Destination, repeater.Hash()[0])
	}
	shared, err := repeater.SharedSecret(serverID)
	if err != nil {
		t.Fatalf("repeater shared secret: %v", err)
	}
	if !anon.VerifyMAC(shared) {
		t.Fatal("repeater could not verify login MAC")
	}

	// Build the repeater's RESPONSE and send it back as a KISS data frame.
	respFrame := buildResponseFrame(t, repeater, serverID)
	if err := ws.Write(rwCtx, websocket.MessageBinary, respFrame); err != nil {
		t.Fatalf("send response: %v", err)
	}

	// Expect a "confirmed" status.
	confirmed := false
	for !confirmed {
		typ, data, err := ws.Read(rwCtx)
		if err != nil {
			t.Fatalf("ws read (awaiting confirm): %v", err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var msg struct{ State, Message string }
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		if msg.State == "error" || msg.State == "timeout" {
			t.Fatalf("unexpected status %q: %s", msg.State, msg.Message)
		}
		if msg.State == "confirmed" {
			confirmed = true
		}
	}

	// The DB row should now be confirmed.
	got, err := st.GetRepeaterForUser(ctx, user.ID, rep.ID)
	if err != nil {
		t.Fatalf("reload repeater: %v", err)
	}
	if !got.Confirmed {
		t.Fatal("repeater not marked confirmed in DB")
	}
}

// buildResponseFrame builds a KISS data frame carrying a repeater login RESPONSE.
func buildResponseFrame(t *testing.T, repeater meshcore.LocalIdentity, server meshcore.Identity) []byte {
	t.Helper()
	shared, err := repeater.SharedSecret(server)
	if err != nil {
		t.Fatalf("shared: %v", err)
	}
	plain := make([]byte, 13)
	binary.LittleEndian.PutUint32(plain[:4], 1_700_000_000)
	plain[4] = 0x00 // RESP_SERVER_LOGIN_OK
	plain[6] = 1    // admin
	plain[7] = 3    // permissions
	enc, err := meshcore.EncryptThenMAC(shared, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	resp := &meshcore.Response{
		Destination:      server.Hash()[0],
		Source:           repeater.Hash()[0],
		MAC:              [2]byte{enc[0], enc[1]},
		EncryptedPayload: enc[2:],
	}
	payload, err := resp.ToBytes()
	if err != nil {
		t.Fatalf("response bytes: %v", err)
	}
	rawPkt := &meshcore.Packet{
		Header:  meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeResponse, 0),
		Payload: payload,
	}
	raw, err := rawPkt.ToBytes()
	if err != nil {
		t.Fatalf("packet bytes: %v", err)
	}
	return hardware.EncodeDataFrame(raw)
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	return u
}
