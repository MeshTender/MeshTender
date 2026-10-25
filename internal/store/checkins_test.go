package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/observe"
)

// Tests for the companion check-in store: brokers, observers, companions,
// sightings, and nets.

// testKey returns a distinct, valid-looking 64-hex-char public key.
func testKey(n int) string {
	return fmt.Sprintf("%02x", n) + strings.Repeat("ab", 31)
}

func mustUser(t *testing.T, st *Store, ctx context.Context, name string) int64 {
	t.Helper()
	u, err := st.CreateUser(ctx, name, "")
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

func mustOrg(t *testing.T, st *Store, ctx context.Context, name string, owner int64) int64 {
	t.Helper()
	o, err := st.CreateOrg(ctx, name, owner)
	if err != nil {
		t.Fatal(err)
	}
	return o.ID
}

func mustCompanion(t *testing.T, st *Store, ctx context.Context, owner int64, name string, key int) *Companion {
	t.Helper()
	c, err := st.CreateCompanion(ctx, owner, name, testKey(key))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// heard records one copy of an advert and fails the test on error. The advert
// timestamp defaults to the receive time, the observer to a fixed key.
func heard(t *testing.T, st *Store, ctx context.Context, h AdvertHeard) bool {
	t.Helper()
	if h.AdvertTS == 0 {
		h.AdvertTS = uint32(h.ReceivedAt.Unix()) //nolint:gosec // test clock
	}
	if h.ObserverPublicKeyHex == "" {
		h.ObserverPublicKeyHex = testKey(200)
	}
	created, err := st.RecordAdvertHeard(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func TestOrgBrokerUpsertKeepsAndClearsPassword(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "brokeradmin")
	org := mustOrg(t, st, ctx, "BrokerOrg", admin)

	if _, err := st.GetOrgBroker(ctx, org); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before: err = %v, want ErrNotFound", err)
	}
	b := OrgBroker{OrgID: org, URL: "tcp://mqtt.example.org:1883", Username: "u", TopicPrefix: "meshcore", Enabled: true}
	if err := st.UpsertOrgBroker(ctx, b, []byte("sealed-1"), false, admin); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := st.RecordBrokerStatus(ctx, org, &now, nil, "auth"); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetOrgBroker(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.PasswordSealed) != "sealed-1" || got.LastConnectedAt == nil || got.LastError != "auth" {
		t.Fatalf("after insert+status: %+v", got)
	}

	// A blank password on edit keeps the stored one; saving resets status.
	b.URL = "tls://mqtt.example.org:8883"
	if err := st.UpsertOrgBroker(ctx, b, nil, false, admin); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetOrgBroker(ctx, org)
	if string(got.PasswordSealed) != "sealed-1" || got.URL != b.URL || got.LastConnectedAt != nil || got.LastError != "" {
		t.Fatalf("after keep: %+v", got)
	}

	if err := st.UpsertOrgBroker(ctx, b, nil, true, admin); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetOrgBroker(ctx, org)
	if got.HasPassword() {
		t.Fatal("clearPassword left a password")
	}

	list, err := st.ListEnabledBrokers(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("enabled = %v, %v", list, err)
	}
	b.Enabled = false
	_ = st.UpsertOrgBroker(ctx, b, nil, false, admin)
	if list, _ := st.ListEnabledBrokers(ctx); len(list) != 0 {
		t.Fatalf("disabled broker listed: %+v", list)
	}
}

func TestOrgObservers(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "obsadmin")
	org := mustOrg(t, st, ctx, "ObsOrg", admin)
	other := mustOrg(t, st, ctx, "OtherObsOrg", admin)

	if err := st.CreateOrgObserver(ctx, org, "Roof", testKey(1), admin); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateOrgObserver(ctx, org, "Dup", testKey(1), admin); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("dup: err = %v", err)
	}
	// Another org may register the same physical observer.
	if err := st.CreateOrgObserver(ctx, other, "Shared", testKey(1), admin); err != nil {
		t.Fatal(err)
	}

	if ok, _ := st.IsOrgObserver(ctx, org, testKey(1)); !ok {
		t.Fatal("registered observer not recognized")
	}
	if ok, _ := st.IsOrgObserver(ctx, org, testKey(2)); ok {
		t.Fatal("unregistered observer recognized")
	}

	t1 := time.Now().Add(-time.Minute).Truncate(time.Microsecond)
	mv, nf := 3999, -110
	if err := st.TouchObserver(ctx, org, testKey(1), t1, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchObserver(ctx, org, testKey(1), t1.Add(-time.Hour), &observe.Status{ClientVersion: "ha:2.9", BatteryMV: &mv, NoiseFloor: &nf}); err != nil {
		t.Fatal(err)
	}
	obs, err := st.ListOrgObservers(ctx, org)
	if err != nil || len(obs) != 1 {
		t.Fatalf("list = %v, %v", obs, err)
	}
	o := obs[0]
	// last_seen_at never moves backwards; status fields are stored.
	if o.LastSeenAt == nil || !o.LastSeenAt.Equal(t1) || o.ClientVersion != "ha:2.9" || *o.BatteryMV != 3999 || *o.NoiseFloor != -110 {
		t.Fatalf("observer = %+v", o)
	}
	// The other org's registration is untouched.
	if obs, _ := st.ListOrgObservers(ctx, other); obs[0].LastSeenAt != nil {
		t.Fatal("touch leaked across orgs")
	}

	if err := st.DeleteOrgObserver(ctx, other, o.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org delete: err = %v", err)
	}
	if err := st.DeleteOrgObserver(ctx, org, o.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCompanionRegistrationAndVisibility(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	alice := mustUser(t, st, ctx, "alice")
	bob := mustUser(t, st, ctx, "bob")
	eve := mustUser(t, st, ctx, "eve")
	org := mustOrg(t, st, ctx, "VisOrg", alice)
	if err := st.AddOrgMember(ctx, org, bob, "member"); err != nil {
		t.Fatal(err)
	}

	c := mustCompanion(t, st, ctx, alice, "Alice T1", 1)
	if c.Confirmed() || c.OwnerName != "alice" {
		t.Fatalf("new companion = %+v", c)
	}
	// A key is one person's, across all users.
	if _, err := st.CreateCompanion(ctx, eve, "Not mine", testKey(1)); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("dup key: err = %v", err)
	}

	for _, v := range []struct {
		name    string
		viewer  int64
		visible bool
	}{{"owner", alice, true}, {"org-mate", bob, true}, {"outsider", eve, false}} {
		_, err := st.GetCompanionForViewer(ctx, c.PublicID, v.viewer)
		if v.visible && err != nil || !v.visible && !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v", v.name, err)
		}
	}

	if err := st.DeleteCompanionOwned(ctx, bob, c.PublicID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner delete: err = %v", err)
	}
	if err := st.DeleteCompanionOwned(ctx, alice, c.PublicID); err != nil {
		t.Fatal(err)
	}
}

func TestCompanionLimit(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u := mustUser(t, st, ctx, "collector")
	for i := range MaxCompanionsPerUser {
		mustCompanion(t, st, ctx, u, fmt.Sprintf("c%d", i), i)
	}
	if _, err := st.CreateCompanion(ctx, u, "one more", testKey(99)); !errors.Is(err, ErrCompanionLimit) {
		t.Fatalf("err = %v, want ErrCompanionLimit", err)
	}
	if list, _ := st.ListCompanionsForUser(ctx, u); len(list) != MaxCompanionsPerUser {
		t.Fatalf("list = %d", len(list))
	}
}

// TestRecordAdvertHeardCountsWithoutPaths: copies of one advert collapse into
// one check-in whose counts are exact over distinct observers and repeaters,
// whatever the duplicates; the sets behind them go after AdvertSetTTL without
// the counts shrinking.
func TestRecordAdvertHeardCountsWithoutPaths(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u := mustUser(t, st, ctx, "sighter")
	c := mustCompanion(t, st, ctx, u, "T1", 1)

	// Heard before registration (an advert that was in flight): recorded, but
	// it doesn't confirm.
	early := c.CreatedAt.Add(-time.Minute)
	if !heard(t, st, ctx, AdvertHeard{CompanionID: c.ID, ReceivedAt: early, ZeroHop: true}) {
		t.Fatal("first copy didn't create a check-in")
	}
	got, _ := st.GetCompanionForViewer(ctx, c.PublicID, u)
	if got.Confirmed() || got.LastHeardAt == nil {
		t.Fatalf("after early advert: %+v", got)
	}

	at := c.CreatedAt.Add(time.Minute)
	ts := uint32(at.Unix()) //nolint:gosec // test clock
	first := AdvertHeard{CompanionID: c.ID, AdvertTS: ts, ReceivedAt: at, ObserverPublicKeyHex: testKey(100),
		Relays: [][]byte{{0x35, 0x2c, 0x30}, {0x8b, 0xfc, 0xfc}}}
	if !heard(t, st, ctx, first) {
		t.Fatal("new advert didn't create a check-in")
	}
	if heard(t, st, ctx, first) { // uplink duplicate
		t.Fatal("a duplicate copy created a second check-in")
	}
	// Another observer, another route sharing one repeater.
	heard(t, st, ctx, AdvertHeard{CompanionID: c.ID, AdvertTS: ts, ReceivedAt: at.Add(time.Second), ObserverPublicKeyHex: testKey(101),
		Relays: [][]byte{{0x8b, 0xfc, 0xfc}, {0x99, 0x99, 0x99}}})

	got, _ = st.GetCompanionForViewer(ctx, c.PublicID, u)
	if !got.Confirmed() || !got.LastHeardAt.Equal(at.Add(time.Second).Truncate(time.Microsecond)) {
		t.Fatalf("after advert: %+v", got)
	}
	if got.LastAdvertAt == nil || got.LastAdvertAt.Unix() != at.Unix() {
		t.Fatalf("last advert = %v", got.LastAdvertAt)
	}

	page, more, err := st.ListCompanionCheckinsPage(ctx, c.ID, nil)
	if err != nil || more || len(page) != 2 {
		t.Fatalf("check-ins = %+v, more=%v, err=%v", page, more, err)
	}
	k := page[0] // newest first
	if k.Observers != 2 || k.Spread != 3 || k.ZeroHop || !k.FirstHeardAt.Equal(at.Truncate(time.Microsecond)) {
		t.Fatalf("check-in = %+v; want 2 observers, 3 distinct repeaters", k)
	}
	if !page[1].ZeroHop || page[1].Spread != 0 {
		t.Fatalf("zero-hop check-in = %+v", page[1])
	}

	// Once the advert is older than AdvertSetTTL its sets are swept, and a
	// straggling copy afterwards can't shrink the counts.
	if _, err := st.pool.Exec(ctx, `UPDATE advert_checkins SET first_heard_at = first_heard_at - $2::interval WHERE id = $1`,
		k.ID, (AdvertSetTTL + time.Minute).String()); err != nil {
		t.Fatal(err)
	}
	if n, err := st.PruneAdvertSets(ctx); err != nil || n != 5 { // 3 relays + 2 receivers
		t.Fatalf("pruned = %d, %v", n, err)
	}
	heard(t, st, ctx, AdvertHeard{CompanionID: c.ID, AdvertTS: ts, ReceivedAt: at.Add(time.Minute), ObserverPublicKeyHex: testKey(102),
		Relays: [][]byte{{0x01, 0x02, 0x03}}})
	page, _, _ = st.ListCompanionCheckinsPage(ctx, c.ID, nil)
	i := slices.IndexFunc(page, func(x Checkin) bool { return x.ID == k.ID })
	if i < 0 || page[i].Observers != 2 || page[i].Spread != 3 {
		t.Fatalf("after sweep + late copy: %+v; counts must not shrink", page)
	}

	if err := st.RecordAdvertRejected(ctx, c.ID, at, 50*365*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetCompanionForViewer(ctx, c.PublicID, u)
	if got.LastRejectedSkew == nil || *got.LastRejectedSkew <= 0 {
		t.Fatalf("rejected skew = %v (want clamped, positive)", got.LastRejectedSkew)
	}
}

func TestCompanionForKeyAndPruneUnconfirmed(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u := mustUser(t, st, ctx, "pruner")
	stale := mustCompanion(t, st, ctx, u, "stale", 1)
	fresh := mustCompanion(t, st, ctx, u, "fresh", 2)
	confirmed := mustCompanion(t, st, ctx, u, "confirmed", 3)
	heard(t, st, ctx, AdvertHeard{CompanionID: confirmed.ID, ReceivedAt: confirmed.CreatedAt.Add(time.Second)})

	if id, err := st.CompanionIDForKey(ctx, testKey(1)); err != nil || id != stale.ID {
		t.Fatalf("for key = %d, %v", id, err)
	}
	if _, err := st.CompanionIDForKey(ctx, testKey(9)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown key: err = %v", err)
	}

	old := time.Now().Add(-UnconfirmedCompanionTTL - time.Hour)
	for _, id := range []int64{stale.ID, confirmed.ID} {
		if _, err := st.pool.Exec(ctx, `UPDATE companions SET created_at = $2 WHERE id = $1`, id, old); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.PruneUnconfirmedCompanions(ctx)
	if err != nil || n != 1 {
		t.Fatalf("pruned = %d, %v", n, err)
	}
	list, _ := st.ListCompanionsForUser(ctx, u)
	if len(list) != 2 || list[0].ID != confirmed.ID || list[1].ID != fresh.ID {
		t.Fatalf("remaining = %+v", list)
	}
}

func TestOrgCompanionActivityPage(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "actadmin")
	outsider := mustUser(t, st, ctx, "actoutsider")
	org := mustOrg(t, st, ctx, "ActOrg", admin)

	for i := range OrgCompanionActivityPageSize + 1 {
		owner := admin
		if i >= MaxCompanionsPerUser {
			owner = mustUser(t, st, ctx, fmt.Sprintf("member%02d", i))
			if err := st.AddOrgMember(ctx, org, owner, "member"); err != nil {
				t.Fatal(err)
			}
		}
		c := mustCompanion(t, st, ctx, owner, fmt.Sprintf("comp %02d", i), i)
		// Heard after registration, so it confirms. Offset from the row's own
		// created_at (the database's clock), not time.Now(): the two clocks can
		// differ by milliseconds, which reads as heard before registration.
		heard(t, st, ctx, AdvertHeard{CompanionID: c.ID, ReceivedAt: c.CreatedAt.Add(time.Second)})
	}
	// Unconfirmed, and a non-member's: neither listed.
	pending := mustUser(t, st, ctx, "actpending")
	_ = st.AddOrgMember(ctx, org, pending, "member")
	mustCompanion(t, st, ctx, pending, "zz unconfirmed", 90)
	oc := mustCompanion(t, st, ctx, outsider, "outsider", 91)
	heard(t, st, ctx, AdvertHeard{CompanionID: oc.ID, ReceivedAt: oc.CreatedAt.Add(time.Second)})

	page, more, err := st.ListOrgCompanionActivityPage(ctx, org, nil)
	if err != nil || !more || len(page) != OrgCompanionActivityPageSize {
		t.Fatalf("page 1 = %d, more=%v, err=%v", len(page), more, err)
	}
	if page[0].Name != "comp 00" || !page[0].Confirmed() {
		t.Fatalf("first row = %+v", page[0])
	}
	last := page[len(page)-1]
	page, more, err = st.ListOrgCompanionActivityPage(ctx, org, &ActivityCursor{Name: last.Name, ID: last.ID})
	if err != nil || more || len(page) != 1 || page[0].Name != fmt.Sprintf("comp %02d", OrgCompanionActivityPageSize) {
		t.Fatalf("page 2 = %+v, more=%v, err=%v", page, more, err)
	}
}

// TestAdvertSetsKeepNoOrderAndNoHistory pins the privacy shape of the relay and
// receiver sets: unlogged (never replicated or backed up), and nothing but the
// check-in and the member of the set. A timestamp or sequence column would
// record which repeater a copy came through first — the order this design
// exists not to keep.
func TestAdvertSetsKeepNoOrderAndNoHistory(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	for table, want := range map[string]string{
		"advert_relays":    "checkin_id,hash",
		"advert_receivers": "checkin_id,observer_public_key_hex",
	} {
		var persistence string
		if err := st.pool.QueryRow(ctx, `SELECT relpersistence FROM pg_class WHERE relname = $1`, table).Scan(&persistence); err != nil {
			t.Fatal(err)
		}
		if persistence != "u" {
			t.Errorf("%s is not UNLOGGED (relpersistence %q)", table, persistence)
		}
		var cols string
		if err := st.pool.QueryRow(ctx, `
			SELECT string_agg(column_name, ',' ORDER BY column_name) FROM information_schema.columns
			WHERE table_name = $1`, table).Scan(&cols); err != nil {
			t.Fatal(err)
		}
		if cols != want {
			t.Errorf("%s columns = %s, want only %s", table, cols, want)
		}
	}
	// And the paths themselves are gone.
	var exists bool
	if err := st.pool.QueryRow(ctx, `SELECT to_regclass('advert_sightings') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("advert_sightings still exists (%v)", err)
	}
}

func TestNetOccurrencesAcrossDST(t *testing.T) {
	t.Parallel()
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// Sunday 19:00 for an hour. DST ends Sunday 2026-11-01 at 02:00 local, so the
	// session that evening is EST: 19:00 local = 00:00 UTC Monday.
	from := time.Date(2026, 10, 30, 12, 0, 0, 0, ny)
	got := NetOccurrences(time.Sunday, 19*60, 60, ny, from, 15*24*time.Hour)
	want := []time.Time{
		time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, w := range got {
		if !w.Start.Equal(want[i]) || w.End.Sub(w.Start) != time.Hour {
			t.Errorf("window %d = %v–%v, want start %v", i, w.Start.UTC(), w.End.UTC(), want[i])
		}
	}
	// The week before is EDT: 19:00 local = 23:00 UTC.
	before := NetOccurrences(time.Sunday, 19*60, 60, ny, time.Date(2026, 10, 24, 0, 0, 0, 0, ny), 2*24*time.Hour)
	if len(before) != 1 || !before[0].Start.Equal(time.Date(2026, 10, 25, 23, 0, 0, 0, time.UTC)) {
		t.Fatalf("EDT week = %v", before)
	}

	// A session already in progress at `from` is included.
	mid := time.Date(2026, 11, 1, 19, 30, 0, 0, ny)
	if got := NetOccurrences(time.Sunday, 19*60, 60, ny, mid, time.Hour); len(got) != 1 || !got[0].Start.Equal(want[0]) {
		t.Fatalf("in progress = %v", got)
	}
	// One that already ended is not.
	if got := NetOccurrences(time.Sunday, 19*60, 60, ny, mid.Add(time.Hour), time.Hour); len(got) != 0 {
		t.Fatalf("ended = %v", got)
	}

	// 02:30 on spring-forward day doesn't exist locally. time.Date picks a real
	// instant for it (Go doesn't promise which side of the gap); what matters is
	// that the session still happens that day rather than vanishing.
	spring := NetOccurrences(time.Sunday, 2*60+30, 30, ny, time.Date(2026, 3, 7, 0, 0, 0, 0, ny), 2*24*time.Hour)
	if len(spring) != 1 || spring[0].Start.In(ny).Day() != 8 || spring[0].End.Sub(spring[0].Start) != 30*time.Minute {
		t.Fatalf("spring-forward = %v", spring)
	}
}

func testNetInput() NetInput {
	return NetInput{Name: "Weekly Net", Weekday: time.Sunday, StartMinute: 19 * 60, DurationMinutes: 60,
		Timezone: "America/New_York", Active: true}
}

func TestNetCRUDAndSessions(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "netadmin")
	org := mustOrg(t, st, ctx, "NetOrg", admin)
	other := mustOrg(t, st, ctx, "NetOther", admin)
	ny, _ := time.LoadLocation("America/New_York")
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, ny) // Friday

	net, err := st.CreateNet(ctx, org, testNetInput(), admin, now)
	if err != nil {
		t.Fatal(err)
	}
	next, err := st.NextNetSession(ctx, net.ID, now)
	if err != nil || !next.StartsAt.Equal(time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if sums, err := st.ListOrgNetSummaries(ctx, org, now); err != nil || len(sums) != 1 || sums[0].Next == nil || sums[0].Next.ID != next.ID {
		t.Fatalf("summaries = %+v, %v", sums, err)
	}
	if _, err := st.GetNet(ctx, other, net.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org get: %v", err)
	}

	// Re-ensuring is idempotent.
	if err := st.EnsureUpcomingNetSessions(ctx, now); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = st.pool.QueryRow(ctx, `SELECT count(*) FROM net_sessions WHERE net_id = $1`, net.ID).Scan(&n)
	if n != 1 {
		t.Fatalf("sessions = %d, want 1", n)
	}

	// Run the session, then move the net to Monday: the past session stays,
	// the next one moves.
	later := time.Date(2026, 11, 3, 12, 0, 0, 0, ny)
	if err := st.EnsureUpcomingNetSessions(ctx, later); err != nil {
		t.Fatal(err)
	}
	in := testNetInput()
	in.Weekday = time.Monday
	if err := st.UpdateNet(ctx, org, net.ID, in, later); err != nil {
		t.Fatal(err)
	}
	next, _ = st.NextNetSession(ctx, net.ID, later)
	if next.StartsAt.In(ny).Weekday() != time.Monday {
		t.Fatalf("next after edit = %v", next.StartsAt.In(ny))
	}
	past, more, err := st.ListNetSessionsPage(ctx, org, net.ID, later, nil)
	if err != nil || more || len(past) != 1 || past[0].StartsAt.In(ny).Weekday() != time.Sunday {
		t.Fatalf("past = %+v, %v", past, err)
	}

	if err := st.UpdateNet(ctx, other, net.ID, in, later); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org update: %v", err)
	}
	if err := st.DeleteNet(ctx, other, net.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-org delete: %v", err)
	}
	if err := st.DeleteNet(ctx, org, net.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNetLimit(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "netlimit")
	org := mustOrg(t, st, ctx, "NetLimitOrg", admin)
	in := testNetInput()
	in.Active = false
	for range MaxNetsPerOrg {
		if _, err := st.CreateNet(ctx, org, in, admin, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CreateNet(ctx, org, in, admin, time.Now()); !errors.Is(err, ErrNetLimit) {
		t.Fatalf("err = %v, want ErrNetLimit", err)
	}
	nets, _ := st.ListOrgNetSummaries(ctx, org, time.Now())
	if len(nets) != MaxNetsPerOrg {
		t.Fatalf("nets = %d", len(nets))
	}
	if nets[0].Next != nil {
		t.Fatalf("inactive net has a next session: %+v", nets[0].Next)
	}
}

// TestNetSessionRoster: the roster is by member, not by device. A member
// checks in with whichever companion they have, so each member appears once,
// with their earliest check-in (and the companion it came from) and the best
// counts across all of them.
func TestNetSessionRoster(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "rosteradmin")
	member := mustUser(t, st, ctx, "rostermember")
	quiet := mustUser(t, st, ctx, "rosterquiet")
	pending := mustUser(t, st, ctx, "rosterpending")
	outsider := mustUser(t, st, ctx, "rosteroutsider")
	org := mustOrg(t, st, ctx, "RosterOrg", admin)
	for _, u := range []int64{member, quiet, pending} {
		if err := st.AddOrgMember(ctx, org, u, "member"); err != nil {
			t.Fatal(err)
		}
	}
	ny, _ := time.LoadLocation("America/New_York")
	before := time.Date(2026, 10, 30, 12, 0, 0, 0, ny)
	net, err := st.CreateNet(ctx, org, testNetInput(), admin, before)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.NextNetSession(ctx, net.ID, before)
	if err != nil {
		t.Fatal(err)
	}

	// Registered (and confirmed) before the net.
	confirmed := func(owner int64, name string, key int) *Companion {
		c := mustCompanion(t, st, ctx, owner, name, key)
		if _, err := st.pool.Exec(ctx, `UPDATE companions SET created_at = $2 WHERE id = $1`, c.ID, before.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		heard(t, st, ctx, AdvertHeard{CompanionID: c.ID, ReceivedAt: before})
		return c
	}
	zero := confirmed(admin, "ZeroHop", 1)
	late := confirmed(admin, "Late", 2)
	flood := confirmed(member, "Flood", 3)
	spare := confirmed(member, "Spare", 4)
	confirmed(quiet, "Quiet", 5)
	mustCompanion(t, st, ctx, pending, "Unconfirmed", 6) // never heard: not on the roster
	foreign := confirmed(outsider, "Foreign", 7)

	at := sess.StartsAt.Add(10 * time.Minute)
	// The member's main device: one advert heard by two observers, repeaters
	// 1, 2, 3 on one route, 9 on another, and a third route through 1 and 9
	// again. Each repeater rebroadcasts a flood once, so the spread is the 4
	// distinct repeaters, not the 6 path entries.
	ts := uint32(at.Unix()) //nolint:gosec // test clock
	heard(t, st, ctx, AdvertHeard{CompanionID: flood.ID, AdvertTS: ts, ReceivedAt: at, ObserverPublicKeyHex: testKey(100),
		Relays: [][]byte{{1}, {2}, {3}}})
	heard(t, st, ctx, AdvertHeard{CompanionID: flood.ID, AdvertTS: ts, ReceivedAt: at.Add(time.Second), ObserverPublicKeyHex: testKey(101),
		Relays: [][]byte{{9}}})
	heard(t, st, ctx, AdvertHeard{CompanionID: flood.ID, AdvertTS: ts, ReceivedAt: at.Add(2 * time.Second), ObserverPublicKeyHex: testKey(101),
		Relays: [][]byte{{1}, {9}}})
	// Their spare device later in the session: still one row, and the best
	// counts win.
	heard(t, st, ctx, AdvertHeard{CompanionID: spare.ID, ReceivedAt: at.Add(5 * time.Minute), Relays: [][]byte{{7}}})
	// The admin: zero-hop, earlier than the member; their other device only at
	// the very end, outside the half-open window.
	heard(t, st, ctx, AdvertHeard{CompanionID: zero.ID, ReceivedAt: at.Add(-time.Minute), ZeroHop: true})
	heard(t, st, ctx, AdvertHeard{CompanionID: late.ID, ReceivedAt: sess.EndsAt})
	heard(t, st, ctx, AdvertHeard{CompanionID: foreign.ID, ReceivedAt: at})

	roster, err := st.NetSessionRoster(ctx, org, sess)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range roster {
		names = append(names, e.MemberName)
	}
	if got := strings.Join(names, ","); got != "rosteradmin,rostermember,rosterquiet" {
		t.Fatalf("roster = %s; want one row per member with a confirmed companion, checked-in first", got)
	}
	a, m, q := roster[0], roster[1], roster[2]
	if !a.CheckedIn() || a.CompanionName != "ZeroHop" || !a.ZeroHopOnly || a.Observers != 1 || a.Spread != 0 {
		t.Errorf("admin = %+v", a)
	}
	if !m.CheckedIn() || m.CompanionName != "Flood" || m.CompanionPublicID != flood.PublicID || m.ZeroHopOnly ||
		m.Observers != 2 || m.Spread != 4 {
		t.Errorf("member = %+v; want checked in on Flood (first), best counts across both devices", m)
	}
	if q.CheckedIn() || q.CompanionName != "" {
		t.Errorf("quiet member = %+v", q)
	}

	sessions, _, err := st.ListNetSessionsPage(ctx, org, net.ID, sess.EndsAt, nil)
	if err != nil || len(sessions) != 1 || sessions[0].CheckedIn != 2 {
		t.Fatalf("sessions = %+v, %v; want 2 members checked in (not 3 companions)", sessions, err)
	}
}

func TestPruneCheckins(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u := mustUser(t, st, ctx, "oldsighter")
	c := mustCompanion(t, st, ctx, u, "Old", 1)
	heard(t, st, ctx, AdvertHeard{CompanionID: c.ID, ReceivedAt: time.Now().Add(-CheckinRetention - time.Hour)})
	heard(t, st, ctx, AdvertHeard{CompanionID: c.ID, ReceivedAt: time.Now()})
	n, err := st.PruneCheckins(ctx)
	if err != nil || n != 1 {
		t.Fatalf("pruned = %d, %v", n, err)
	}
}

// TestNetControlAndAcknowledgements: net control is per net and only for
// current members; an acknowledgement either confirms a heard advert or, with
// none heard, logs the check-in by hand — and that member shows on the roster
// even with no companion at all.
func TestNetControlAndAcknowledgements(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "ncadmin")
	control := mustUser(t, st, ctx, "nccontrol")
	heardMember := mustUser(t, st, ctx, "ncheard")
	radioless := mustUser(t, st, ctx, "ncradioless")
	outsider := mustUser(t, st, ctx, "ncoutsider")
	org := mustOrg(t, st, ctx, "NCOrg", admin)
	other := mustOrg(t, st, ctx, "NCOther", outsider)
	for _, u := range []int64{control, heardMember, radioless} {
		if err := st.AddOrgMember(ctx, org, u, "member"); err != nil {
			t.Fatal(err)
		}
	}
	ny, _ := time.LoadLocation("America/New_York")
	before := time.Date(2026, 10, 30, 12, 0, 0, 0, ny)
	in := testNetInput()
	in.ManualAck = true
	net, err := st.CreateNet(ctx, org, in, admin, before)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetNet(ctx, org, net.ID); !got.ManualAck {
		t.Fatal("manual_ack didn't round-trip")
	}
	sess, err := st.NextNetSession(ctx, net.ID, before)
	if err != nil {
		t.Fatal(err)
	}

	// Net control: members only, per net, gone with membership.
	if err := st.AddNetControl(ctx, org, net.ID, control, admin); err != nil {
		t.Fatal(err)
	}
	if err := st.AddNetControl(ctx, org, net.ID, control, admin); err != nil {
		t.Fatalf("re-adding: %v", err)
	}
	if err := st.AddNetControl(ctx, org, net.ID, outsider, admin); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-member: err = %v", err)
	}
	if err := st.AddNetControl(ctx, other, net.ID, outsider, outsider); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another org's net: err = %v", err)
	}
	if ok, _ := st.IsNetControl(ctx, org, net.ID, control); !ok {
		t.Fatal("designated member isn't net control")
	}
	if ok, _ := st.IsNetControl(ctx, other, net.ID, control); ok {
		t.Fatal("net control leaked across orgs")
	}
	controls, err := st.ListNetControls(ctx, org, net.ID)
	if err != nil || len(controls) != 1 || controls[0].Username != "nccontrol" {
		t.Fatalf("controls = %+v, %v", controls, err)
	}

	// The heard member's advert, then net control's acknowledgement of it.
	c := mustCompanion(t, st, ctx, heardMember, "Heard", 1)
	if _, err := st.pool.Exec(ctx, `UPDATE companions SET created_at = $2 WHERE id = $1`, c.ID, before.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	at := sess.StartsAt.Add(5 * time.Minute)
	heard(t, st, ctx, AdvertHeard{CompanionID: c.ID, ReceivedAt: at})

	roster, _ := st.NetSessionRoster(ctx, org, sess)
	if len(roster) != 1 || !roster[0].Heard() || roster[0].Acked() {
		t.Fatalf("before acknowledging: %+v", roster)
	}
	if err := st.AcknowledgeMember(ctx, org, sess.ID, heardMember, control, "", at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// A radioless member logged by hand, with a note (truncated to 140).
	long := strings.Repeat("x", 200)
	if err := st.AcknowledgeMember(ctx, org, sess.ID, radioless, control, long, at.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.AcknowledgeMember(ctx, org, sess.ID, outsider, control, "", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("acknowledging a non-member: err = %v", err)
	}
	if err := st.AcknowledgeMember(ctx, other, sess.ID, heardMember, control, "", at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("acknowledging on another org's session: err = %v", err)
	}

	roster, err = st.NetSessionRoster(ctx, org, sess)
	if err != nil || len(roster) != 2 {
		t.Fatalf("roster = %+v, %v", roster, err)
	}
	h, r := roster[0], roster[1]
	if h.MemberName != "ncheard" || !h.Heard() || !h.Acked() || h.AddedByHand() || h.AckedBy != "nccontrol" {
		t.Errorf("heard + acknowledged = %+v", h)
	}
	if r.MemberName != "ncradioless" || r.Heard() || !r.AddedByHand() || !r.CheckedIn() || len([]rune(r.AckNote)) != 140 {
		t.Errorf("added by hand = %+v", r)
	}
	sessions, _, err := st.ListNetSessionsPage(ctx, org, net.ID, sess.EndsAt, nil)
	if err != nil || sessions[0].CheckedIn != 2 {
		t.Fatalf("sessions = %+v, %v; the hand-added member counts", sessions, err)
	}

	// Undo, and removing net control.
	if err := st.RemoveAcknowledgement(ctx, org, sess.ID, radioless); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveAcknowledgement(ctx, org, sess.ID, radioless); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second undo: err = %v", err)
	}
	if roster, _ = st.NetSessionRoster(ctx, org, sess); len(roster) != 1 {
		t.Fatalf("after undo the radioless member should drop off: %+v", roster)
	}
	if err := st.RemoveNetControl(ctx, org, net.ID, control); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.IsNetControl(ctx, org, net.ID, control); ok {
		t.Fatal("still net control after removal")
	}
}
