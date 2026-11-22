package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/wsbridge"
)

// heardSettle is how long after an exchange ends a session waits before saying
// how many other observers heard its packets: a flood's repeats reach
// observers a hop or two later, and uploads take a moment.
var heardSettle = 3 * time.Second

// maxHeardKeys bounds a session's memory of packets. A console session sends
// a few packets a minute, so this is only a guard.
const maxHeardKeys = 4096

type packetKey = [meshcore.PacketHashSize]byte

// heardTracker is one console session's view of what observers hear. Every
// packet it's given goes to the exchanger once, however many observers (and
// its own modem or relay) heard it: copies of one reply arrive a second or so
// apart, and a late copy must not be taken as the reply to the next request.
// It also counts which other observers heard each packet the session sent,
// which is all that can be said about whether it went out; a missing reply
// can't be put down to any one cause.
//
// Replies heard through other observers say nothing about the link back to
// the sending device, so nothing here may feed link or round-trip
// measurements.
type heardTracker struct {
	ex     *mesh.Exchanger
	server meshcore.Identity
	// via is the relaying observer's key, "" for a modem session. It hearing
	// its own packet isn't another observer hearing it.
	via string
	// debug, if set, gets a line for each reply heard through another observer.
	debug func(string)

	mu        sync.Mutex
	delivered map[packetKey]struct{}
	sent      map[packetKey]map[string]struct{} // our packets → observers that heard them
	op        *heardOp
}

// heardOp is one exchange (a login or a command): the packets sent for it.
type heardOp struct {
	keys []packetKey
}

// feed hands raw to the exchanger unless a copy of it already went there.
// from is the observer it was heard through, "" for the session's own modem
// or relay.
func (t *heardTracker) feed(raw []byte, from string) {
	key, ok := mesh.PacketKey(raw)
	if !ok {
		return
	}
	t.mu.Lock()
	if t.delivered == nil || len(t.delivered) >= maxHeardKeys {
		t.delivered = map[packetKey]struct{}{}
	}
	_, dup := t.delivered[key]
	t.delivered[key] = struct{}{}
	t.mu.Unlock()
	if dup {
		return
	}
	if from != "" && t.debug != nil && mesh.ClassifyHeard(raw, t.server)&mesh.HeardReply != 0 {
		t.debug("Heard a reply through observer " + shortKey(from) + ".")
	}
	t.ex.HandleData(raw)
}

// noteSent records a packet the session is about to send, under the current
// exchange.
func (t *heardTracker) noteSent(raw []byte) {
	key, ok := mesh.PacketKey(raw)
	if !ok {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sent == nil || len(t.sent) >= maxHeardKeys {
		t.sent = map[packetKey]map[string]struct{}{}
	}
	if _, ok := t.sent[key]; !ok {
		t.sent[key] = map[string]struct{}{}
	}
	if t.op != nil {
		t.op.keys = append(t.op.keys, key)
	}
}

// heard takes a packet the ingest leader passed on: a copy of one the session
// sent is counted against the observer that heard it; anything else may be a
// reply, and goes to the exchanger.
func (t *heardTracker) heard(p store.HeardPacket) {
	key, ok := mesh.PacketKey(p.Raw)
	if !ok {
		return
	}
	t.mu.Lock()
	observers, ours := t.sent[key]
	if ours && !strings.EqualFold(p.Observer, t.via) {
		observers[strings.ToLower(p.Observer)] = struct{}{}
	}
	t.mu.Unlock()
	if !ours {
		t.feed(p.Raw, p.Observer)
	}
}

// begin starts an exchange: packets sent until end are counted together.
func (t *heardTracker) begin() {
	t.mu.Lock()
	t.op = &heardOp{}
	t.mu.Unlock()
}

// end finishes the exchange begun last and returns it.
func (t *heardTracker) end() *heardOp {
	t.mu.Lock()
	defer t.mu.Unlock()
	op := t.op
	t.op = nil
	return op
}

// otherObservers is how many observers, other than the relaying one, heard
// any of op's packets.
func (t *heardTracker) otherObservers(op *heardOp) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	all := map[string]struct{}{}
	for _, k := range op.keys {
		for o := range t.sent[k] {
			all[o] = struct{}{}
		}
	}
	return len(all)
}

// reportLater says, heardSettle after an exchange ends, how many other
// observers heard what was sent for it. what names it: "login", "command".
func (t *heardTracker) reportLater(ctx context.Context, op *heardOp, what string, bridge *wsbridge.Conn) {
	if op == nil || len(op.keys) == 0 {
		return
	}
	time.AfterFunc(heardSettle, func() {
		if ctx.Err() != nil {
			return
		}
		_ = bridge.Status("info", heardMessage(what, t.otherObservers(op)))
	})
}

// heardMessage states how many other observers heard a packet: a fact, with
// no guess at why a reply did or didn't come.
func heardMessage(what string, n int) string {
	switch n {
	case 0:
		return "No other observer heard your " + what + "."
	case 1:
		return "Your " + what + " was heard by 1 other observer."
	default:
		return fmt.Sprintf("Your %s was heard by %d other observers.", what, n)
	}
}

// shortKey is the first 8 hex digits of a public key, for the log.
func shortKey(key string) string {
	if len(key) > 8 {
		return strings.ToUpper(key[:8])
	}
	return strings.ToUpper(key)
}
