// Package ingest connects to each org's MQTT broker, discovers the observers
// publishing to it, and records verified adverts from registered companions,
// as heard by the org's approved observers, and the channel messages a running
// net logs (see netair). The Supervisor owns the broker connections (on one
// elected replica); the Ingester turns each message into store writes.
package ingest

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/netair"
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
	// MasterKey opens a private net channel's key.
	MasterKey [32]byte
	Log       *slog.Logger

	mu      sync.Mutex
	touched map[touchKey]time.Time

	// The running nets' channel logs by org, as of logsMinute. Sessions start
	// and end on whole minutes, so the set only changes when the minute does.
	logsMu     sync.Mutex
	logsMinute time.Time
	logs       map[int64][]netair.Log
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
		in.handleStatus(ctx, m, observer, t.IATA)
	case observe.KindPackets:
		in.touch(ctx, m, observer)
		in.handlePacket(ctx, m, observer)
	}
}

// handleStatus records an observer's status report, discovering the observer
// (unapproved) if the org doesn't have it yet. The broker only lets an uplink
// publish under its own key, so the topic's key and region and the reported
// name are the observer's own.
func (in *Ingester) handleStatus(ctx context.Context, m Message, observer, iata string) {
	st, err := observe.ParseStatus(m.Payload)
	if err != nil {
		in.Log.Debug("ingest: bad status", "org", m.OrgID, "observer", observer, "err", err)
		return
	}
	if err := in.Store.ReportObserver(ctx, m.OrgID, observer, iata, m.ReceivedAt, &st); err != nil {
		in.Log.Warn("ingest: report observer", "org", m.OrgID, "err", err)
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
	if err := in.Store.TouchObserver(ctx, m.OrgID, observer, m.ReceivedAt); err != nil {
		in.Log.Warn("ingest: touch observer", "org", m.OrgID, "err", err)
	}
}

// handlePacket records what the org's observers hear that MeshTender cares
// about: adverts from registered companions, and channel messages a running
// net logs. The checks run cheapest first: most packets are neither
// (in-memory decode), and the observer lookup waits until one is.
func (in *Ingester) handlePacket(ctx context.Context, m Message, observer string) {
	p, err := observe.ParsePacket(m.Payload)
	if err != nil {
		return
	}
	pkt, err := meshcore.PacketFromBytes(p.Raw)
	if err != nil {
		return
	}
	switch pkt.PayloadType() {
	case meshcore.PayloadTypeAdvert:
		in.handleAdvert(ctx, m, observer, p.Raw)
	case meshcore.PayloadTypeGrpTxt:
		in.handleChannelMessage(ctx, m, observer, p.Raw)
	}
}

// handleAdvert records a verified advert from a registered companion. Most
// adverts aren't from one (one indexed lookup), and only then is the
// signature checked.
func (in *Ingester) handleAdvert(ctx context.Context, m Message, observer string, raw []byte) {
	adv, companionID, err := RegisteredAdvert(ctx, in.Store, raw)
	if err != nil {
		if !errors.Is(err, ErrNotRegistered) {
			in.Log.Warn("ingest: companion lookup", "err", err)
		}
		return
	}
	if !in.isObserver(ctx, m, observer) {
		return
	}
	_, err = RecordAdvert(ctx, in.Store, adv, companionID, observer, m.ReceivedAt)
	switch {
	case errors.Is(err, ErrAdvertSignature):
		in.Log.Warn("ingest: advert signature invalid", "org", m.OrgID, "observer", observer,
			"companion", companionID)
	case errors.Is(err, ErrAdvertSkew):
	case err != nil:
		in.Log.Warn("ingest: record check-in", "err", err)
	}
}

// handleChannelMessage logs a channel message to each of the org's running
// nets that logs it (netair.Log.Record decides).
func (in *Ingester) handleChannelMessage(ctx context.Context, m Message, observer string, raw []byte) {
	logs := in.netLogs(ctx, time.Now())[m.OrgID]
	if len(logs) == 0 || !in.isObserver(ctx, m, observer) {
		return
	}
	for _, l := range logs {
		if _, _, _, err := l.Record(ctx, in.Store, raw, m.ReceivedAt); err != nil {
			in.Log.Warn("ingest: log channel message", "org", m.OrgID, "session", l.SessionID, "err", err)
		}
	}
}

func (in *Ingester) isObserver(ctx context.Context, m Message, observer string) bool {
	ok, err := in.Store.IsOrgObserver(ctx, m.OrgID, observer)
	if err != nil {
		in.Log.Warn("ingest: observer lookup", "err", err)
		return false
	}
	return ok
}

// netLogs returns the running nets' channel logs by org, reloading them when
// the minute changes. A failed reload logs nothing — the previous minute's
// could hold a session that just ended — and retries on the next message.
func (in *Ingester) netLogs(ctx context.Context, now time.Time) map[int64][]netair.Log {
	minute := now.Truncate(time.Minute)
	in.logsMu.Lock()
	defer in.logsMu.Unlock()
	if in.logs != nil && in.logsMinute.Equal(minute) {
		return in.logs
	}
	active, err := in.Store.ListActiveNetLogs(ctx, now)
	if err != nil {
		in.Log.Warn("ingest: load net logs", "err", err)
		return nil
	}
	logs := make(map[int64][]netair.Log)
	for _, a := range active {
		ch, err := netair.Channel(&a.Air, in.MasterKey)
		if err != nil {
			in.Log.Warn("ingest: net channel", "org", a.OrgID, "session", a.SessionID, "err", err)
			continue
		}
		logs[a.OrgID] = append(logs[a.OrgID], netair.Log{SessionID: a.SessionID, Channel: ch,
			Prefix: a.Air.LogPrefix, Sender: netair.Sender(&a.Air, a.NetName)})
	}
	in.logs, in.logsMinute = logs, minute
	return logs
}

// Errors from RegisteredAdvert and RecordAdvert for adverts that don't count.
var (
	// ErrNotRegistered: not an advert, or not from a registered companion.
	ErrNotRegistered = errors.New("ingest: not a registered companion's advert")
	// ErrAdvertSignature: the advert's signature doesn't verify.
	ErrAdvertSignature = errors.New("ingest: advert signature invalid")
	// ErrAdvertSkew: the sender's clock is too far off (a possible replay); the
	// companion's owner is shown the rejection.
	ErrAdvertSkew = errors.New("ingest: advert clock skew")
)

// RegisteredAdvert decodes raw as an advert from a registered companion,
// returning ErrNotRegistered for any other packet. The advert isn't verified
// yet: RecordAdvert does that, so callers can run cheaper checks in between.
// Shared by the MQTT ingester and net control's modem.
func RegisteredAdvert(ctx context.Context, st *store.Store, raw []byte) (*observe.Advert, int64, error) {
	adv, err := observe.DecodeAdvert(raw)
	if err != nil {
		return nil, 0, ErrNotRegistered
	}
	companionID, err := st.CompanionIDForKey(ctx, adv.PublicKey.String())
	if errors.Is(err, store.ErrNotFound) {
		return nil, 0, ErrNotRegistered
	}
	if err != nil {
		return nil, 0, err
	}
	return adv, companionID, nil
}

// RecordAdvert verifies a registered companion's advert and records it as a
// check-in heard by observer at receivedAt. A bad signature is
// ErrAdvertSignature; a timestamp more than MaxAdvertSkew from receivedAt is
// recorded as a rejection and returned as ErrAdvertSkew. created reports
// whether this copy was the first heard of the advert.
func RecordAdvert(ctx context.Context, st *store.Store, adv *observe.Advert, companionID int64, observer string, receivedAt time.Time) (created bool, err error) {
	if !adv.Verify() {
		return false, ErrAdvertSignature
	}
	if skew := adv.Skew(receivedAt); skew > MaxAdvertSkew || skew < -MaxAdvertSkew {
		if err := st.RecordAdvertRejected(ctx, companionID, receivedAt, skew); err != nil {
			return false, err
		}
		return false, ErrAdvertSkew
	}
	// The path's repeaters go in unordered; the store keeps them only long
	// enough to count (store.AdvertSetTTL).
	return st.RecordAdvertHeard(ctx, store.AdvertHeard{
		CompanionID:          companionID,
		AdvertTS:             adv.Timestamp,
		ObserverPublicKeyHex: observer,
		ReceivedAt:           receivedAt,
		ZeroHop:              adv.ZeroHop(),
		Relays:               adv.Path,
		Name:                 adv.Name,
	})
}
