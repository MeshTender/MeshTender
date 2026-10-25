package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
)

// leaderLockKey is the advisory lock that elects the one replica running broker
// connections. Arbitrary but fixed; distinct from testdb's template lock.
const leaderLockKey int64 = 0x4D54_4D51_5454 // "MTMQTT"

// Timing defaults; tests shorten them through the Supervisor fields.
const (
	defaultReconcileEvery = 30 * time.Second
	queueSize             = 1024
	statusEvery           = time.Minute
	initialBackoff        = time.Second
	maxBackoff            = 5 * time.Minute
	// stableAfter is how long a connection must have lasted for its drop to
	// reset the backoff rather than grow it (a broker that accepts and then
	// immediately closes us is flapping, not recovering).
	stableAfter = time.Minute
)

// Supervisor runs the org broker connections. Only one replica does at a time:
// Run takes a Postgres session lock and the others wait to take over. Writes
// are idempotent, so a brief overlap during handover is harmless.
type Supervisor struct {
	Store     *store.Store
	MasterKey [32]byte
	Log       *slog.Logger
	// Dialer opens every broker connection through the address guard (see
	// dial.go). A field so tests can reach a broker on loopback and trust their
	// own CA; production uses NewDialer.
	Dialer Dialer

	ReconcileEvery time.Duration

	ingester *Ingester
	queue    chan Message
	dropped  atomic.Int64
}

// NewSupervisor returns a Supervisor with production defaults.
func NewSupervisor(st *store.Store, masterKey [32]byte, log *slog.Logger) *Supervisor {
	return &Supervisor{
		Store:          st,
		MasterKey:      masterKey,
		Log:            log,
		Dialer:         NewDialer(),
		ReconcileEvery: defaultReconcileEvery,
	}
}

// Run blocks until ctx is done, acting as leader whenever it holds the lock.
func (s *Supervisor) Run(ctx context.Context) {
	s.ingester = &Ingester{Store: s.Store, Log: s.Log}
	s.queue = make(chan Message, queueSize)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.work(ctx)
	}()
	defer wg.Wait()

	for {
		lock, err := s.Store.TryAcquireSessionLock(ctx, leaderLockKey)
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("ingest: leader lock", "err", err)
		}
		if lock != nil {
			s.Log.Info("ingest: acting as MQTT leader")
			s.lead(ctx, lock)
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			lock.Release(releaseCtx)
			cancel()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.ReconcileEvery):
		}
	}
}

// work drains the queue into the ingester. Messages still queued at shutdown
// are dropped; the next advert will be heard again.
func (s *Supervisor) work(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-s.queue:
			s.ingester.Handle(ctx, m)
		}
	}
}

// enqueue hands a message to the worker without blocking the MQTT read loop,
// which also services keep-alives; a full queue drops the message.
func (s *Supervisor) enqueue(m Message) {
	select {
	case s.queue <- m:
	default:
		if n := s.dropped.Add(1); n%1000 == 1 {
			s.Log.Warn("ingest: queue full, dropping messages", "dropped", n)
		}
	}
}

// running is one live broker connection, keyed by org.
type running struct {
	hash   string
	cancel context.CancelFunc
	done   chan struct{}
}

// lead reconciles connections against the enabled brokers until ctx ends or
// the lock's connection dies, then tears every connection down.
func (s *Supervisor) lead(ctx context.Context, lock *store.SessionLock) {
	conns := map[int64]*running{}
	defer func() {
		for _, r := range conns {
			r.cancel()
			<-r.done
		}
	}()
	for {
		if err := lock.Alive(ctx); err != nil {
			if ctx.Err() == nil {
				s.Log.Warn("ingest: lost leader lock", "err", err)
			}
			return
		}
		s.reconcile(ctx, conns)
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.ReconcileEvery):
		}
	}
}

// brokerHash identifies a broker configuration; any change restarts its
// connection.
func brokerHash(b *store.OrgBroker) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00", b.URL, b.Username, b.TopicPrefix)
	h.Write(b.PasswordSealed)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Supervisor) reconcile(ctx context.Context, conns map[int64]*running) {
	brokers, err := s.Store.ListEnabledBrokers(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.Log.Warn("ingest: list brokers", "err", err)
		}
		return
	}
	want := make(map[int64]*store.OrgBroker, len(brokers))
	for _, b := range brokers {
		want[b.OrgID] = b
	}
	for orgID, r := range conns {
		if b, ok := want[orgID]; !ok || brokerHash(b) != r.hash {
			r.cancel()
			<-r.done
			delete(conns, orgID)
		}
	}
	for orgID, b := range want {
		if _, ok := conns[orgID]; ok {
			continue
		}
		cctx, cancel := context.WithCancel(ctx)
		r := &running{hash: brokerHash(b), cancel: cancel, done: make(chan struct{})}
		conns[orgID] = r
		go func() {
			defer close(r.done)
			s.runBroker(cctx, b)
		}()
	}
}

// runBroker keeps one org's connection up until ctx ends: resolve and vet the
// address, connect, subscribe, then wait for a drop and start over with
// backoff. Every attempt re-resolves, so the address check covers each dial.
func (s *Supervisor) runBroker(ctx context.Context, b *store.OrgBroker) {
	backoff := initialBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := s.session(ctx, b)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.Log.Info("ingest: broker connection failed", "org", b.OrgID, "category", Category(err), "err", err)
			s.recordStatus(b.OrgID, nil, nil, Category(err))
		}
		if time.Since(started) >= stableAfter {
			backoff = initialBackoff
		}
		wait := backoff/2 + rand.N(backoff/2+1) //nolint:gosec // jitter, not security
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// session runs one connection until it drops or ctx ends.
func (s *Supervisor) session(ctx context.Context, b *store.OrgBroker) error {
	var password string
	if len(b.PasswordSealed) > 0 {
		pt, err := secretbox.Open(s.MasterKey, b.PasswordSealed)
		if err != nil {
			s.recordStatus(b.OrgID, nil, nil, CategoryConfig)
			return fmt.Errorf("open broker password: %w", err)
		}
		password = string(pt)
	}
	lost := make(chan error, 1)
	client, err := s.Dialer.Connect(BrokerConfig{OrgID: b.OrgID, URL: b.URL, Username: b.Username, Password: password},
		func(err error) {
			select {
			case lost <- err:
			default:
			}
		})
	if err != nil {
		return err
	}
	defer client.Disconnect(250) // ms to let an in-flight packet finish

	// Paho calls the handler from its router goroutine, so it only queues; the
	// session loop below writes last_message_at on a ticker.
	var lastMsg atomic.Int64
	handler := func(_ mqtt.Client, msg mqtt.Message) {
		now := time.Now()
		lastMsg.Store(now.UnixNano())
		s.enqueue(Message{OrgID: b.OrgID, Prefix: b.TopicPrefix, Topic: msg.Topic(),
			Payload: append([]byte(nil), msg.Payload()...), ReceivedAt: now})
	}
	filters := []string{b.TopicPrefix + "/+/+/packets", b.TopicPrefix + "/+/+/status"}
	if err := Subscribe(client, filters, handler); err != nil {
		return err
	}
	connected := time.Now()
	s.recordStatus(b.OrgID, &connected, nil, "")
	s.Log.Info("ingest: broker connected", "org", b.OrgID)

	tick := time.NewTicker(statusEvery)
	defer tick.Stop()
	var written int64
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-lost:
			return fmt.Errorf("connection lost: %w", err)
		case <-tick.C:
			if n := lastMsg.Load(); n > written {
				written = n
				at := time.Unix(0, n)
				s.recordStatus(b.OrgID, nil, &at, "")
			}
		}
	}
}

func (s *Supervisor) recordStatus(orgID int64, connectedAt, messageAt *time.Time, category string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Store.RecordBrokerStatus(ctx, orgID, connectedAt, messageAt, category); err != nil {
		s.Log.Warn("ingest: record broker status", "org", orgID, "err", err)
	}
}
