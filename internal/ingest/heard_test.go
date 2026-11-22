package ingest

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/mesh"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/store"
)

// TestIngesterPassesOnHeardPackets: a packet an observer heard that may be a
// reply to MeshTender, or MeshTender's own, reaches every replica's listener
// with the observer that heard it; other traffic doesn't.
func TestIngesterPassesOnHeardPackets(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	server := observetest.NewIdentity(t)
	repeater := observetest.NewIdentity(t)
	in := &Ingester{Store: st, Log: quietLog(), Server: server.Identity}

	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	heard := make(chan store.HeardPacket, 16)
	go func() {
		_ = st.ListenHeard(lctx, func(h store.HeardPacket) {
			select {
			case heard <- h:
			default:
			}
		})
	}()

	now := time.Unix(1_800_000_000, 0)
	ours, err := mesh.BuildCommandPacket(server, repeater.Identity, "get name", now)
	if err != nil {
		t.Fatal(err)
	}
	someoneElse, err := mesh.BuildCommandPacket(repeater, f.companion.Identity, "get name", now)
	if err != nil {
		t.Fatal(err)
	}
	if mesh.ClassifyHeard(someoneElse, server.Identity) != 0 {
		t.Skip("the random keys' one-byte hashes collide; rerun")
	}

	// LISTEN is asynchronous: feed the unrelated packet then ours until ours
	// arrives, and make sure the unrelated one never does.
	deadline := time.After(10 * time.Second)
	for {
		in.Handle(ctx, packetMsg(f.org, f.observer, someoneElse, now))
		in.Handle(ctx, packetMsg(f.org, f.observer, ours, now))
		select {
		case h := <-heard:
			if !bytes.Equal(h.Raw, ours) {
				t.Fatalf("passed on %x, want only our packet", h.Raw)
			}
			if h.Observer != f.observer.String() || !h.At.Equal(now) {
				t.Fatalf("heard by %s at %v, want %s at %v", h.Observer, h.At, f.observer.String(), now)
			}
			return
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatal("our packet was never passed on")
		}
	}
}

// TestIngesterWithoutServerPassesNothing: with no server identity set, the
// ingester passes nothing on (it has no Store here, so touching it would
// panic), even for a packet that would match a zero key.
func TestIngesterWithoutServerPassesNothing(t *testing.T) {
	t.Parallel()
	in := &Ingester{Log: quietLog()}
	login, err := mesh.BuildLoginPacket(observetest.NewIdentity(t), observetest.NewIdentity(t).Identity, "", time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	in.passHeard(context.Background(), Message{}, "", login)
}
