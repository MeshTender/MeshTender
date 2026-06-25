package store

import (
	"testing"

	"github.com/jleight/meshtender/internal/geo"
)

// ptr returns a pointer to v, for the optional lat/lon and command-id fields.
func ptr[T any](v T) *T { return &v }

func TestOrgConfigReplaceAndRead(t *testing.T) {
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

	if has, err := st.OrgHasConfig(ctx, org.ID); err != nil || has {
		t.Fatalf("OrgHasConfig before = (%v, %v), want (false, nil)", has, err)
	}

	profiles := []ProfileInput{
		{Name: "ESP32", Steps: []ConfigStep{{CommandLine: "set tx 22"}, {Comment: "esp notes"}}},
		{Name: "nRF52", Steps: []ConfigStep{{CommandLine: "set tx 20"}}},
	}
	regions := []RegionInput{
		{Name: "metro", Priority: 10, GeofenceJSON: geo.Rectangle(10, 30, 20, 40),
			Steps: []ConfigStep{{CommandLine: "region put Metro"}}},
		{Name: "country", Priority: 0, GeofenceJSON: nil,
			Steps: []ConfigStep{{CommandLine: "region put Country"}}},
	}
	if err := st.ReplaceOrgConfig(ctx, org.ID, profiles, regions); err != nil {
		t.Fatalf("replace: %v", err)
	}

	if has, err := st.OrgHasConfig(ctx, org.ID); err != nil || !has {
		t.Fatalf("OrgHasConfig after = (%v, %v), want (true, nil)", has, err)
	}

	gotP, err := st.ListProfiles(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotP) != 2 || gotP[0].Name != "ESP32" || gotP[1].Name != "nRF52" {
		t.Fatalf("profiles = %+v", gotP)
	}
	if len(gotP[0].Steps) != 2 || gotP[0].Steps[0].CommandLine != "set tx 22" {
		t.Fatalf("ESP32 steps round-trip wrong: %+v", gotP[0].Steps)
	}

	gotR, err := st.ListRegions(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Regions come back ordered by (priority, id): country(0) then metro(10).
	if len(gotR) != 2 || gotR[0].Name != "country" || gotR[1].Name != "metro" {
		t.Fatalf("region order = %+v, want [country, metro]", gotR)
	}

	// Replace fully (mutable, no versioning): the old set is gone.
	if err := st.ReplaceOrgConfig(ctx, org.ID, []ProfileInput{{Name: "Only"}}, nil); err != nil {
		t.Fatal(err)
	}
	gotP, _ = st.ListProfiles(ctx, org.ID)
	if len(gotP) != 1 || gotP[0].Name != "Only" {
		t.Fatalf("after replace, profiles = %+v, want [Only]", gotP)
	}
	gotR, _ = st.ListRegions(ctx, org.ID)
	if len(gotR) != 0 {
		t.Fatalf("after replace, regions = %+v, want none", gotR)
	}
}

func TestResolveRegions(t *testing.T) {
	t.Parallel()

	mk := func(name string, prio int, id int64, box []byte, line string) Region {
		shape, err := geo.Parse(box)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		return Region{ID: id, Name: name, Priority: prio, Geofence: shape,
			Steps: []ConfigStep{{CommandLine: line}}}
	}

	regions := []Region{
		mk("country", 0, 1, nil, "region put Country"), // match-all
		mk("metroA", 10, 2, geo.Rectangle(10, 30, 20, 40), "region put MetroA"),
		mk("metroB", 10, 3, geo.Rectangle(15, 35, 25, 45), "region put MetroB"), // overlaps metroA
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

	eq("metroA only", lines(ResolveRegions(regions, ptr(12.0), ptr(32.0))),
		[]string{"region put Country", "region put MetroA"})
	eq("overlap", lines(ResolveRegions(regions, ptr(17.0), ptr(37.0))),
		[]string{"region put Country", "region put MetroA", "region put MetroB"})
	eq("no location", lines(ResolveRegions(regions, nil, nil)),
		[]string{"region put Country"})
	eq("outside", lines(ResolveRegions(regions, ptr(0.0), ptr(0.0))),
		[]string{"region put Country"})
}
