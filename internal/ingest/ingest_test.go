package ingest

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	mqtt "github.com/SukramJ/go-mqtt"
	"github.com/SukramJ/go-mqtt/protocol"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/secretbox"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/testdb"
)

func TestMain(m *testing.M) {
	os.Exit(testdb.RunMain(m))
}

func ingestMigrate(dsn string) error {
	ctx := context.Background()
	s, err := store.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Migrate(ctx)
}

func newStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.New(ctx, testdb.Fresh(t, ingestMigrate))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st, ctx
}

func quietLog() *slog.Logger { return slog.New(slog.DiscardHandler) }

// fixture is an org with one registered observer and one member companion.
type fixture struct {
	st        *store.Store
	org       int64
	owner     int64
	observer  meshcore.LocalIdentity
	companion meshcore.LocalIdentity
	compID    string // public id
}

func newFixture(t *testing.T, st *store.Store, ctx context.Context) *fixture {
	t.Helper()
	u, err := st.CreateUser(ctx, "ingestowner", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "IngestOrg", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{st: st, org: org.ID, owner: u.ID,
		observer: observetest.NewIdentity(t), companion: observetest.NewIdentity(t)}
	if err := st.CreateOrgObserver(ctx, f.org, "Roof", f.observer.String(), u.ID); err != nil {
		t.Fatal(err)
	}
	c, err := st.CreateCompanion(ctx, u.ID, "Owner T1", f.companion.String())
	if err != nil {
		t.Fatal(err)
	}
	f.compID = c.PublicID
	return f
}

// packetMsg wraps a raw packet the way an observer uplink publishes it.
func packetMsg(org int64, observer meshcore.LocalIdentity, raw []byte, at time.Time) Message {
	return Message{
		OrgID:      org,
		Prefix:     "meshcore",
		Topic:      "meshcore/BUF/" + strings.ToUpper(observer.String()) + "/packets",
		Payload:    fmt.Appendf(nil, `{"raw":"%s","SNR":5.5,"RSSI":"-90"}`, strings.ToUpper(hex.EncodeToString(raw))),
		ReceivedAt: at,
	}
}

func (f *fixture) companionState(t *testing.T, ctx context.Context) *store.Companion {
	t.Helper()
	c, err := f.st.GetCompanionForViewer(ctx, f.compID, f.owner)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fixture) sightings(t *testing.T, ctx context.Context) []store.AdvertSighting {
	t.Helper()
	c := f.companionState(t, ctx)
	page, _, err := f.st.ListCompanionSightingsPage(ctx, c.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestIngesterRecordsVerifiedAdvert(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	in := &Ingester{Store: st, Log: quietLog()}

	now := time.Now().Add(time.Second)
	path := [][]byte{{0x35, 0x2c, 0x30}, {0x8b, 0xfc, 0xfc}}
	raw := observetest.AdvertPacket(t, f.companion, uint32(now.Unix()), "Owner T1", meshcore.RouteTypeFlood, 3, path) //nolint:gosec // test clock
	m := packetMsg(f.org, f.observer, raw, now)
	in.Handle(ctx, m)
	in.Handle(ctx, m) // an uplink duplicate collapses

	got := f.sightings(t, ctx)
	if len(got) != 1 {
		t.Fatalf("sightings = %d, want 1", len(got))
	}
	s := got[0]
	if s.ObserverPublicKeyHex != f.observer.String() || s.PathHashSize != 3 || hex.EncodeToString(s.Path) != "352c308bfcfc" ||
		*s.SNR != 5.5 || *s.RSSI != -90 || s.RouteType != meshcore.RouteTypeFlood {
		t.Fatalf("sighting = %+v", s)
	}
	if !f.companionState(t, ctx).Confirmed() {
		t.Fatal("companion not confirmed")
	}
}

func TestIngesterIgnoresWhatItShould(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	in := &Ingester{Store: st, Log: quietLog()}
	now := time.Now().Add(time.Second)
	ts := uint32(now.Unix()) //nolint:gosec // test clock
	good := observetest.AdvertPacket(t, f.companion, ts, "Owner T1", meshcore.RouteTypeFlood, 1, nil)

	// An unregistered observer, and a registered one heard on another org's
	// broker connection.
	in.Handle(ctx, packetMsg(f.org, observetest.NewIdentity(t), good, now))
	otherOrg, _ := st.CreateOrg(ctx, "Elsewhere", f.owner)
	in.Handle(ctx, packetMsg(otherOrg.ID, f.observer, good, now))

	// A tampered advert from the registered companion.
	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 1
	in.Handle(ctx, packetMsg(f.org, f.observer, bad, now))

	// An unregistered companion's advert, a non-advert, a foreign topic, junk.
	in.Handle(ctx, packetMsg(f.org, f.observer, observetest.AdvertPacket(t, observetest.NewIdentity(t), ts, "x", meshcore.RouteTypeFlood, 1, nil), now))
	in.Handle(ctx, packetMsg(f.org, f.observer, []byte{0x15, 0x00, 0xab, 0xcd}, now))
	in.Handle(ctx, Message{OrgID: f.org, Prefix: "meshcore", Topic: "other/thing", Payload: []byte("{}"), ReceivedAt: now})
	in.Handle(ctx, Message{OrgID: f.org, Prefix: "meshcore", Topic: packetMsg(f.org, f.observer, nil, now).Topic, Payload: []byte("not json"), ReceivedAt: now})

	if got := f.sightings(t, ctx); len(got) != 0 {
		t.Fatalf("sightings = %+v, want none", got)
	}
	if f.companionState(t, ctx).Confirmed() {
		t.Fatal("confirmed by an ignored message")
	}
}

func TestIngesterRejectsReplayedAdvert(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	in := &Ingester{Store: st, Log: quietLog()}

	// A genuine advert from an hour ago, rebroadcast now.
	now := time.Now().Add(time.Second)
	old := uint32(now.Add(-time.Hour).Unix()) //nolint:gosec // test clock
	in.Handle(ctx, packetMsg(f.org, f.observer, observetest.AdvertPacket(t, f.companion, old, "T1", meshcore.RouteTypeFlood, 1, nil), now))

	if got := f.sightings(t, ctx); len(got) != 0 {
		t.Fatalf("replay recorded: %+v", got)
	}
	c := f.companionState(t, ctx)
	if c.LastRejectedSkew == nil || *c.LastRejectedSkew < 59*time.Minute {
		t.Fatalf("rejection not recorded: %+v", c)
	}
}

func TestIngesterStatusAndLiveness(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	in := &Ingester{Store: st, Log: quietLog()}
	now := time.Now()
	topic := "meshcore/BUF/" + strings.ToUpper(f.observer.String()) + "/status"
	in.Handle(ctx, Message{OrgID: f.org, Prefix: "meshcore", Topic: topic, ReceivedAt: now,
		Payload: []byte(`{"status":"online","client_version":"meshcore-ha:3.0.3","stats":{"battery_mv":"4012"}}`)})
	obs, err := st.ListOrgObservers(ctx, f.org)
	if err != nil {
		t.Fatal(err)
	}
	if obs[0].LastSeenAt == nil || obs[0].ClientVersion != "meshcore-ha:3.0.3" || *obs[0].BatteryMV != 4012 {
		t.Fatalf("observer = %+v", obs[0])
	}
}

type fakeResolver map[string][]netip.Addr

func (r fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if a, ok := r[host]; ok {
		return a, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func TestPublicAddr(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]bool{
		"8.8.8.8":         true,
		"2606:4700::1111": true,
		"127.0.0.1":       false,
		"10.93.111.151":   false, // a cluster service IP
		"172.16.0.1":      false,
		"192.168.1.10":    false,
		"169.254.169.254": false, // cloud metadata
		"100.64.0.1":      false, // CGNAT
		"0.0.0.0":         false,
		"224.0.0.1":       false,
		"::1":             false,
		"fd00::1":         false,
		"fe80::1":         false,
		"::ffff:10.0.0.1": false, // IPv4-mapped private
		"64:ff9b::a00:1":  false, // NAT64 of 10.0.0.1
		"2002:a00:1::1":   false, // 6to4 of 10.0.0.1
	} {
		if got := PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("PublicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestResolveTarget(t *testing.T) {
	t.Parallel()
	r := fakeResolver{
		"mqtt.example.org": {netip.MustParseAddr("203.0.113.7")},
		"internal.example": {netip.MustParseAddr("10.0.0.5")},
		"mixed.example":    {netip.MustParseAddr("203.0.113.8"), netip.MustParseAddr("127.0.0.1")},
	}
	ctx := context.Background()

	got, err := ResolveTarget(ctx, "tls://mqtt.example.org", r, PublicAddr)
	if err != nil || got.DialURL != "tls://203.0.113.7:8883" || got.ServerName != "mqtt.example.org" || !got.TLS {
		t.Fatalf("tls = %+v, %v", got, err)
	}
	got, err = ResolveTarget(ctx, "tcp://mqtt.example.org:1884", r, PublicAddr)
	if err != nil || got.DialURL != "tcp://203.0.113.7:1884" {
		t.Fatalf("tcp = %+v, %v", got, err)
	}
	if got, err := ResolveTarget(ctx, "tcp://[2606:4700::1111]", r, PublicAddr); err != nil || got.DialURL != "tcp://[2606:4700::1111]:1883" {
		t.Fatalf("ipv6 literal = %+v, %v", got, err)
	}

	for _, raw := range []string{"tcp://internal.example", "tcp://mixed.example", "tcp://127.0.0.1:1883", "tcp://[::1]"} {
		if _, err := ResolveTarget(ctx, raw, r, PublicAddr); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: err = %v, want ErrBlockedAddress", raw, err)
		}
	}
	var dnsErr *DNSError
	if _, err := ResolveTarget(ctx, "tcp://nope.example", r, PublicAddr); !errors.As(err, &dnsErr) {
		t.Errorf("unknown host: err = %v", err)
	}
	for _, raw := range []string{"http://mqtt.example.org", "mqtt.example.org", "tcp://u:p@mqtt.example.org", "tcp://mqtt.example.org/x", "tcp://"} {
		if _, err := ResolveTarget(ctx, raw, r, PublicAddr); err == nil {
			t.Errorf("%s: want a URL error", raw)
		}
	}
}

func TestCategory(t *testing.T) {
	t.Parallel()
	refused := &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	for err, want := range map[error]string{
		&DNSError{Err: errors.New("x")}:                                                 CategoryDNS,
		fmt.Errorf("w: %w", ErrBlockedAddress):                                          CategoryBlocked,
		&mqtt.ReasonError{Packet: "CONNECT", Code: protocol.NotAuthorized}:              CategoryAuth,
		&mqtt.ReasonError{Packet: "CONNECT", Code: protocol.BadUserNameOrPassword}:      CategoryAuth,
		&mqtt.ReasonError{Packet: "CONNECT", Code: protocol.ServerUnavailable}:          CategoryRejected,
		fmt.Errorf("subscribe: %w", &mqtt.ReasonError{Packet: "SUBSCRIBE", Code: 0x80}): CategorySubscribe,
		refused:                       CategoryRefused,
		context.DeadlineExceeded:      CategoryTimeout,
		errors.New("connection lost"): CategoryConnect,
	} {
		if got := Category(err); got != want {
			t.Errorf("Category(%v) = %q, want %q", err, got, want)
		}
	}
}

// allowAll lets tests dial the fake broker on loopback.
func allowAll(netip.Addr) bool { return true }

var testMasterKey = [32]byte{1, 2, 3}

func startSupervisor(t *testing.T, st *store.Store, allow AddrPolicy) {
	t.Helper()
	sup := NewSupervisor(st, testMasterKey, quietLog())
	sup.Allow = allow
	sup.ReconcileEvery = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sup.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func saveBroker(t *testing.T, st *store.Store, ctx context.Context, f *fixture, url, password string, enabled bool) {
	t.Helper()
	sealed, err := secretbox.Seal(testMasterKey, []byte(password))
	if err != nil {
		t.Fatal(err)
	}
	b := store.OrgBroker{OrgID: f.org, URL: url, Username: "meshtender", TopicPrefix: "meshcore", Enabled: enabled}
	if err := st.UpsertOrgBroker(ctx, b, sealed, false, f.owner); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorEndToEnd(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	broker := newFakeBroker(t, "s3cret")
	saveBroker(t, st, ctx, f, broker.url(), "s3cret", true)
	startSupervisor(t, st, allowAll)

	eventually(t, "both subscriptions", func() bool {
		filters, _ := broker.snapshot()
		return len(filters) == 2
	})
	filters, users := broker.snapshot()
	if filters[0] != "meshcore/+/+/packets" || filters[1] != "meshcore/+/+/status" || users[0] != "meshtender" {
		t.Fatalf("filters=%v users=%v", filters, users)
	}
	eventually(t, "connected status", func() bool {
		b, _ := st.GetOrgBroker(ctx, f.org)
		return b.LastConnectedAt != nil && b.LastError == ""
	})

	now := time.Now().Add(time.Second)
	raw := observetest.AdvertPacket(t, f.companion, uint32(now.Unix()), "Owner T1", meshcore.RouteTypeFlood, 1, [][]byte{{0x42}}) //nolint:gosec // test clock
	m := packetMsg(f.org, f.observer, raw, now)
	broker.publish(m.Topic, m.Payload)
	eventually(t, "companion confirmed", func() bool { return f.companionState(t, ctx).Confirmed() })

	// Disabling the broker disconnects it on the next reconcile.
	saveBroker(t, st, ctx, f, broker.url(), "s3cret", false)
	eventually(t, "disconnect", func() bool { return broker.live() == 0 })
}

func TestSupervisorReportsAuthFailure(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	broker := newFakeBroker(t, "right")
	saveBroker(t, st, ctx, f, broker.url(), "wrong", true)
	startSupervisor(t, st, allowAll)
	eventually(t, "auth category", func() bool {
		b, _ := st.GetOrgBroker(ctx, f.org)
		return b.LastError == CategoryAuth
	})
}

func TestSupervisorRefusesPrivateBroker(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	broker := newFakeBroker(t, "s3cret")
	saveBroker(t, st, ctx, f, broker.url(), "s3cret", true)
	startSupervisor(t, st, PublicAddr) // production policy: loopback is refused

	eventually(t, "blocked category", func() bool {
		b, _ := st.GetOrgBroker(ctx, f.org)
		return b.LastError == CategoryBlocked
	})
	if _, users := broker.snapshot(); len(users) != 0 {
		t.Fatalf("connected to a loopback broker: %v", users)
	}
}
