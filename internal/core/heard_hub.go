package core

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/MeshTender/MeshTender/internal/store"
)

// heardSubQueue bounds how many heard packets wait for one console session.
// The leader only passes on packets that may be ours (mesh.ClassifyHeard), a
// trickle; a session that falls this far behind drops the excess.
const heardSubQueue = 64

// heardRetryMax caps the wait between attempts to listen again after the
// listening connection fails.
const heardRetryMax = 30 * time.Second

// heardHub hands every packet the ingest leader passes on (store.NotifyHeard)
// to this replica's console sessions, so a session hears replies and repeats
// of its packets through every observer, not only through its own modem or
// relay. One listening connection serves every session on the replica.
type heardHub struct {
	mu   sync.Mutex
	subs map[chan store.HeardPacket]struct{}
}

// subscribe returns a channel of heard packets and a function that stops
// them. Call it once per session.
func (h *heardHub) subscribe() (<-chan store.HeardPacket, func()) {
	ch := make(chan store.HeardPacket, heardSubQueue)
	h.mu.Lock()
	if h.subs == nil {
		h.subs = map[chan store.HeardPacket]struct{}{}
	}
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

// publish hands p to every session without blocking the listener.
func (h *heardHub) publish(p store.HeardPacket) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- p:
		default: // that session is behind; it can do without this copy
		}
	}
}

// run listens until ctx ends, listening again (with backoff) if the
// connection fails.
func (h *heardHub) run(ctx context.Context, st *store.Store) {
	wait := time.Second
	for {
		started := time.Now()
		err := st.ListenHeard(ctx, h.publish)
		if ctx.Err() != nil {
			return
		}
		slog.Warn("heard: listening stopped; listening again", "err", err, "in", wait)
		if time.Since(started) > time.Minute {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, heardRetryMax)
	}
}
