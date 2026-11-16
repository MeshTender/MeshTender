package store

import (
	"errors"
	"reflect"
	"testing"

	"github.com/MeshTender/MeshTender/internal/config"
)

var usca = config.RadioDefaults{FreqHz: 910_525_000, BwHz: 62_500, SF: 7, CR: 5}

func TestCanonicalizePlaceholders(t *testing.T) {
	t.Parallel()
	got, unknown := CanonicalizePlaceholders("set radio {{radio.freq}},{{ RADIO.BW }},{{  radio.sf}},{{radio.cr  }}")
	if want := "set radio {{ radio.freq }},{{ radio.bw }},{{ radio.sf }},{{ radio.cr }}"; got != want || unknown != nil {
		t.Fatalf("got %q %v, want %q", got, unknown, want)
	}
	got, unknown = CanonicalizePlaceholders("set freq {{ radio.frq }} {{region}}")
	if got != "set freq {{ radio.frq }} {{region}}" || !reflect.DeepEqual(unknown, []string{"{{ radio.frq }}", "{{region}}"}) {
		t.Fatalf("unknown names: got %q %v", got, unknown)
	}
}

func TestExpandPlaceholders(t *testing.T) {
	t.Parallel()
	line := "set radio {{ radio.freq }},{{ radio.bw }},{{ radio.sf }},{{ radio.cr }}"
	if got, ok := ExpandPlaceholders(line, OrgMesh{Radio: &usca}); !ok || got != "set radio 910.525,62.5,7,5" {
		t.Fatalf("expand = %q, %v", got, ok)
	}
	// Whole-number values carry no trailing zeros, like the firmware prints them.
	r := config.RadioDefaults{FreqHz: 869_000_000, BwHz: 250_000, SF: 11, CR: 8}
	if got, _ := ExpandPlaceholders(line, OrgMesh{Radio: &r}); got != "set radio 869,250,11,8" {
		t.Fatalf("expand = %q", got)
	}
	if got, ok := ExpandPlaceholders(line, OrgMesh{}); ok || got != line {
		t.Fatalf("no radio: %q, %v; want the line untouched and false", got, ok)
	}
	if got, ok := ExpandPlaceholders("set tx 22", OrgMesh{}); !ok || got != "set tx 22" {
		t.Fatalf("no placeholders needs no radio: %q, %v", got, ok)
	}
	if _, ok := ExpandPlaceholders("set freq {{ radio.nope }}", OrgMesh{Radio: &usca}); ok {
		t.Fatal("an unknown name must not expand")
	}

	steps := []ConfigStep{{Comment: "{{ radio.freq }} in a note stays"}, {CommandLine: RegionMarker}, {CommandLine: "set freq {{ radio.freq }}"}}
	out, ok := ExpandSteps(steps, OrgMesh{Radio: &usca})
	if !ok || out[0].Comment != steps[0].Comment || out[1].CommandLine != RegionMarker || out[2].CommandLine != "set freq 910.525" {
		t.Fatalf("ExpandSteps = %+v, %v", out, ok)
	}
	if steps[2].CommandLine != "set freq {{ radio.freq }}" {
		t.Fatal("ExpandSteps modified its input")
	}
	if _, ok := ExpandSteps(steps, OrgMesh{}); ok {
		t.Fatal("ExpandSteps without a radio should report the unfilled step")
	}
}

// TestExpandPathHashPlaceholder: {{ path.hash.mode }} fills in the org's mode as
// the number `set path.hash.mode` takes, needs only the mode (not the radio), and
// leaves the line unfilled while the org hasn't set one.
func TestExpandPathHashPlaceholder(t *testing.T) {
	t.Parallel()
	line := "set path.hash.mode {{ path.hash.mode }}"
	if got, unknown := CanonicalizePlaceholders("set path.hash.mode {{PATH.HASH.MODE}}"); got != line || unknown != nil {
		t.Fatalf("canonical = %q %v, want %q", got, unknown, line)
	}
	two := 1
	if got, ok := ExpandPlaceholders(line, OrgMesh{PathHashMode: &two}); !ok || got != "set path.hash.mode 1" {
		t.Fatalf("expand = %q, %v", got, ok)
	}
	if got, ok := ExpandPlaceholders(line, OrgMesh{Radio: &usca}); ok || got != line {
		t.Fatalf("no mode: %q, %v; want the line untouched and false", got, ok)
	}
	mixed := "set radio {{ radio.freq }},62.5,7,5 {{ path.hash.mode }}"
	if _, ok := ExpandPlaceholders(mixed, OrgMesh{PathHashMode: &two}); ok {
		t.Fatal("a line needing the radio too must not expand without it")
	}
}

func TestOrgRadio(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, err := st.CreateUser(ctx, "radioowner", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "RadioOrg", owner.ID)
	if err != nil {
		t.Fatal(err)
	}

	if r, err := st.OrgRadio(ctx, org.ID); err != nil || r != nil {
		t.Fatalf("new org radio = %v, %v; want unset", r, err)
	}
	if has, _ := st.OrgHasConfig(ctx, org.ID); has {
		t.Fatal("a new org already has config")
	}
	if err := st.SetOrgRadio(ctx, org.ID, &usca); err != nil {
		t.Fatal(err)
	}
	if has, err := st.OrgHasConfig(ctx, org.ID); err != nil || !has {
		t.Fatalf("radio alone should count as recommended config: %v, %v", has, err)
	}
	if r, err := st.OrgRadio(ctx, org.ID); err != nil || r == nil || *r != usca {
		t.Fatalf("radio = %v, %v; want %v", r, err, usca)
	}

	// A profile referencing the radio blocks clearing it.
	pid, err := st.CreateProfile(ctx, org.ID, "P", []ConfigStep{{CommandLine: "set freq {{ radio.freq }}"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetOrgRadio(ctx, org.ID, nil); !errors.Is(err, ErrRadioInUse) {
		t.Fatalf("clear while used = %v, want ErrRadioInUse", err)
	}
	if r, _ := st.OrgRadio(ctx, org.ID); r == nil {
		t.Fatal("refused clear still cleared the radio")
	}

	// Once no profile uses it, it clears.
	if err := st.UpdateProfile(ctx, org.ID, pid, "P", []ConfigStep{{CommandLine: "set tx 22"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOrgRadio(ctx, org.ID, nil); err != nil {
		t.Fatalf("clear = %v", err)
	}
	if r, err := st.OrgRadio(ctx, org.ID); err != nil || r != nil {
		t.Fatalf("after clear = %v, %v", r, err)
	}

	// The schema refuses a half-set radio.
	if _, err := st.pool.Exec(ctx, `UPDATE organizations SET radio_freq_hz = 910525000 WHERE id = $1`, org.ID); err == nil {
		t.Fatal("a radio with only a frequency was accepted")
	}
}

// TestOrgPathHashMode: an org's path hash mode is unset until an admin picks 0–2,
// reads back through OrgMesh beside the radio, can't be 3 (reserved by the
// firmware) or more, and can't be cleared while a profile uses
// {{ path.hash.mode }}.
func TestOrgPathHashMode(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner := mustUser(t, st, ctx, "hashowner")
	org := mustOrg(t, st, ctx, "HashOrg", owner)

	if m, err := st.OrgMesh(ctx, org); err != nil || m.PathHashMode != nil || m.Radio != nil {
		t.Fatalf("new org mesh = %+v, %v; want nothing set", m, err)
	}
	mode := 1
	if err := st.SetOrgPathHashMode(ctx, org, &mode); err != nil {
		t.Fatal(err)
	}
	if m, err := st.OrgMesh(ctx, org); err != nil || m.PathHashMode == nil || *m.PathHashMode != 1 {
		t.Fatalf("mesh = %+v, %v; want mode 1", m, err)
	}
	for _, bad := range []int{-1, 3} {
		if err := st.SetOrgPathHashMode(ctx, org, &bad); err == nil {
			t.Errorf("mode %d was accepted", bad)
		}
	}
	if _, err := st.pool.Exec(ctx, `UPDATE organizations SET path_hash_mode = 3 WHERE id = $1`, org); err == nil {
		t.Error("the schema accepted the reserved mode 3")
	}

	pid, err := st.CreateProfile(ctx, org, "P", []ConfigStep{{CommandLine: "set path.hash.mode {{ path.hash.mode }}"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetOrgPathHashMode(ctx, org, nil); !errors.Is(err, ErrPathHashInUse) {
		t.Fatalf("clear while used = %v, want ErrPathHashInUse", err)
	}
	if err := st.UpdateProfile(ctx, org, pid, "P", []ConfigStep{{CommandLine: "set tx 22"}}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetOrgPathHashMode(ctx, org, nil); err != nil {
		t.Fatalf("clear = %v", err)
	}
	if m, err := st.OrgPathHashMode(ctx, org); err != nil || m != nil {
		t.Fatalf("after clear = %v, %v", m, err)
	}
}

// TestRegionDefaultScope: at most one of an org's regions is its default scope,
// separately from its primary region — claiming the flag takes it from the
// region that had it, and leaves the primary alone.
func TestRegionDefaultScope(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner := mustUser(t, st, ctx, "scopeowner")
	org := mustOrg(t, st, ctx, "ScopeOrg", owner)

	us, err := st.CreateRegion(ctx, org, RegionInput{Token: "us", DisplayName: "US", Layer: 1, Primary: true, DefaultScope: true})
	if err != nil {
		t.Fatal(err)
	}
	wny, err := st.CreateRegion(ctx, org, RegionInput{Token: "wny", DisplayName: "WNY", Layer: 2})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateRegion(ctx, org, wny, RegionInput{Token: "wny", DisplayName: "WNY", Layer: 2, DefaultScope: true}); err != nil {
		t.Fatal(err)
	}
	got := map[string]Region{}
	regions, err := st.ListRegions(ctx, org)
	if err != nil {
		t.Fatal(err)
	}
	for _, z := range regions {
		got[z.Token] = z
	}
	if got["us"].DefaultScope || !got["wny"].DefaultScope {
		t.Errorf("default scope: us %v, wny %v; want only wny", got["us"].DefaultScope, got["wny"].DefaultScope)
	}
	if !got["us"].Primary || got["wny"].Primary {
		t.Error("claiming the default scope moved the primary region")
	}
	if z, err := st.GetRegion(ctx, org, us); err != nil || z.DefaultScope {
		t.Fatalf("GetRegion(us) = %+v, %v; want no default scope", z, err)
	}
}
