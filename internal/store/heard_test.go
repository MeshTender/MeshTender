package store

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestHeardReachesEveryListener(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Two listeners stand in for two replicas.
	got := [2]chan HeardPacket{make(chan HeardPacket, 1), make(chan HeardPacket, 1)}
	done := make(chan error, 2)
	for i := range got {
		ch := got[i]
		go func() {
			done <- st.ListenHeard(lctx, func(h HeardPacket) {
				select { // never block: a blocked handler couldn't see the cancel
				case ch <- h:
				default:
				}
			})
		}()
	}

	want := HeardPacket{Raw: []byte{0x0A, 0x02, 0xAB}, Observer: testKey(9), At: time.Unix(1_800_000_000, 0).UTC()}
	// LISTEN is asynchronous to this goroutine: notify until both have heard.
	deadline := time.After(10 * time.Second)
	var heard [2]*HeardPacket
	for heard[0] == nil || heard[1] == nil {
		if err := st.NotifyHeard(ctx, want); err != nil {
			t.Fatal(err)
		}
		select {
		case h := <-got[0]:
			heard[0] = &h
		case h := <-got[1]:
			heard[1] = &h
		case <-time.After(100 * time.Millisecond):
		case <-deadline:
			t.Fatalf("heard by %v", heard)
		}
	}
	for i, h := range heard {
		if !bytes.Equal(h.Raw, want.Raw) || h.Observer != want.Observer || !h.At.Equal(want.At) {
			t.Errorf("listener %d got %+v, want %+v", i, *h, want)
		}
	}

	cancel()
	for range got {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("a listener didn't stop when its context ended")
		}
	}
}
