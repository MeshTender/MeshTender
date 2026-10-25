package store

import (
	"context"
	"errors"
	"fmt"
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

// sight records a sighting heard at `at` and fails the test on error.
func sight(t *testing.T, st *Store, ctx context.Context, sg AdvertSighting) bool {
	t.Helper()
	if sg.AdvertTS == 0 {
		sg.AdvertTS = uint32(sg.ReceivedAt.Unix())
	}
	if sg.ObserverPublicKeyHex == "" {
		sg.ObserverPublicKeyHex = testKey(200)
	}
	if sg.RouteType == 0 && sg.PathHashSize == 0 {
		sg.RouteType, sg.PathHashSize = 1, 1 // flood
	}
	if sg.Path == nil {
		sg.Path = []byte{}
	}
	ok, err := st.RecordAdvertSighting(ctx, sg)
	if err != nil {
		t.Fatal(err)
	}
	return ok
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

func TestRecordAdvertSightingConfirmsAndDedupes(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u := mustUser(t, st, ctx, "sighter")
	c := mustCompanion(t, st, ctx, u, "T1", 1)

	// Heard before registration (an advert that was in flight): stored, but it
	// doesn't confirm.
	early := c.CreatedAt.Add(-time.Minute)
	if !sight(t, st, ctx, AdvertSighting{CompanionID: c.ID, ReceivedAt: early}) {
		t.Fatal("first sighting not inserted")
	}
	got, _ := st.GetCompanionForViewer(ctx, c.PublicID, u)
	if got.Confirmed() || got.LastHeardAt == nil {
		t.Fatalf("after early sighting: %+v", got)
	}

	heard := c.CreatedAt.Add(time.Minute)
	sg := AdvertSighting{CompanionID: c.ID, ReceivedAt: heard, Path: []byte{0x35, 0x8b}}
	if !sight(t, st, ctx, sg) {
		t.Fatal("sighting not inserted")
	}
	// The identical sighting (uplink duplicate) is absorbed.
	if sight(t, st, ctx, sg) {
		t.Fatal("duplicate sighting inserted")
	}
	// Same advert, same observer, different route: a distinct row.
	sg.Path = []byte{0x35, 0x99}
	if !sight(t, st, ctx, sg) {
		t.Fatal("second route not inserted")
	}

	got, _ = st.GetCompanionForViewer(ctx, c.PublicID, u)
	if !got.Confirmed() || !got.LastHeardAt.Equal(heard.Truncate(time.Microsecond)) {
		t.Fatalf("after sighting: %+v", got)
	}
	if got.LastAdvertAt == nil || got.LastAdvertAt.Unix() != heard.Unix() {
		t.Fatalf("last advert = %v", got.LastAdvertAt)
	}

	page, more, err := st.ListCompanionSightingsPage(ctx, c.ID, nil)
	if err != nil || more || len(page) != 3 {
		t.Fatalf("sightings = %d, more=%v, err=%v", len(page), more, err)
	}
	if hops := page[0].Hops(); len(hops) != 2 {
		t.Fatalf("hops = %x", hops)
	}

	routes, err := st.ListCompanionRoutes(ctx, c.ID, heard)
	if err != nil || len(routes) != 3 {
		t.Fatalf("routes = %+v, %v", routes, err)
	}

	if err := st.RecordAdvertRejected(ctx, c.ID, heard, 50*365*24*time.Hour); err != nil {
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
	sight(t, st, ctx, AdvertSighting{CompanionID: confirmed.ID, ReceivedAt: time.Now()})

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
		// Heard after registration, so it confirms.
		sight(t, st, ctx, AdvertSighting{CompanionID: c.ID, ReceivedAt: time.Now(), RouteType: 1, PathHashSize: 3,
			Path: []byte{0x35, 0x2c, 0x30, 0x8b, 0xfc, 0xfc}})
	}
	// Unconfirmed, and a non-member's: neither listed.
	pending := mustUser(t, st, ctx, "actpending")
	_ = st.AddOrgMember(ctx, org, pending, "member")
	mustCompanion(t, st, ctx, pending, "zz unconfirmed", 90)
	oc := mustCompanion(t, st, ctx, outsider, "outsider", 91)
	sight(t, st, ctx, AdvertSighting{CompanionID: oc.ID, ReceivedAt: time.Now()})

	page, more, err := st.ListOrgCompanionActivityPage(ctx, org, nil)
	if err != nil || !more || len(page) != OrgCompanionActivityPageSize {
		t.Fatalf("page 1 = %d, more=%v, err=%v", len(page), more, err)
	}
	if page[0].Name != "comp 00" || page[0].HomeHashSize != 3 || fmt.Sprintf("%x", page[0].HomeHash) != "352c30" {
		t.Fatalf("first row = %+v", page[0])
	}
	last := page[len(page)-1]
	page, more, err = st.ListOrgCompanionActivityPage(ctx, org, &ActivityCursor{Name: last.Name, ID: last.ID})
	if err != nil || more || len(page) != 1 || page[0].Name != fmt.Sprintf("comp %02d", OrgCompanionActivityPageSize) {
		t.Fatalf("page 2 = %+v, more=%v, err=%v", page, more, err)
	}
}

func TestListRepeaterKeysForViewer(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	alice := mustUser(t, st, ctx, "rkalice")
	bob := mustUser(t, st, ctx, "rkbob")
	eve := mustUser(t, st, ctx, "rkeve")
	org := mustOrg(t, st, ctx, "RKOrg", alice)
	_ = st.AddOrgMember(ctx, org, bob, "member")
	mustOrg(t, st, ctx, "EveOrg", eve)

	for i, owner := range []int64{bob, eve} {
		if _, err := st.CreateRepeater(ctx, &Repeater{OwnerID: owner, Name: fmt.Sprintf("r%d", i), PublicKeyHex: testKey(i)}); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := st.ListRepeaterKeysForViewer(ctx, alice)
	if err != nil || len(keys) != 1 || keys[0].Name != "r0" {
		t.Fatalf("keys = %+v, %v", keys, err)
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
	nets, _ := st.ListOrgNets(ctx, org)
	if len(nets) != MaxNetsPerOrg {
		t.Fatalf("nets = %d", len(nets))
	}
}

func TestNetSessionRoster(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	admin := mustUser(t, st, ctx, "rosteradmin")
	member := mustUser(t, st, ctx, "rostermember")
	outsider := mustUser(t, st, ctx, "rosteroutsider")
	org := mustOrg(t, st, ctx, "RosterOrg", admin)
	_ = st.AddOrgMember(ctx, org, member, "member")
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
	backdate := func(c *Companion) {
		if _, err := st.pool.Exec(ctx, `UPDATE companions SET created_at = $2 WHERE id = $1`, c.ID, before.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
		sight(t, st, ctx, AdvertSighting{CompanionID: c.ID, ReceivedAt: before, Path: []byte{}})
	}
	flood := mustCompanion(t, st, ctx, member, "Flood", 1)
	zero := mustCompanion(t, st, ctx, admin, "ZeroHop", 2)
	late := mustCompanion(t, st, ctx, admin, "Late", 3)
	absent := mustCompanion(t, st, ctx, member, "Absent", 4)
	foreign := mustCompanion(t, st, ctx, outsider, "Foreign", 5)
	for _, c := range []*Companion{flood, zero, late, absent, foreign} {
		backdate(c)
	}

	at := sess.StartsAt.Add(10 * time.Minute)
	// Flood: two observers, 3 and 1 hops.
	sight(t, st, ctx, AdvertSighting{CompanionID: flood.ID, ReceivedAt: at, ObserverPublicKeyHex: testKey(100),
		RouteType: 1, PathHashSize: 1, Path: []byte{1, 2, 3}})
	sight(t, st, ctx, AdvertSighting{CompanionID: flood.ID, ReceivedAt: at.Add(time.Second), ObserverPublicKeyHex: testKey(101),
		RouteType: 1, PathHashSize: 1, Path: []byte{9}})
	// Zero-hop: direct, empty path, earlier than flood.
	sight(t, st, ctx, AdvertSighting{CompanionID: zero.ID, ReceivedAt: at.Add(-time.Minute), RouteType: 2, PathHashSize: 1})
	// Exactly at the end: outside the half-open window.
	sight(t, st, ctx, AdvertSighting{CompanionID: late.ID, ReceivedAt: sess.EndsAt})
	sight(t, st, ctx, AdvertSighting{CompanionID: foreign.ID, ReceivedAt: at})

	roster, err := st.NetSessionRoster(ctx, org, sess)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range roster {
		names = append(names, e.CompanionName)
	}
	if got := strings.Join(names, ","); got != "ZeroHop,Flood,Absent,Late" {
		t.Fatalf("roster order = %s", got)
	}
	z, f, a := roster[0], roster[1], roster[2]
	if !z.CheckedIn() || !z.ZeroHopOnly || z.Observers != 1 {
		t.Errorf("zero-hop = %+v", z)
	}
	if !f.CheckedIn() || f.ZeroHopOnly || f.Observers != 2 || f.FewestHops != 1 {
		t.Errorf("flood = %+v", f)
	}
	if a.CheckedIn() || roster[3].CheckedIn() {
		t.Errorf("absent/late checked in: %+v %+v", a, roster[3])
	}

	sessions, _, err := st.ListNetSessionsPage(ctx, org, net.ID, sess.EndsAt, nil)
	if err != nil || len(sessions) != 1 || sessions[0].CheckedIn != 2 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
}

func TestPruneSightings(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u := mustUser(t, st, ctx, "oldsighter")
	c := mustCompanion(t, st, ctx, u, "Old", 1)
	sight(t, st, ctx, AdvertSighting{CompanionID: c.ID, ReceivedAt: time.Now().Add(-SightingRetention - time.Hour)})
	sight(t, st, ctx, AdvertSighting{CompanionID: c.ID, ReceivedAt: time.Now()})
	n, err := st.PruneSightings(ctx)
	if err != nil || n != 1 {
		t.Fatalf("pruned = %d, %v", n, err)
	}
}
