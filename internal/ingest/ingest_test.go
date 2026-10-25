package ingest

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/eclipse/paho.mqtt.golang/packets"
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

	for raw, want := range map[string]Target{
		"tls://mqtt.example.org":      {Scheme: "tls", Addr: "203.0.113.7:8883", ServerName: "mqtt.example.org", Port: "8883"},
		"tcp://mqtt.example.org:1884": {Scheme: "tcp", Addr: "203.0.113.7:1884", ServerName: "mqtt.example.org", Port: "1884"},
		"tcp://[2606:4700::1111]":     {Scheme: "tcp", Addr: "[2606:4700::1111]:1883", ServerName: "2606:4700::1111", Port: "1883"},
		// WebSocket: default port per scheme, path defaults to /mqtt.
		"wss://mqtt.example.org":           {Scheme: "wss", Addr: "203.0.113.7:443", ServerName: "mqtt.example.org", Port: "443", Path: "/mqtt"},
		"wss://mqtt.example.org/":          {Scheme: "wss", Addr: "203.0.113.7:443", ServerName: "mqtt.example.org", Port: "443", Path: "/mqtt"},
		"ws://mqtt.example.org:8080/ws/mq": {Scheme: "ws", Addr: "203.0.113.7:8080", ServerName: "mqtt.example.org", Port: "8080", Path: "/ws/mq"},
	} {
		got, err := ResolveTarget(ctx, raw, r, PublicAddr)
		if err != nil || got != want {
			t.Errorf("%s = %+v, %v; want %+v", raw, got, err, want)
		}
	}

	for _, raw := range []string{"tcp://internal.example", "tcp://mixed.example", "tcp://127.0.0.1:1883", "tcp://[::1]", "wss://internal.example/mqtt"} {
		if _, err := ResolveTarget(ctx, raw, r, PublicAddr); !errors.Is(err, ErrBlockedAddress) {
			t.Errorf("%s: err = %v, want ErrBlockedAddress", raw, err)
		}
	}
	var dnsErr *DNSError
	if _, err := ResolveTarget(ctx, "tcp://nope.example", r, PublicAddr); !errors.As(err, &dnsErr) {
		t.Errorf("unknown host: err = %v", err)
	}
	for _, raw := range []string{"http://mqtt.example.org", "https://mqtt.example.org", "mqtt.example.org", "tcp://u:p@mqtt.example.org",
		"wss://u:p@mqtt.example.org", "tcp://mqtt.example.org/x", "wss://mqtt.example.org/mqtt?x=1", "tcp://"} {
		if _, err := ResolveTarget(ctx, raw, r, PublicAddr); err == nil {
			t.Errorf("%s: want a URL error", raw)
		}
	}
}

func TestCategory(t *testing.T) {
	t.Parallel()
	refused := &net.OpError{Op: "dial", Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	for err, want := range map[error]string{
		&DNSError{Err: errors.New("x")}:        CategoryDNS,
		fmt.Errorf("w: %w", ErrBlockedAddress): CategoryBlocked,
		// Paho wraps a failed dial as "<network error> : <cause>".
		fmt.Errorf("%w : %w", packets.ErrorNetworkError, ErrBlockedAddress): CategoryBlocked,
		packets.ErrorRefusedNotAuthorised:                                   CategoryAuth,
		packets.ErrorRefusedBadUsernameOrPassword:                           CategoryAuth,
		packets.ErrorRefusedServerUnavailable:                               CategoryRejected,
		&HandshakeError{Status: 404, Err: errors.New("x")}:                  CategoryRejected,
		fmt.Errorf("%w: meshcore/+/+/packets", ErrSubscribeRejected):        CategorySubscribe,
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

// testDialer reaches the fake broker: loopback allowed only when allow is
// allowAll, and its test CA trusted for wss.
func testDialer(b *fakeBroker, allow AddrPolicy) Dialer {
	return Dialer{Resolver: net.DefaultResolver, Allow: allow, RootCAs: b.rootCAs}
}

func startSupervisor(t *testing.T, st *store.Store, d Dialer) {
	t.Helper()
	sup := NewSupervisor(st, testMasterKey, quietLog())
	sup.Dialer = d
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

// TestSupervisorEndToEnd runs the whole path — connect, subscribe, receive an
// advert, record it, disconnect when disabled — over each transport. wss is the
// one real deployments use (MQTT over WebSocket behind HTTPS).
func TestSupervisorEndToEnd(t *testing.T) {
	t.Parallel()
	for _, transport := range []string{"tcp", "ws", "wss"} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			st, ctx := newStore(t)
			f := newFixture(t, st, ctx)
			broker := newFakeBroker(t, "s3cret", transport)
			saveBroker(t, st, ctx, f, broker.url(), "s3cret", true)
			startSupervisor(t, st, testDialer(broker, allowAll))

			eventually(t, "both subscriptions", func() bool {
				filters, _, _ := broker.snapshot()
				return len(filters) == 2
			})
			filters, users, paths := broker.snapshot()
			slices.Sort(filters) // one SUBSCRIBE; Paho orders the filters as it likes
			if filters[0] != "meshcore/+/+/packets" || filters[1] != "meshcore/+/+/status" || users[0] != "meshtender" {
				t.Fatalf("filters=%v users=%v", filters, users)
			}
			if transport != "tcp" && (len(paths) == 0 || paths[0] != "/mqtt") {
				t.Errorf("WebSocket paths = %v, want the /mqtt default", paths)
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
		})
	}
}

func TestSupervisorReportsAuthFailure(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	broker := newFakeBroker(t, "right", "wss")
	saveBroker(t, st, ctx, f, broker.url(), "wrong", true)
	startSupervisor(t, st, testDialer(broker, allowAll))
	eventually(t, "auth category", func() bool {
		b, _ := st.GetOrgBroker(ctx, f.org)
		return b.LastError == CategoryAuth
	})
}

func TestSupervisorRefusesPrivateBroker(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	broker := newFakeBroker(t, "s3cret", "wss")
	saveBroker(t, st, ctx, f, broker.url(), "s3cret", true)
	startSupervisor(t, st, testDialer(broker, PublicAddr)) // production policy: loopback is refused

	eventually(t, "blocked category", func() bool {
		b, _ := st.GetOrgBroker(ctx, f.org)
		return b.LastError == CategoryBlocked
	})
	if _, users, _ := broker.snapshot(); len(users) != 0 {
		t.Fatalf("connected to a loopback broker: %v", users)
	}
}

// TestSupervisorReportsRejectedSubscription: an MQTT 3.1.1 broker refuses a
// subscription inside a successful SUBACK (code 0x80), typically an ACL on the
// topic prefix. That must surface as "subscribe", not as a healthy connection
// that never hears anything.
func TestSupervisorReportsRejectedSubscription(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	broker := newFakeBroker(t, "s3cret", "ws")
	broker.rejectSubs = true
	saveBroker(t, st, ctx, f, broker.url(), "s3cret", true)
	startSupervisor(t, st, testDialer(broker, allowAll))
	eventually(t, "subscribe category", func() bool {
		b, _ := st.GetOrgBroker(ctx, f.org)
		return b.LastError == CategorySubscribe
	})
}

// TestDialerVerifiesTLSAgainstEnteredHost: the dial goes to the vetted IP, but
// the certificate must still be checked. A wss broker whose certificate the
// dialer doesn't trust is refused as a TLS failure, not silently accepted.
func TestDialerVerifiesTLSAgainstEnteredHost(t *testing.T) {
	t.Parallel()
	broker := newFakeBroker(t, "s3cret", "wss")
	untrusting := Dialer{Resolver: net.DefaultResolver, Allow: allowAll} // system roots only
	_, err := untrusting.Connect(BrokerConfig{OrgID: 1, URL: broker.url(), Username: "meshtender", Password: "s3cret"}, nil)
	if Category(err) != CategoryTLS {
		t.Fatalf("err = %v (category %q), want a TLS failure", err, Category(err))
	}
	client, err := testDialer(broker, allowAll).Connect(BrokerConfig{OrgID: 1, URL: broker.url(), Username: "meshtender", Password: "s3cret"}, nil)
	if err != nil {
		t.Fatalf("trusted CA: %v", err)
	}
	client.Disconnect(0)
}

// TestDialerWebSocketWrongPath: an HTTPS endpoint that doesn't upgrade the
// path is reported as the broker rejecting the connection.
func TestDialerWebSocketWrongPath(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	d := Dialer{Resolver: net.DefaultResolver, Allow: allowAll}
	_, err := d.Connect(BrokerConfig{OrgID: 1, URL: "ws://" + srv.Listener.Addr().String() + "/nope"}, nil)
	if Category(err) != CategoryRejected {
		t.Fatalf("err = %v (category %q), want rejected", err, Category(err))
	}
}
