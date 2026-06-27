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
		{Token: "metro", DisplayName: "Metro", Layer: 2, GeofenceJSON: geo.Rectangle(10, 30, 20, 40)},
		{Token: "country", DisplayName: "Country", Layer: 1, GeofenceJSON: nil},
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
	// Regions come back ordered by (layer, token): country(1) then metro(2).
	if len(gotR) != 2 || gotR[0].Token != "country" || gotR[1].Token != "metro" {
		t.Fatalf("region order = %+v, want [country, metro]", gotR)
	}
	if gotR[0].DisplayName != "Country" || gotR[0].Layer != 1 {
		t.Fatalf("region round-trip wrong: %+v", gotR[0])
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

func TestRegionDefCommands(t *testing.T) {
	t.Parallel()

	mk := func(token string, layer int, box []byte) Region {
		shape, err := geo.Parse(box)
		if err != nil {
			t.Fatalf("parse %s: %v", token, err)
		}
		return Region{Token: token, Layer: layer, Geofence: shape}
	}

	// us (L1) covers everything; ny and pa (L2) sit inside us and overlap each
	// other in a border strip (lon 25–30); buf (L3) is inside ny only.
	regions := []Region{
		mk("us", 1, geo.Rectangle(0, 0, 100, 100)),
		mk("ny", 2, geo.Rectangle(10, 10, 30, 30)),
		mk("pa", 2, geo.Rectangle(10, 25, 30, 45)), // overlaps ny in lon 25–30
		mk("buf", 3, geo.Rectangle(12, 12, 18, 18)), // inside ny, west of pa
	}

	def := func(label string, lat, lon float64, want string) {
		t.Helper()
		got := RegionDefCommands(regions, ptr(lat), ptr(lon))
		if len(got) != 2 || got[0] != want || got[1] != "region save" {
			t.Fatalf("%s = %v, want [%q, region save]", label, got, want)
		}
	}

	// Inside ny + buf only (lon 15) → a linear chain.
	def("buf", 15, 15, "region def us ny buf")
	// In the ny/pa overlap (lon 27) → siblings ny and pa branch under us.
	def("border overlap", 15, 27, "region def us ny|us pa")
	// In pa only (lon 40) → linear us → pa.
	def("pa only", 15, 40, "region def us pa")

	// Unknown / outside location → no commands.
	if got := RegionDefCommands(regions, nil, nil); got != nil {
		t.Fatalf("no location: got %v, want nil", got)
	}
	if got := RegionDefCommands(regions, ptr(-5.0), ptr(-5.0)); got != nil {
		t.Fatalf("outside: got %v, want nil", got)
	}
}
