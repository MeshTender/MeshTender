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
	if got, ok := ExpandPlaceholders(line, &usca); !ok || got != "set radio 910.525,62.5,7,5" {
		t.Fatalf("expand = %q, %v", got, ok)
	}
	// Whole-number values carry no trailing zeros, like the firmware prints them.
	r := config.RadioDefaults{FreqHz: 869_000_000, BwHz: 250_000, SF: 11, CR: 8}
	if got, _ := ExpandPlaceholders(line, &r); got != "set radio 869,250,11,8" {
		t.Fatalf("expand = %q", got)
	}
	if got, ok := ExpandPlaceholders(line, nil); ok || got != line {
		t.Fatalf("no radio: %q, %v; want the line untouched and false", got, ok)
	}
	if got, ok := ExpandPlaceholders("set tx 22", nil); !ok || got != "set tx 22" {
		t.Fatalf("no placeholders needs no radio: %q, %v", got, ok)
	}
	if _, ok := ExpandPlaceholders("set freq {{ radio.nope }}", &usca); ok {
		t.Fatal("an unknown name must not expand")
	}

	steps := []ConfigStep{{Comment: "{{ radio.freq }} in a note stays"}, {CommandLine: RegionMarker}, {CommandLine: "set freq {{ radio.freq }}"}}
	out, ok := ExpandSteps(steps, &usca)
	if !ok || out[0].Comment != steps[0].Comment || out[1].CommandLine != RegionMarker || out[2].CommandLine != "set freq 910.525" {
		t.Fatalf("ExpandSteps = %+v, %v", out, ok)
	}
	if steps[2].CommandLine != "set freq {{ radio.freq }}" {
		t.Fatal("ExpandSteps modified its input")
	}
	if _, ok := ExpandSteps(steps, nil); ok {
		t.Fatal("ExpandSteps without a radio should report the unfilled step")
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
