package store

import (
	"context"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/observe"
)

func mustRepeater(t *testing.T, st *Store, ctx context.Context, owner int64, name string, key int) *Repeater {
	t.Helper()
	r, err := st.CreateRepeater(ctx, &Repeater{OwnerID: owner, Name: name, PublicKeyHex: testKey(key),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// repeaterHeard records an advert and fails the test on error. The sender
// timestamp defaults to the heard time.
func repeaterHeard(t *testing.T, st *Store, ctx context.Context, a RepeaterAdvert) bool {
	t.Helper()
	if a.AdvertTS == 0 {
		a.AdvertTS = uint32(a.ReceivedAt.Unix()) //nolint:gosec // test clock
	}
	created, err := st.RecordRepeaterAdvert(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func mustObservation(t *testing.T, st *Store, ctx context.Context, key string) *RepeaterObservation {
	t.Helper()
	o, err := st.GetRepeaterObservation(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestRecordRepeaterAdvert(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	key := testKey(1)
	if _, err := st.GetRepeaterObservation(ctx, key); err != ErrNotFound {
		t.Fatalf("before any advert: %v, want ErrNotFound", err)
	}
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	lat, lon := 42.9, -78.8
	flood := RepeaterAdvert{PublicKeyHex: key, ReceivedAt: t0, Flood: true, Name: "Hilltop",
		Lat: &lat, Lon: &lon, PathHashSize: 2, Packet: []byte{1, 2, 3}}
	if !repeaterHeard(t, st, ctx, flood) {
		t.Fatal("first copy didn't count")
	}
	// Another observer's copy, and a later replay, of the same advert change nothing.
	dup := flood
	dup.ReceivedAt, dup.Name = t0.Add(time.Hour), "Replayed"
	dup.AdvertTS = uint32(t0.Unix())
	if repeaterHeard(t, st, ctx, dup) {
		t.Error("replay counted")
	}
	o := mustObservation(t, st, ctx, key)
	if !o.LastHeardAt.Equal(t0) || o.LastFloodAt == nil || !o.LastFloodAt.Equal(t0) || o.LastZeroHopAt != nil {
		t.Errorf("times: heard %v flood %v zero-hop %v", o.LastHeardAt, o.LastFloodAt, o.LastZeroHopAt)
	}
	if o.AdvertName != "Hilltop" || o.AdvertLat == nil || *o.AdvertLat != lat || *o.AdvertLon != lon {
		t.Errorf("advertised: %q %v,%v", o.AdvertName, o.AdvertLat, o.AdvertLon)
	}
	if o.PathHashSize == nil || *o.PathHashSize != 2 || string(o.LastFloodPacket) != "\x01\x02\x03" {
		t.Errorf("flood details: size %v packet %x", o.PathHashSize, o.LastFloodPacket)
	}

	// A newer zero-hop advert takes over the name and location (here: none) but
	// can't say anything about the hash size or scope.
	zh := RepeaterAdvert{PublicKeyHex: key, ReceivedAt: t0.Add(2 * time.Hour), Name: "Hilltop 2"}
	repeaterHeard(t, st, ctx, zh)
	o = mustObservation(t, st, ctx, key)
	if o.AdvertName != "Hilltop 2" || o.AdvertLat != nil || o.LastZeroHopAt == nil {
		t.Errorf("after zero-hop: %q %v %v", o.AdvertName, o.AdvertLat, o.LastZeroHopAt)
	}
	if o.PathHashSize == nil || *o.PathHashSize != 2 || len(o.LastFloodPacket) != 3 || !o.LastFloodAt.Equal(t0) {
		t.Errorf("zero-hop overwrote flood details: %v %x %v", o.PathHashSize, o.LastFloodPacket, o.LastFloodAt)
	}

	// An older advert processed late doesn't roll anything back.
	late := RepeaterAdvert{PublicKeyHex: key, ReceivedAt: t0.Add(time.Hour), Flood: true, Name: "Old",
		PathHashSize: 1, Packet: []byte{9}}
	repeaterHeard(t, st, ctx, late)
	o = mustObservation(t, st, ctx, key)
	if o.AdvertName != "Hilltop 2" || !o.LastHeardAt.Equal(t0.Add(2*time.Hour)) {
		t.Errorf("late advert rolled back: %q %v", o.AdvertName, o.LastHeardAt)
	}
	if !o.LastFloodAt.Equal(t0.Add(time.Hour)) || *o.PathHashSize != 1 {
		t.Errorf("newer flood advert ignored: %v %v", o.LastFloodAt, *o.PathHashSize)
	}
}

// The sender's clock only feeds the offset: one restarted in 2024 still
// counts as the newest advert.
func TestRecordRepeaterAdvertClockOffset(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	key := testKey(2)
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	repeaterHeard(t, st, ctx, RepeaterAdvert{PublicKeyHex: key, ReceivedAt: t0, Name: "A"})
	reset := uint32(1_715_770_351) // the firmware's unsynced-RTC start
	repeaterHeard(t, st, ctx, RepeaterAdvert{PublicKeyHex: key, ReceivedAt: t0.Add(time.Minute),
		AdvertTS: reset, Name: "B"})
	o := mustObservation(t, st, ctx, key)
	if o.AdvertName != "B" {
		t.Errorf("name = %q, want B", o.AdvertName)
	}
	if want := t0.Add(time.Minute).Sub(time.Unix(int64(reset), 0)); o.ClockOffset != want {
		t.Errorf("offset = %v, want %v", o.ClockOffset, want)
	}
}

func TestRecordRepeaterAdvertInterval(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	key := testKey(3)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	flood := func(at time.Time) {
		repeaterHeard(t, st, ctx, RepeaterAdvert{PublicKeyHex: key, ReceivedAt: at, Flood: true, PathHashSize: 1})
	}
	flood(t0)
	flood(t0.Add(12 * time.Hour))
	if o := mustObservation(t, st, ctx, key); o.FloodInterval != nil {
		t.Errorf("one gap gave an estimate: %+v", o.FloodInterval)
	}
	// Zero-hop adverts in between don't count toward it.
	repeaterHeard(t, st, ctx, RepeaterAdvert{PublicKeyHex: key, ReceivedAt: t0.Add(13 * time.Hour)})
	flood(t0.Add(24 * time.Hour))
	o := mustObservation(t, st, ctx, key)
	if o.FloodInterval == nil || *o.FloodInterval != (observe.FloodInterval{Hours: 12, Samples: 2}) {
		t.Errorf("estimate = %+v, want 12 h from 2", o.FloodInterval)
	}
}

func TestOrgHasRepeaterKey(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner := mustUser(t, st, ctx, "rowner")
	outsider := mustUser(t, st, ctx, "routsider")
	org := mustOrg(t, st, ctx, "Mesh", owner)
	other := mustOrg(t, st, ctx, "Other", outsider)
	r := mustRepeater(t, st, ctx, owner, "Hill", 10)
	mustRepeater(t, st, ctx, outsider, "Theirs", 11)

	has := func(org int64, key string) bool {
		t.Helper()
		ok, err := st.OrgHasRepeaterKey(ctx, org, key)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !has(org, testKey(10)) {
		t.Error("member's repeater not in org")
	}
	if has(org, testKey(11)) || has(other, testKey(10)) || has(org, testKey(12)) {
		t.Error("non-member's or unknown repeater in org")
	}
	if err := st.SetRepeaterOrgExcluded(ctx, org, r.ID, true); err != nil {
		t.Fatal(err)
	}
	if has(org, testKey(10)) {
		t.Error("opted-out repeater still in org")
	}
	// A second owner registering the same key into the org brings it back.
	co := mustUser(t, st, ctx, "rco")
	if err := st.AddOrgMember(ctx, org, co, "member"); err != nil {
		t.Fatal(err)
	}
	mustRepeater(t, st, ctx, co, "Hill (co)", 10)
	if !has(org, testKey(10)) {
		t.Error("co-registered key not in org")
	}
}

func TestListRepeaterObservations(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	repeaterHeard(t, st, ctx, RepeaterAdvert{PublicKeyHex: testKey(20), ReceivedAt: t0, Name: "A"})
	repeaterHeard(t, st, ctx, RepeaterAdvert{PublicKeyHex: testKey(21), ReceivedAt: t0, Name: "B"})
	got, err := st.ListRepeaterObservations(ctx, []string{testKey(20), testKey(21), testKey(22)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[testKey(20)].AdvertName != "A" || got[testKey(21)].AdvertName != "B" {
		t.Errorf("got %+v", got)
	}
	if empty, err := st.ListRepeaterObservations(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("no keys: %v %v", empty, err)
	}
}

func TestPruneRepeaterAdverts(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner := mustUser(t, st, ctx, "powner")
	mustRepeater(t, st, ctx, owner, "Kept", 30)
	now := time.Now()
	key := testKey(30)
	for _, a := range []RepeaterAdvert{
		{ReceivedAt: now.Add(-RepeaterFloodAdvertRetention - time.Hour), Flood: true}, // gone
		{ReceivedAt: now.Add(-RepeaterFloodAdvertRetention + time.Hour), Flood: true}, // kept
		{ReceivedAt: now.Add(-RepeaterZeroHopAdvertRetention - time.Hour)},            // gone
		{ReceivedAt: now.Add(-RepeaterZeroHopAdvertRetention + time.Hour)},            // kept
	} {
		a.PublicKeyHex = key
		repeaterHeard(t, st, ctx, a)
	}
	// Heard once, then unregistered: its observation goes too.
	repeaterHeard(t, st, ctx, RepeaterAdvert{PublicKeyHex: testKey(31), ReceivedAt: now})

	if _, err := st.PruneRepeaterAdverts(ctx); err != nil {
		t.Fatal(err)
	}
	var floods, zeroHops int
	if err := st.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE NOT zero_hop), count(*) FILTER (WHERE zero_hop)
		FROM repeater_adverts WHERE public_key_hex = $1`, key).Scan(&floods, &zeroHops); err != nil {
		t.Fatal(err)
	}
	if floods != 1 || zeroHops != 1 {
		t.Errorf("left %d flood, %d zero-hop; want 1, 1", floods, zeroHops)
	}
	mustObservation(t, st, ctx, key)
	if _, err := st.GetRepeaterObservation(ctx, testKey(31)); err != ErrNotFound {
		t.Errorf("unregistered key's observation: %v", err)
	}
}
