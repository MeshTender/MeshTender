// Package ingest connects to each org's MQTT broker and records verified
// adverts from registered companions, as heard by the org's registered
// observers. The Supervisor owns the broker connections (on one elected
// replica); the Ingester turns each message into store writes.
package ingest

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/store"
)

// MaxAdvertSkew bounds how far an advert's own timestamp may be from when it
// was heard. A signature proves who sent an advert, not when; a captured advert
// replayed later still verifies, so this is the replay guard. Companions take
// their clock from the phone app and were seen within seconds; ten minutes
// leaves room for a drifting clock without letting last week's advert check
// someone in.
const MaxAdvertSkew = 10 * time.Minute

// observerTouchEvery throttles last_seen_at writes from packet traffic: a busy
// observer uplinks several packets a second, and "online" only needs minutes.
const observerTouchEvery = time.Minute

// maxTouchEntries caps the throttle map. Topic keys come from the broker, so
// the map is cleared rather than allowed to grow without bound.
const maxTouchEntries = 10_000

// Message is one MQTT message received on an org's broker connection.
type Message struct {
	OrgID      int64
	Prefix     string
	Topic      string
	Payload    []byte
	ReceivedAt time.Time
}

// Ingester applies messages to the store. Handle is called from a single
// worker goroutine, but the throttle map is locked anyway so tests and future
// callers can't race it.
type Ingester struct {
	Store *store.Store
	Log   *slog.Logger

	mu      sync.Mutex
	touched map[touchKey]time.Time
}

type touchKey struct {
	orgID int64
	key   string
}

// Handle processes one message. Errors are logged, not returned: one bad
// message must never stall the stream.
func (in *Ingester) Handle(ctx context.Context, m Message) {
	t, err := observe.ParseTopic(m.Prefix, m.Topic)
	if err != nil {
		if !errors.Is(err, observe.ErrForeignTopic) {
			in.Log.Debug("ingest: bad topic", "org", m.OrgID, "err", err)
		}
		return
	}
	observer := t.Observer.String()
	switch t.Kind {
	case observe.KindStatus:
		in.handleStatus(ctx, m, observer)
	case observe.KindPackets:
		in.touch(ctx, m, observer)
		in.handlePacket(ctx, m, observer)
	}
}

func (in *Ingester) handleStatus(ctx context.Context, m Message, observer string) {
	st, err := observe.ParseStatus(m.Payload)
	if err != nil {
		in.Log.Debug("ingest: bad status", "org", m.OrgID, "observer", observer, "err", err)
		return
	}
	// TouchObserver only updates a registered observer's row, so an
	// unregistered one is a no-op without a separate lookup.
	if err := in.Store.TouchObserver(ctx, m.OrgID, observer, m.ReceivedAt, &st); err != nil {
		in.Log.Warn("ingest: touch observer", "org", m.OrgID, "err", err)
	}
}

// touch records packet traffic as observer liveness, at most once a minute per
// (org, observer).
func (in *Ingester) touch(ctx context.Context, m Message, observer string) {
	k := touchKey{m.OrgID, observer}
	in.mu.Lock()
	if in.touched == nil || len(in.touched) >= maxTouchEntries {
		in.touched = make(map[touchKey]time.Time)
	}
	last, seen := in.touched[k]
	due := !seen || m.ReceivedAt.Sub(last) >= observerTouchEvery
	if due {
		in.touched[k] = m.ReceivedAt
	}
	in.mu.Unlock()
	if !due {
		return
	}
	if err := in.Store.TouchObserver(ctx, m.OrgID, observer, m.ReceivedAt, nil); err != nil {
		in.Log.Warn("ingest: touch observer", "org", m.OrgID, "err", err)
	}
}

// handlePacket records the packet if it's a verified advert from a registered
// companion heard by one of the org's observers. The checks run cheapest
// first: most packets aren't adverts (in-memory decode), most adverts aren't
// from registered companions (one indexed lookup), and only then is the
// signature checked.
func (in *Ingester) handlePacket(ctx context.Context, m Message, observer string) {
	p, err := observe.ParsePacket(m.Payload)
	if err != nil {
		return
	}
	adv, err := observe.DecodeAdvert(p.Raw)
	if err != nil {
		return
	}
	companionID, err := in.Store.CompanionIDForKey(ctx, adv.PublicKey.String())
	if errors.Is(err, store.ErrNotFound) {
		return
	}
	if err != nil {
		in.Log.Warn("ingest: companion lookup", "err", err)
		return
	}
	ok, err := in.Store.IsOrgObserver(ctx, m.OrgID, observer)
	if err != nil {
		in.Log.Warn("ingest: observer lookup", "err", err)
		return
	}
	if !ok {
		return
	}
	if !adv.Verify() {
		in.Log.Warn("ingest: advert signature invalid", "org", m.OrgID, "observer", observer,
			"companion", companionID)
		return
	}
	if skew := adv.Skew(m.ReceivedAt); skew > MaxAdvertSkew || skew < -MaxAdvertSkew {
		if err := in.Store.RecordAdvertRejected(ctx, companionID, m.ReceivedAt, skew); err != nil {
			in.Log.Warn("ingest: record rejected", "err", err)
		}
		return
	}
	// The path's repeaters go in unordered; the store keeps them only long
	// enough to count (store.AdvertSetTTL).
	_, err = in.Store.RecordAdvertHeard(ctx, store.AdvertHeard{
		CompanionID:          companionID,
		AdvertTS:             adv.Timestamp,
		ObserverPublicKeyHex: observer,
		ReceivedAt:           m.ReceivedAt,
		ZeroHop:              adv.ZeroHop(),
		Relays:               adv.Path,
		Name:                 adv.Name,
	})
	if err != nil {
		in.Log.Warn("ingest: record check-in", "err", err)
	}
}
