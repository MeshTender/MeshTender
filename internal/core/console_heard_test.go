package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/mqtttest"
	"github.com/MeshTender/MeshTender/internal/relay"
	"github.com/MeshTender/MeshTender/internal/store"
)

func newLocalIdentity(t *testing.T) meshcore.LocalIdentity {
	t.Helper()
	id, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// textReply is the repeater's CLI reply text to server, as a raw packet.
func textReply(t *testing.T, repeater meshcore.LocalIdentity, server meshcore.Identity, text string, at time.Time) []byte {
	t.Helper()
	shared, err := repeater.SharedSecret(server)
	if err != nil {
		t.Fatal(err)
	}
	plain := meshcore.BuildTextPlaintext(at, 1<<2, []byte(text))
	tm, err := meshcore.NewTextMessage(repeater, server, plain, shared)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := tm.ToBytes()
	raw, err := (&meshcore.Packet{Header: meshcore.MakeHeader(meshcore.RouteTypeFlood, meshcore.PayloadTypeTxtMsg, 0), Payload: payload}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// sentPackets is a mesh.Sender that records what's sent and signals each send.
type sentPackets struct {
	mu   sync.Mutex
	pkts [][]byte
	sent chan struct{}
}

func (s *sentPackets) SendData(pkt []byte) error {
	s.mu.Lock()
	s.pkts = append(s.pkts, pkt)
	s.mu.Unlock()
	select {
	case s.sent <- struct{}{}:
	default:
	}
	return nil
}

// TestHeardTrackerHandsEachReplyOverOnce is the duplicate-reply case: a reply
// heard through one observer answers the command, and a late copy of it heard
// through another observer must not be taken as the reply to the next command
// (the exchanger accepts any reply that decrypts).
func TestHeardTrackerHandsEachReplyOverOnce(t *testing.T) {
	t.Parallel()
	server, repeater := newLocalIdentity(t), newLocalIdentity(t)
	link := &sentPackets{sent: make(chan struct{}, 4)}
	tracker := &heardTracker{server: server.Identity}
	ex := mesh.NewExchanger(senderFunc(func(pkt []byte) error {
		tracker.noteSent(pkt)
		return link.SendData(pkt)
	}), server, repeater.Identity, 0, 300*time.Millisecond, 1)
	tracker.ex = ex
	ctx := context.Background()
	reply := textReply(t, repeater, server.Identity, "> v1.2.3", time.Unix(1_800_000_000, 0))

	done := make(chan error, 1)
	var got string
	go func() {
		var err error
		got, err = ex.Command(ctx, "ver", nil)
		done <- err
	}()
	<-link.sent
	tracker.heard(store.HeardPacket{Raw: reply, Observer: "aa"})
	if err := <-done; err != nil || got != "> v1.2.3" {
		t.Fatalf("first command: %q, %v", got, err)
	}

	go func() {
		_, err := ex.Command(ctx, "get name", nil)
		done <- err
	}()
	<-link.sent
	tracker.heard(store.HeardPacket{Raw: reply, Observer: "bb"}) // a late copy of the first reply
	if err := <-done; !errors.Is(err, mesh.ErrNoReply) {
		t.Fatalf("second command took a late copy of the first reply: err = %v", err)
	}
}

// TestHeardTrackerCountsOtherObservers: an exchange's packets, heard by the
// relaying observer itself and by others (one of them twice, once after a
// hop), count each other observer once.
func TestHeardTrackerCountsOtherObservers(t *testing.T) {
	t.Parallel()
	server, repeater := newLocalIdentity(t), newLocalIdentity(t)
	tracker := &heardTracker{server: server.Identity, via: "relayobserver"}
	tracker.ex = mesh.NewExchanger(&sentPackets{sent: make(chan struct{}, 1)}, server, repeater.Identity, 0, time.Millisecond, 1)

	pkt, err := mesh.BuildCommandPacket(server, repeater.Identity, "ver", time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := meshcore.PacketFromBytes(pkt)
	if err != nil {
		t.Fatal(err)
	}
	repeated.PathLength, repeated.Path = repeated.PathLength+1, []byte{0x11, 0x22, 0x33}
	hop, err := repeated.ToBytes()
	if err != nil {
		t.Fatal(err)
	}

	tracker.begin()
	tracker.noteSent(pkt)
	op := tracker.end()
	if n := tracker.otherObservers(op); n != 0 {
		t.Fatalf("before anyone heard it: %d", n)
	}
	for _, h := range []store.HeardPacket{
		{Raw: pkt, Observer: "RELAYOBSERVER"}, // the sender itself, in another case
		{Raw: pkt, Observer: "aa"},
		{Raw: hop, Observer: "aa"},
		{Raw: hop, Observer: "bb"},
	} {
		tracker.heard(h)
	}
	if n := tracker.otherObservers(op); n != 2 {
		t.Fatalf("other observers = %d, want 2", n)
	}
	for n, want := range map[int]string{
		0: "No other observer heard your command.",
		1: "Your command was heard by 1 other observer.",
		2: "Your command was heard by 2 other observers.",
	} {
		if got := heardMessage("command", n); got != want {
			t.Errorf("heardMessage(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestConsoleHearsRepliesThroughOtherObservers drives a relay session whose
// observer uploads nothing it hears: the login reply and the command reply
// arrive only through other observers (the ingest leader's NOTIFY), and the
// command is reported as heard by the two other observers that heard it.
func TestConsoleHearsRepliesThroughOtherObservers(t *testing.T) {
	t.Parallel()
	h := newRelayHarness(t, true)
	others := []string{newLocalIdentity(t).String(), newLocalIdentity(t).String()}
	notify := func(raw []byte, observer string) {
		if err := h.st.NotifyHeard(h.ctx, store.HeardPacket{Raw: raw, Observer: observer, At: time.Now()}); err != nil {
			t.Error(err)
		}
	}
	const replyText = "> v1.2.3 (test build)"
	h.broker.OnPublish(func(m mqtttest.Message) {
		if m.Topic != relay.TxTopic(h.observer.Identity) {
			return
		}
		h.answer(m.Payload, "")
		pkt := h.framePacket(m.Payload)
		raw, err := pkt.ToBytes()
		if err != nil {
			t.Error(err)
			return
		}
		// The relaying observer and two others hear it go out.
		notify(raw, h.observer.String())
		for _, o := range others {
			notify(raw, o)
		}
		switch pkt.PayloadType() {
		case meshcore.PayloadTypeAnonReq:
			notify(buildResponsePacket(t, h.repeater, h.server), others[0])
		case meshcore.PayloadTypeTxtMsg:
			notify(textReply(t, h.repeater, h.server, replyText, time.Now()), others[1])
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
	ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
	defer cancel()
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			t.Fatalf("never said who heard the command: %v", err)
		}
		var m struct{ State, Message string }
		if json.Unmarshal(data, &m) == nil && strings.HasPrefix(m.Message, "Your command was heard") {
			if want := heardMessage("command", 2); m.Message != want {
				t.Fatalf("%q, want %q", m.Message, want)
			}
			return
		}
	}
}
