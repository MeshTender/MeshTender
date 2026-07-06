package store

import (
	"strings"
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

func TestProfileCRUD(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, err := st.CreateUser(ctx, "profowner", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "ProfOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}

	id, err := st.CreateProfile(ctx, org.ID, "ESP32", []ConfigStep{{CommandLine: "set tx 22"}, {Comment: "note"}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Duplicate name (case-insensitive per the unique constraint is not guaranteed;
	// exact-name collide is).
	if _, err := st.CreateProfile(ctx, org.ID, "ESP32", nil); err != ErrDuplicate {
		t.Fatalf("duplicate create err = %v, want ErrDuplicate", err)
	}

	got, err := st.GetProfile(ctx, org.ID, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "ESP32" || len(got.Steps) != 2 || got.Steps[0].CommandLine != "set tx 22" {
		t.Fatalf("get profile = %+v", got)
	}

	// Wrong org can't see it.
	other, err := st.CreateOrg(ctx, "OtherOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetProfile(ctx, other.ID, id); err != ErrNotFound {
		t.Fatalf("cross-org get err = %v, want ErrNotFound", err)
	}

	// Update: rename + replace steps, position preserved.
	if err := st.UpdateProfile(ctx, org.ID, id, "Heltec", []ConfigStep{{CommandLine: "set tx 20"}}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = st.GetProfile(ctx, org.ID, id)
	if got.Name != "Heltec" || len(got.Steps) != 1 || got.Steps[0].CommandLine != "set tx 20" {
		t.Fatalf("after update = %+v", got)
	}
	// Update non-owned → ErrNotFound.
	if err := st.UpdateProfile(ctx, other.ID, id, "X", nil); err != ErrNotFound {
		t.Fatalf("cross-org update err = %v, want ErrNotFound", err)
	}

	// A second profile, then a name clash on update → ErrDuplicate.
	id2, err := st.CreateProfile(ctx, org.ID, "Second", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateProfile(ctx, org.ID, id2, "Heltec", nil); err != ErrDuplicate {
		t.Fatalf("update to dup name err = %v, want ErrDuplicate", err)
	}

	// Delete: gone, and idempotency guard returns ErrNotFound on re-delete.
	if err := st.DeleteProfile(ctx, org.ID, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := st.DeleteProfile(ctx, org.ID, id); err != ErrNotFound {
		t.Fatalf("re-delete err = %v, want ErrNotFound", err)
	}
	remaining, _ := st.ListProfiles(ctx, org.ID)
	if len(remaining) != 1 || remaining[0].Name != "Second" {
		t.Fatalf("after delete, profiles = %+v", remaining)
	}
}

func TestReplaceRegionsKeepsProfiles(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, err := st.CreateUser(ctx, "rgnowner", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "RgnOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProfile(ctx, org.ID, "Keep", nil); err != nil {
		t.Fatal(err)
	}

	if err := st.ReplaceRegions(ctx, org.ID, []RegionInput{
		{Token: "country", DisplayName: "Country", Layer: 1, AllowFlood: true},
		{Token: "metro", DisplayName: "Metro", Layer: 2, Primary: true, AllowFlood: true, GeofenceJSON: geo.Rectangle(10, 30, 20, 40)},
	}, false); err != nil {
		t.Fatalf("replace regions: %v", err)
	}
	// The root (*) flood policy round-trips (we passed deny above).
	if allow, err := st.RootAllowFlood(ctx, org.ID); err != nil || allow {
		t.Fatalf("root allow flood = %v (err %v), want false", allow, err)
	}
	// Regions saved and profiles untouched.
	gotR, _ := st.ListRegions(ctx, org.ID)
	if len(gotR) != 2 {
		t.Fatalf("regions = %d, want 2", len(gotR))
	}
	gotP, _ := st.ListProfiles(ctx, org.ID)
	if len(gotP) != 1 || gotP[0].Name != "Keep" {
		t.Fatalf("profiles after region replace = %+v, want [Keep]", gotP)
	}

	// Replacing with an empty set clears regions but still keeps profiles.
	if err := st.ReplaceRegions(ctx, org.ID, nil, true); err != nil {
		t.Fatalf("clear regions: %v", err)
	}
	gotR, _ = st.ListRegions(ctx, org.ID)
	if len(gotR) != 0 {
		t.Fatalf("regions after clear = %d, want 0", len(gotR))
	}
	gotP, _ = st.ListProfiles(ctx, org.ID)
	if len(gotP) != 1 {
		t.Fatalf("profiles after clear = %d, want 1", len(gotP))
	}
}

func TestRegionDefCommands(t *testing.T) {
	t.Parallel()

	mk := func(token string, layer int, box []byte) Region {
		shape, err := geo.Parse(box)
		if err != nil {
			t.Fatalf("parse %s: %v", token, err)
		}
		return Region{Token: token, Layer: layer, AllowFlood: true, Geofence: shape}
	}

	// us (L1) covers everything; ny and pa (L2) sit inside us and overlap each
	// other in a border strip (lon 25–30); buf (L3) is inside ny only.
	regions := []Region{
		mk("us", 1, geo.Rectangle(0, 0, 100, 100)),
		mk("ny", 2, geo.Rectangle(10, 10, 30, 30)),
		mk("pa", 2, geo.Rectangle(10, 25, 30, 45)),  // overlaps ny in lon 25–30
		mk("buf", 3, geo.Rectangle(12, 12, 18, 18)), // inside ny, west of pa
	}

	def := func(label string, lat, lon float64, want string) {
		t.Helper()
		// The def line comes first; flood lines follow; "region save" is always last.
		got := RegionDefCommands(regions, true, ptr(lat), ptr(lon))
		if len(got) < 2 || got[0] != want || got[len(got)-1] != "region save" {
			t.Fatalf("%s = %v, want [%q, …, region save]", label, got, want)
		}
	}

	// Inside ny + buf only (lon 15) → a linear chain.
	def("buf", 15, 15, "region def us ny buf")
	// In the ny/pa overlap (lon 27) → siblings ny and pa branch under us.
	def("border overlap", 15, 27, "region def us ny|us pa")
	// In pa only (lon 40) → linear us → pa.
	def("pa only", 15, 40, "region def us pa")

	// Unknown / outside location → no commands (the safety guard: never a lone denyf *).
	if got := RegionDefCommands(regions, true, nil, nil); got != nil {
		t.Fatalf("no location: got %v, want nil", got)
	}
	if got := RegionDefCommands(regions, false, ptr(-5.0), ptr(-5.0)); got != nil {
		t.Fatalf("outside: got %v, want nil", got)
	}
}

func TestRegionDefFloodCommands(t *testing.T) {
	t.Parallel()
	mk := func(token string, layer int, allow bool, box []byte) Region {
		shape, err := geo.Parse(box)
		if err != nil {
			t.Fatalf("parse %s: %v", token, err)
		}
		return Region{Token: token, Layer: layer, AllowFlood: allow, Geofence: shape}
	}
	// us allows flood, ny denies it; root denies flood (scoped-flooding pattern).
	regions := []Region{
		mk("us", 1, true, geo.Rectangle(0, 0, 100, 100)),
		mk("ny", 2, false, geo.Rectangle(10, 10, 30, 30)),
	}
	got := RegionDefCommands(regions, false, ptr(15.0), ptr(15.0))
	want := []string{
		"region def us ny",
		"region denyf *",
		"region allowf us",
		"region denyf ny",
		"region save",
	}
	if len(got) != len(want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commands[%d] = %q, want %q (full %v)", i, got[i], want[i], got)
		}
	}
}

func TestSplitAtRegionMarker(t *testing.T) {
	t.Parallel()
	cmd := func(line string) ConfigStep { return ConfigStep{CommandLine: line} }
	marker := ConfigStep{CommandLine: RegionMarker}
	lines := func(steps []ConfigStep) []string {
		out := []string{}
		for _, s := range steps {
			out = append(out, s.CommandLine)
		}
		return out
	}

	cases := []struct {
		name          string
		steps         []ConfigStep
		before, after []string
	}{
		{"middle", []ConfigStep{cmd("a"), marker, cmd("b")}, []string{"a"}, []string{"b"}},
		{"no marker (region appends at end)", []ConfigStep{cmd("a"), cmd("b")}, []string{"a", "b"}, []string{}},
		{"marker first", []ConfigStep{marker, cmd("a")}, []string{}, []string{"a"}},
		{"marker last", []ConfigStep{cmd("a"), marker}, []string{"a"}, []string{}},
		{"duplicate markers dropped", []ConfigStep{cmd("a"), marker, cmd("b"), marker, cmd("c")}, []string{"a"}, []string{"b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before, after := SplitAtRegionMarker(tc.steps)
			if got := lines(before); !strings.EqualFold(strings.Join(got, ","), strings.Join(tc.before, ",")) {
				t.Errorf("before = %v, want %v", got, tc.before)
			}
			if got := lines(after); !strings.EqualFold(strings.Join(got, ","), strings.Join(tc.after, ",")) {
				t.Errorf("after = %v, want %v", got, tc.after)
			}
		})
	}
}

// TestListRepeaterConfigOrgs covers which orgs surface in the console config
// picker: the repeater must participate (owner is a member, not excluded) and the
// org must have config. Profile names come back per org (empty for region-only).
func TestListRepeaterConfigOrgs(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "rco-owner", "")
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := st.CreateUser(ctx, "rco-stranger", "")
	if err != nil {
		t.Fatal(err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("d", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A: owner is a member, has profiles → included, with profile names.
	orgA, _ := st.CreateOrg(ctx, "Alpha", owner.ID)
	if err := st.ReplaceOrgConfig(ctx, orgA.ID,
		[]ProfileInput{{Name: "ESP32"}, {Name: "nRF52"}}, nil); err != nil {
		t.Fatal(err)
	}
	// B: owner is a member, region-only config (no profiles) → included, empty profiles.
	orgB, _ := st.CreateOrg(ctx, "Bravo", owner.ID)
	if err := st.ReplaceRegions(ctx, orgB.ID,
		[]RegionInput{{Token: "x", DisplayName: "X", Layer: 1}}, true); err != nil {
		t.Fatal(err)
	}
	// C: owner is a member but the org has NO config → excluded.
	st.CreateOrg(ctx, "Charlie", owner.ID)
	// D: owner is a member, org has config, but the repeater is opted out → excluded.
	orgD, _ := st.CreateOrg(ctx, "Delta", owner.ID)
	if err := st.ReplaceOrgConfig(ctx, orgD.ID, []ProfileInput{{Name: "P"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepeaterOrgExcluded(ctx, orgD.ID, rep.ID, true); err != nil {
		t.Fatal(err)
	}
	// E: has config but the repeater's owner is NOT a member → excluded.
	orgE, _ := st.CreateOrg(ctx, "Echo", stranger.ID)
	if err := st.ReplaceOrgConfig(ctx, orgE.ID, []ProfileInput{{Name: "P"}}, nil); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListRepeaterConfigOrgs(ctx, rep.ID)
	if err != nil {
		t.Fatalf("ListRepeaterConfigOrgs: %v", err)
	}
	byName := map[string]RepeaterConfigOrg{}
	for _, o := range got {
		byName[o.OrgName] = o
	}
	if len(got) != 2 {
		t.Fatalf("orgs = %d %v, want 2 (Alpha, Bravo)", len(got), byName)
	}
	if a, ok := byName["Alpha"]; !ok {
		t.Error("Alpha missing")
	} else if len(a.Profiles) != 2 || a.Profiles[0] != "ESP32" || a.Profiles[1] != "nRF52" {
		t.Errorf("Alpha profiles = %v, want [ESP32 nRF52]", a.Profiles)
	}
	if b, ok := byName["Bravo"]; !ok {
		t.Error("Bravo (region-only) missing")
	} else if len(b.Profiles) != 0 {
		t.Errorf("Bravo profiles = %v, want empty", b.Profiles)
	}
	for _, absent := range []string{"Charlie", "Delta", "Echo"} {
		if _, ok := byName[absent]; ok {
			t.Errorf("%s should not be listed", absent)
		}
	}
}
