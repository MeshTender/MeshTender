package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/identity"
	"github.com/MeshTender/MeshTender/internal/ingest"
	"github.com/MeshTender/MeshTender/internal/mqtttest"
	"github.com/MeshTender/MeshTender/internal/relay"
	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
)

// relayHarness is a console that sends through an observer: a server whose
// relay dialer reaches a loopback test broker, a user who owns a repeater, and
// an observer key. With approve set, the user's org has approved the observer
// and has the broker set up, so the user may relay through it.
type relayHarness struct {
	t        *testing.T
	st       *store.Store
	ctx      context.Context
	broker   *mqtttest.Broker
	server   meshcore.Identity
	repeater meshcore.LocalIdentity
	observer meshcore.LocalIdentity
	rep      *store.Repeater
	user     *store.User
	ts       *httptest.Server
	jar      http.CookieJar
}

func newRelayHarness(t *testing.T, approve bool) *relayHarness {
	t.Helper()
	st, ctx := coreStore(t)
	idSvc, err := identity.LoadOrCreate(ctx, st, testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := NewServer(st, testAuth(t, st, testAuthConfig()), idSvc, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	broker := mqtttest.New(t, "s3cret", "ws")
	srv.app.relayDialer = &ingest.Dialer{
		Resolver: net.DefaultResolver,
		Allow:    func(netip.Addr) bool { return true }, // the broker is on loopback
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "relayuser")
	h := &relayHarness{t: t, st: st, ctx: ctx, broker: broker, server: idSvc.Local().Identity, user: user, ts: ts, jar: jar}
	if h.repeater, err = meshcore.GenerateLocalIdentity(rand.Reader); err != nil {
		t.Fatal(err)
	}
	if h.observer, err = meshcore.GenerateLocalIdentity(rand.Reader); err != nil {
		t.Fatal(err)
	}
	h.rep, err = st.CreateRepeater(ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Hilltop", PublicKeyHex: h.repeater.String(),
		RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if approve {
		org, err := st.CreateOrg(ctx, "Relay Org", user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.CreateOrgObserver(ctx, org.ID, "Roof", h.observer.String(), user.ID); err != nil {
			t.Fatal(err)
		}
		sealed, err := secretbox.Seal(testMasterKey, []byte("s3cret"))
		if err != nil {
			t.Fatal(err)
		}
		b := store.OrgBroker{OrgID: org.ID, URL: broker.URL(), Username: "meshtender", TopicPrefix: "meshcore", Enabled: true}
		if err := st.UpsertOrgBroker(ctx, b, sealed, false, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

// dial opens the console WebSocket through the observer and says the page is
// ready, as air-console.js does with no serial port.
func (h *relayHarness) dial() *websocket.Conn {
	h.t.Helper()
	wsURL := "ws" + strings.TrimPrefix(h.ts.URL, "http") + "/repeaters/" + h.rep.PublicID + "/console/ws?via=" + h.observer.String()
	hdr := http.Header{}
	for _, c := range h.jar.Cookies(mustURL(h.t, h.ts.URL)) {
		hdr.Add("Cookie", c.Name+"="+c.Value)
	}
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = ws.Close(websocket.StatusNormalClosure, "") })
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"ready"}`)); err != nil {
		h.t.Fatal(err)
	}
	return ws
}

// uplink publishes raw as the observer hearing it.
func (h *relayHarness) uplink(raw []byte) {
	key := strings.ToUpper(h.observer.String())
	h.broker.Publish("meshcore/BUF/"+key+"/packets", []byte(`{"raw":"`+hex.EncodeToString(raw)+`"}`))
}

// answer publishes the observer's verdict on the frame.
func (h *relayHarness) answer(frame []byte, code string) {
	id := binary.LittleEndian.Uint32(frame[2:6])
	res := fmt.Sprintf(`{"v":1,"id":%d,"ok":true}`, id)
	if code != "" {
		res = fmt.Sprintf(`{"v":1,"id":%d,"ok":false,"error":%q}`, id, code)
	}
	h.broker.Publish(relay.ResultTopic(h.observer.Identity), []byte(res))
}

// framePacket checks a frame is signed by the server for this observer and
// returns the packet inside.
func (h *relayHarness) framePacket(frame []byte) *meshcore.Packet {
	h.t.Helper()
	const header, sig = 43, 64
	if len(frame) < header+sig {
		h.t.Fatalf("frame of %d bytes", len(frame))
	}
	body, s := frame[:len(frame)-sig], frame[len(frame)-sig:]
	msg := append(append([]byte("MTTX"), h.observer.PublicKeyBytes()...), body...)
	if !h.server.Verify(msg, s) {
		h.t.Fatal("frame isn't signed by the server for this observer")
	}
	pkt, err := meshcore.PacketFromBytes(body[header:])
	if err != nil {
		h.t.Fatal(err)
	}
	return pkt
}

// readUntil reads statuses until one in want arrives, failing on an error or
// noreply status not in want.
func readUntil(t *testing.T, ws *websocket.Conn, want ...string) (state, message string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %v: %v", want, err)
		}
		var m struct{ State, Message string }
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		for _, w := range want {
			if m.State == w {
				return m.State, m.Message
			}
		}
		if m.State == "error" || m.State == "noreply" || m.State == "denied" {
			t.Fatalf("waiting for %v: got %q: %s", want, m.State, m.Message)
		}
	}
}

// TestConsoleRelayRoundTrip drives the console through an observer: the test
// plays the browser (no serial port), the broker, the observer (answers each
// frame and uplinks what it hears) and the repeater.
func TestConsoleRelayRoundTrip(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, true)
	shared, err := h.repeater.SharedSecret(h.server)
	if err != nil {
		t.Fatal(err)
	}
	const replyText = "> v1.2.3 (test build)"
	h.broker.OnPublish(func(m mqtttest.Message) {
		if m.Topic != relay.TxTopic(h.observer.Identity) || m.Retain {
			return
		}
		h.answer(m.Payload, "")
		pkt := h.framePacket(m.Payload)
		switch pkt.PayloadType() {
		case meshcore.PayloadTypeAnonReq:
			h.uplink(buildResponsePacket(t, h.repeater, h.server))
		case meshcore.PayloadTypeTxtMsg:
			tm, err := meshcore.TextMessageFromBytes(pkt.Payload)
			if err != nil {
				return
			}
			if plain := tm.Decrypt(shared); plain == nil || !strings.HasPrefix(string(plain[5:]), "ver") {
				return
			}
			plain := meshcore.BuildTextPlaintext(time.Unix(1_700_002_000, 0), 1<<2, []byte(replyText))
			rtm, err := meshcore.NewTextMessage(h.repeater, h.server, plain, shared)
			if err != nil {
				return
			}
			payload, _ := rtm.ToBytes()
			raw, _ := (&meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}).ToBytes()
			h.uplink(raw)
		}
	})

	ws := h.dial()
	readUntil(t, ws, "ready")
	if err := ws.Write(h.ctx, websocket.MessageText, []byte(`{"type":"cmd","text":"ver"}`)); err != nil {
		t.Fatal(err)
	}
	if _, got := readUntil(t, ws, "reply"); got != replyText {
		t.Fatalf("reply = %q, want %q", got, replyText)
	}

	for _, m := range h.broker.Published() {
		if m.QoS != 1 || m.Retain {
			t.Errorf("frame on %s: qos %d retain %v, want qos 1 and not retained", m.Topic, m.QoS, m.Retain)
		}
	}
	entries, err := h.st.ListCommandLog(h.ctx, h.rep.ID, 10)
	if err != nil || len(entries) != 1 || entries[0].ResponseText == nil || *entries[0].ResponseText != replyText {
		t.Fatalf("command log = %+v, %v", entries, err)
	}
}

func TestConsoleRelayRefusedObserver(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, false) // the user's orgs never approved the observer
	ws := h.dial()
	if _, msg := readUntil(t, ws, "error"); !strings.Contains(msg, "organization you administer") {
		t.Fatalf("error = %q", msg)
	}
	if n := h.broker.Live(); n != 0 {
		t.Errorf("%d broker connections, want none", n)
	}
}

func TestConsoleRelayShowsTheObserversRefusal(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, true)
	h.broker.OnPublish(func(m mqtttest.Message) {
		if m.Topic == relay.TxTopic(h.observer.Identity) {
			h.answer(m.Payload, relay.ErrCodeNotAdmin)
		}
	})
	ws := h.dial()
	if _, msg := readUntil(t, ws, "warning"); msg != relayRefusal(relay.ErrCodeNotAdmin) {
		t.Fatalf("warning = %q", msg)
	}
}

func TestConsoleRelayExplainsSilence(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, true) // the observer never answers: TX is off on its slot
	ws := h.dial()
	ctx, cancel := context.WithTimeout(h.ctx, 15*time.Second)
	defer cancel()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("never got the hint: %v", err)
		}
		var m struct{ State, Message string }
		if json.Unmarshal(data, &m) == nil && m.Message == relayNoReplyHint {
			return
		}
	}
}

func TestConsoleRejectsABadObserverKey(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, false)
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+"/repeaters/"+h.rep.PublicID+"/console/ws?via=nothex", nil)
	for _, c := range h.jar.Cookies(mustURL(t, h.ts.URL)) {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}
