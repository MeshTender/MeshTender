package ingest

import (
	"errors"
	"testing"
	"time"

	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
	"github.com/MeshTender/MeshTender/internal/store"
)

// newRepeater registers a repeater owned by the fixture's org owner.
func (f *fixture) newRepeater(t *testing.T, name string) meshcore.LocalIdentity {
	t.Helper()
	id := observetest.NewIdentity(t)
	if _, err := f.st.CreateRepeater(t.Context(), &store.Repeater{OwnerID: f.owner, Name: name,
		PublicKeyHex: id.String(), RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestIngesterRecordsRepeaterAdvert(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	rep := f.newRepeater(t, "Hilltop")
	in := &Ingester{Store: st, Log: quietLog()}

	// A repeater whose clock restarted in 2024, scoped to a region, relayed
	// twice before the observer heard it.
	now := time.Now()
	reset := uint32(1_715_770_351)
	raw := observetest.Advert{Type: "REPEATER", Name: "Hilltop", Lat: 42.9, Lon: -78.8, Scope: "buf",
		HashSize: 2, Path: [][]byte{{1, 2}, {3, 4}}}.Packet(t, rep, reset)
	in.Handle(ctx, packetMsg(f.org, f.observer, raw, now))

	o, err := st.GetRepeaterObservation(ctx, rep.String())
	if err != nil {
		t.Fatalf("not recorded: %v", err)
	}
	if o.AdvertName != "Hilltop" || o.AdvertLat == nil || o.LastFloodAt == nil || *o.PathHashSize != 2 {
		t.Errorf("observation = %+v", o)
	}
	if o.ClockOffset < 365*24*time.Hour {
		t.Errorf("clock offset = %v, want the 2024 reset", o.ClockOffset)
	}
	if s := observe.MatchScope(o.LastFloodPacket, []string{"roc", "buf"}); s != (observe.Scope{Scoped: true, Region: "buf"}) {
		t.Errorf("scope = %+v", s)
	}
}

func TestIngesterIgnoresRepeaterAdverts(t *testing.T) {
	t.Parallel()
	st, ctx := newStore(t)
	f := newFixture(t, st, ctx)
	rep := f.newRepeater(t, "Hilltop")
	in := &Ingester{Store: st, Log: quietLog()}
	now := time.Now()
	good := observetest.Advert{Type: "REPEATER", Name: "Hilltop", Route: meshcore.RouteTypeFlood}.
		Packet(t, rep, uint32(now.Unix())) //nolint:gosec // test clock

	// An unknown observer; the org's observer heard on another org's connection.
	in.Handle(ctx, packetMsg(f.org, observetest.NewIdentity(t), good, now))
	otherOrg, err := st.CreateOrg(ctx, "Elsewhere", f.owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateOrgObserver(ctx, otherOrg.ID, "Roof", f.observer.String(), f.owner); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepeaterOrgExcluded(ctx, otherOrg.ID, repeaterID(t, f, rep), true); err != nil {
		t.Fatal(err)
	}
	// ...where the repeater is opted out.
	in.Handle(ctx, packetMsg(otherOrg.ID, f.observer, good, now))
	// A tampered advert.
	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 1
	in.Handle(ctx, packetMsg(f.org, f.observer, bad, now))
	// A repeater nobody registered.
	in.Handle(ctx, packetMsg(f.org, f.observer, observetest.Advert{Type: "REPEATER", Name: "x",
		Route: meshcore.RouteTypeFlood}.Packet(t, observetest.NewIdentity(t), 1), now))

	if _, err := st.GetRepeaterObservation(ctx, rep.String()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("recorded an ignored advert: %v", err)
	}
	// The companion path is untouched by all this.
	if got := f.checkins(t, ctx); len(got) != 0 {
		t.Fatalf("check-ins = %+v", got)
	}
}

func repeaterID(t *testing.T, f *fixture, id meshcore.LocalIdentity) int64 {
	t.Helper()
	reps, err := f.st.ListRepeatersForUser(t.Context(), f.owner)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reps {
		if r.PublicKeyHex == id.String() {
			return r.ID
		}
	}
	t.Fatal("repeater not found")
	return 0
}
