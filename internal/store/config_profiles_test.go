package store

import (
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/geo"
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
	remaining, err := st.ListProfiles(ctx, org.ID)
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Name != "Second" {
		t.Fatalf("after delete, profiles = %+v", remaining)
	}
}

// TestRegionWritesKeepProfiles: profiles and regions are independent halves of an
// org's config, so writing regions (or the root flood policy) never disturbs them.
func TestRegionWritesKeepProfiles(t *testing.T) {
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

	country, err := st.CreateRegion(ctx, org.ID, RegionInput{Token: "country", DisplayName: "Country", Layer: 1, AllowFlood: true})
	if err != nil {
		t.Fatalf("create country: %v", err)
	}
	if _, err := st.CreateRegion(ctx, org.ID, RegionInput{
		Token: "metro", DisplayName: "Metro", Layer: 2, Primary: true, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(10, 30, 20, 40),
	}); err != nil {
		t.Fatalf("create metro: %v", err)
	}
	if err := st.SetRootAllowFlood(ctx, org.ID, false); err != nil {
		t.Fatalf("set root flood: %v", err)
	}
	if allow, err := st.RootAllowFlood(ctx, org.ID); err != nil || allow {
		t.Fatalf("root allow flood = %v (err %v), want false", allow, err)
	}

	gotR, err := st.ListRegions(ctx, org.ID)
	if err != nil {
		t.Fatalf("list regions: %v", err)
	}
	if len(gotR) != 2 {
		t.Fatalf("regions = %d, want 2", len(gotR))
	}
	gotP, err := st.ListProfiles(ctx, org.ID)
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(gotP) != 1 || gotP[0].Name != "Keep" {
		t.Fatalf("profiles after region writes = %+v, want [Keep]", gotP)
	}

	// Deleting every region likewise leaves the profiles alone.
	if err := st.DeleteRegion(ctx, org.ID, country); err != nil {
		t.Fatal(err)
	}
	for _, z := range gotR {
		_ = st.DeleteRegion(ctx, org.ID, z.ID)
	}
	if gotR, _ = st.ListRegions(ctx, org.ID); len(gotR) != 0 {
		t.Fatalf("regions after delete = %d, want 0", len(gotR))
	}
	if gotP, _ = st.ListProfiles(ctx, org.ID); len(gotP) != 1 {
		t.Fatalf("profiles after region delete = %d, want 1", len(gotP))
	}
}

// TestRegionCRUD covers the per-region write path the region editor uses: one row
// at a time, with the geofence saved separately from the attributes.
func TestRegionCRUD(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, err := st.CreateUser(ctx, "rgncrud", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "RgnCrudOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Created without an area: a draft. Geofence stays NULL rather than becoming an
	// empty shape, so RegionMatches keeps it out of every region def.
	id, err := st.CreateRegion(ctx, org.ID, RegionInput{
		Token: "buf", DisplayName: "Buffalo", Layer: 3, AllowFlood: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.GetRegion(ctx, org.ID, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Token != "buf" || got.DisplayName != "Buffalo" || got.Layer != 3 || !got.AllowFlood {
		t.Fatalf("get region = %+v", got)
	}
	if got.Geofence != nil || got.GeofenceJSON != nil {
		t.Fatalf("draft region has a shape: %+v", got)
	}

	// Token collides within the org.
	if _, err := st.CreateRegion(ctx, org.ID, RegionInput{Token: "buf", DisplayName: "Dup"}); err != ErrDuplicate {
		t.Fatalf("duplicate token err = %v, want ErrDuplicate", err)
	}

	// Another org can't see or write it.
	other, err := st.CreateOrg(ctx, "OtherRgnOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetRegion(ctx, other.ID, id); err != ErrNotFound {
		t.Fatalf("cross-org get err = %v, want ErrNotFound", err)
	}
	if err := st.UpdateRegion(ctx, other.ID, id, RegionInput{Token: "x"}); err != ErrNotFound {
		t.Fatalf("cross-org update err = %v, want ErrNotFound", err)
	}
	if err := st.UpdateRegionGeofence(ctx, other.ID, id, geo.Rectangle(0, 0, 1, 1)); err != ErrNotFound {
		t.Fatalf("cross-org geofence update err = %v, want ErrNotFound", err)
	}

	// Draw the area, then edit the attributes: the shape must survive an attribute
	// edit, since the two are saved by separate requests.
	box := geo.Rectangle(40, -80, 45, -75)
	if err := st.UpdateRegionGeofence(ctx, org.ID, id, box); err != nil {
		t.Fatalf("save geofence: %v", err)
	}
	if err := st.UpdateRegion(ctx, org.ID, id, RegionInput{
		Token: "buf", DisplayName: "Buffalo NY", Layer: 4, AllowFlood: false,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = st.GetRegion(ctx, org.ID, id)
	if got.DisplayName != "Buffalo NY" || got.Layer != 4 || got.AllowFlood {
		t.Fatalf("after attribute update = %+v", got)
	}
	if got.Geofence == nil || !got.Geofence.Contains(42, -78) {
		t.Fatalf("attribute update dropped the drawn area: %+v", got)
	}

	// Clearing the area returns it to a draft.
	if err := st.UpdateRegionGeofence(ctx, org.ID, id, nil); err != nil {
		t.Fatalf("clear geofence: %v", err)
	}
	if got, _ = st.GetRegion(ctx, org.ID, id); got.Geofence != nil {
		t.Fatalf("cleared area still present: %+v", got)
	}

	// Delete, then the idempotency guard.
	if err := st.DeleteRegion(ctx, org.ID, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := st.DeleteRegion(ctx, org.ID, id); err != ErrNotFound {
		t.Fatalf("re-delete err = %v, want ErrNotFound", err)
	}
}

// TestRegionPrimaryIsExclusive: at most one region per org is primary. This used to
// be enforced by the bulk editor's JavaScript clearing the other switches; now the
// write clears siblings in the same transaction, backed by a partial unique index.
func TestRegionPrimaryIsExclusive(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, err := st.CreateUser(ctx, "rgnprimary", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "RgnPrimaryOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A second org's primary must be untouched by any of this.
	otherOrg, err := st.CreateOrg(ctx, "RgnPrimaryOther", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRegion(ctx, otherOrg.ID, RegionInput{Token: "oth", DisplayName: "Other", Primary: true}); err != nil {
		t.Fatal(err)
	}

	primaryToken := func(orgID int64) string {
		t.Helper()
		regions, err := st.ListRegions(ctx, orgID)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		var found []string
		for _, z := range regions {
			if z.Primary {
				found = append(found, z.Token)
			}
		}
		if len(found) > 1 {
			t.Fatalf("org %d has %d primary regions: %v", orgID, len(found), found)
		}
		if len(found) == 0 {
			return ""
		}
		return found[0]
	}

	a, err := st.CreateRegion(ctx, org.ID, RegionInput{Token: "a", DisplayName: "A", Primary: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := primaryToken(org.ID); got != "a" {
		t.Fatalf("primary after first create = %q, want a", got)
	}

	// Creating a second primary demotes the first.
	if _, err := st.CreateRegion(ctx, org.ID, RegionInput{Token: "b", DisplayName: "B", Primary: true}); err != nil {
		t.Fatalf("create second primary: %v", err)
	}
	if got := primaryToken(org.ID); got != "b" {
		t.Fatalf("primary after second create = %q, want b", got)
	}

	// So does promoting one by update.
	if err := st.UpdateRegion(ctx, org.ID, a, RegionInput{Token: "a", DisplayName: "A", Primary: true}); err != nil {
		t.Fatalf("promote a: %v", err)
	}
	if got := primaryToken(org.ID); got != "a" {
		t.Fatalf("primary after promoting a = %q, want a", got)
	}

	// Turning the flag off leaves the org with none — it must not promote a sibling.
	if err := st.UpdateRegion(ctx, org.ID, a, RegionInput{Token: "a", DisplayName: "A", Primary: false}); err != nil {
		t.Fatalf("demote a: %v", err)
	}
	if got := primaryToken(org.ID); got != "" {
		t.Fatalf("primary after demoting a = %q, want none", got)
	}

	// The other org kept its own primary throughout.
	if got := primaryToken(otherOrg.ID); got != "oth" {
		t.Fatalf("other org's primary = %q, want oth", got)
	}
}

// TestRegionPrimaryIndexIsEnforced checks that one-primary-per-org survives a
// writer that doesn't go through CreateRegion/UpdateRegion, by inserting a second
// primary with raw SQL. CreateRegion clears siblings first, so this partial unique
// index (migration 0044) is otherwise never exercised — without this test nothing
// would notice if the migration were dropped.
func TestRegionPrimaryIndexIsEnforced(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, err := st.CreateUser(ctx, "rgnindex", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "RgnIndexOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw := func(token string) error {
		_, err := st.pool.Exec(ctx,
			`INSERT INTO config_regions (org_id, token, display_name, layer, is_primary, allow_flood)
			 VALUES ($1, $2, $2, 1, true, true)`, org.ID, token)
		return err
	}
	if err := raw("first"); err != nil {
		t.Fatalf("first raw primary insert: %v", err)
	}
	if err := raw("second"); !isUniqueViolation(err) {
		t.Fatalf("second raw primary insert err = %v, want a unique violation", err)
	}
}

// TestSetRootAllowFlood: the root (*) flood policy lives on the org, not as a
// region row, so it toggles independently of any region.
func TestSetRootAllowFlood(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, err := st.CreateUser(ctx, "rootflood", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "RootFloodOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Default is allow.
	if allow, err := st.RootAllowFlood(ctx, org.ID); err != nil || !allow {
		t.Fatalf("default root flood = (%v, %v), want (true, nil)", allow, err)
	}
	if err := st.SetRootAllowFlood(ctx, org.ID, false); err != nil {
		t.Fatalf("deny: %v", err)
	}
	if allow, err := st.RootAllowFlood(ctx, org.ID); err != nil || allow {
		t.Fatalf("after deny = (%v, %v), want (false, nil)", allow, err)
	}
	if err := st.SetRootAllowFlood(ctx, org.ID, true); err != nil {
		t.Fatalf("allow: %v", err)
	}
	if allow, _ := st.RootAllowFlood(ctx, org.ID); !allow {
		t.Fatal("root flood did not toggle back to allow")
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

// TestRegionDefIgnoresDraftRegions: a draft region — one created but whose area
// hasn't been drawn yet, so its geofence is NULL — must apply nowhere. Regression
// test: RegionMatches used to report a nil geofence as a match, which made a
// shapeless row apply to every repeater everywhere and inject its token into every
// region def chain. "Everywhere" is the org root (*), which lives on the
// organizations row, never as a config_regions row.
func TestRegionDefIgnoresDraftRegions(t *testing.T) {
	t.Parallel()

	mk := func(token string, layer int, box []byte) Region {
		shape, err := geo.Parse(box)
		if err != nil {
			t.Fatalf("parse %s: %v", token, err)
		}
		return Region{Token: token, Layer: layer, AllowFlood: true, Geofence: shape}
	}
	// draft sits at layer 2 with no shape — between us (L1) and buf (L3), so if it
	// matched it would also wedge itself into the middle of the chain.
	regions := []Region{
		mk("us", 1, geo.Rectangle(0, 0, 100, 100)),
		mk("draft", 2, nil),
		mk("buf", 3, geo.Rectangle(12, 12, 18, 18)),
	}

	// Inside us + buf: the draft must not appear, and must not re-parent buf.
	got := RegionDefCommands(regions, true, ptr(15.0), ptr(15.0))
	if len(got) == 0 || got[0] != "region def us buf" {
		t.Fatalf("inside = %v, want first line %q", got, "region def us buf")
	}
	for _, line := range got {
		if strings.Contains(line, "draft") {
			t.Errorf("draft region leaked into %q (full %v)", line, got)
		}
	}

	// Outside every drawn area: no commands at all. A draft must not be the reason
	// a config gets emitted — that's the `denyf *` safety guard.
	if got := RegionDefCommands(regions, true, ptr(-5.0), ptr(-5.0)); got != nil {
		t.Fatalf("outside with a draft present = %v, want nil", got)
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
	orgA, err := st.CreateOrg(ctx, "Alpha", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := st.ReplaceOrgConfig(ctx, orgA.ID,
		[]ProfileInput{{Name: "ESP32"}, {Name: "nRF52"}}, nil); err != nil {
		t.Fatal(err)
	}
	// B: owner is a member, region-only config (no profiles) → included, empty profiles.
	orgB, err := st.CreateOrg(ctx, "Bravo", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := st.CreateRegion(ctx, orgB.ID, RegionInput{Token: "x", DisplayName: "X", Layer: 1}); err != nil {
		t.Fatal(err)
	}
	// C: owner is a member but the org has NO config → excluded.
	st.CreateOrg(ctx, "Charlie", owner.ID)
	// D: owner is a member, org has config, but the repeater is opted out → excluded.
	orgD, err := st.CreateOrg(ctx, "Delta", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := st.ReplaceOrgConfig(ctx, orgD.ID, []ProfileInput{{Name: "P"}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRepeaterOrgExcluded(ctx, orgD.ID, rep.ID, true); err != nil {
		t.Fatal(err)
	}
	// E: has config but the repeater's owner is NOT a member → excluded.
	orgE, err := st.CreateOrg(ctx, "Echo", stranger.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
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
