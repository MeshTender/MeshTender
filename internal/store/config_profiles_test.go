package store

import (
	"errors"
	"testing"

	"github.com/jleight/meshtender/internal/geo"
)

// ptr returns a pointer to v, for the optional lat/lon and command-id fields.
func ptr[T any](v T) *T { return &v }

func TestConfigProfilePublishAndRead(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, err := st.CreateUser(ctx, "cfgowner", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "CfgOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}

	// No profile yet → ErrNotFound.
	if _, _, err := st.CurrentProfileVersion(ctx, org.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CurrentProfileVersion before publish = %v, want ErrNotFound", err)
	}

	base := []ConfigStep{
		{CommandLine: "set tx 22"},
		{CommandLine: "", Comment: "radio settings above"},
	}
	zones := []ZoneInput{
		// A boxed metro zone over lat[10,20] lon[30,40].
		{Name: "metro", Priority: 10, GeofenceJSON: geo.Rectangle(10, 30, 20, 40),
			Steps: []ConfigStep{{CommandLine: "region put Metro"}}},
		// A match-all base region.
		{Name: "country", Priority: 0, GeofenceJSON: nil,
			Steps: []ConfigStep{{CommandLine: "region put Country"}}},
	}
	v, err := st.PublishProfileVersion(ctx, org.ID, "v1", owner.ID, base, zones)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if v != 1 {
		t.Fatalf("first version = %d, want 1", v)
	}

	vid, vnum, err := st.CurrentProfileVersion(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if vnum != 1 {
		t.Fatalf("current version = %d, want 1", vnum)
	}
	gotBase, gotZones, err := st.ProfileVersion(ctx, vid)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotBase) != 2 || gotBase[0].CommandLine != "set tx 22" {
		t.Fatalf("base steps round-trip wrong: %+v", gotBase)
	}
	if len(gotZones) != 2 {
		t.Fatalf("zones = %d, want 2", len(gotZones))
	}
	// Zones come back ordered by (priority, id): country(0) then metro(10).
	if gotZones[0].Name != "country" || gotZones[1].Name != "metro" {
		t.Fatalf("zone order = [%s,%s], want [country,metro]", gotZones[0].Name, gotZones[1].Name)
	}

	// Publishing again bumps the version and leaves v1 intact.
	v2, err := st.PublishProfileVersion(ctx, org.ID, "v2", owner.ID, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v2 != 2 {
		t.Fatalf("second version = %d, want 2", v2)
	}
}

func TestResolveProfile(t *testing.T) {
	t.Parallel()

	mk := func(name string, prio int, id int64, box []byte, line string) Zone {
		shape, err := geo.Parse(box)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		return Zone{ID: id, Name: name, Priority: prio, Geofence: shape,
			Steps: []ConfigStep{{CommandLine: line}}}
	}

	base := []ConfigStep{{CommandLine: "set tx 22"}}
	zones := []Zone{
		mk("country", 0, 1, nil, "region put Country"),                  // match-all
		mk("metroA", 10, 2, geo.Rectangle(10, 30, 20, 40), "region put MetroA"),
		mk("metroB", 10, 3, geo.Rectangle(15, 35, 25, 45), "region put MetroB"), // overlaps metroA, same priority
	}

	lines := func(steps []ConfigStep) []string {
		out := make([]string, len(steps))
		for i, s := range steps {
			out[i] = s.CommandLine
		}
		return out
	}
	eq := func(label string, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", label, got, want)
			}
		}
	}

	// Inside metroA only (lat 12, lon 32): base + country + metroA.
	eq("metroA only", lines(ResolveProfile(base, zones, ptr(12.0), ptr(32.0))),
		[]string{"set tx 22", "region put Country", "region put MetroA"})

	// Inside the metroA/metroB overlap (lat 17, lon 37): both apply, deterministic
	// by id (metroA=2 before metroB=3) at the shared priority.
	eq("overlap", lines(ResolveProfile(base, zones, ptr(17.0), ptr(37.0))),
		[]string{"set tx 22", "region put Country", "region put MetroA", "region put MetroB"})

	// No location: only base + match-all zones.
	eq("no location", lines(ResolveProfile(base, zones, nil, nil)),
		[]string{"set tx 22", "region put Country"})

	// Outside all boxes (lat 0, lon 0): base + match-all only.
	eq("outside", lines(ResolveProfile(base, zones, ptr(0.0), ptr(0.0))),
		[]string{"set tx 22", "region put Country"})
}
