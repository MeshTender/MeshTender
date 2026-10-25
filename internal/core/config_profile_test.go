package core

import (
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/store"
)

// configCatalog reuses the resolver test catalog plus a risky command, so the
// step parser can be exercised without a database.
func configCatalog() []*store.Command {
	c := resolveTestCatalog()
	c = append(c, &store.Command{Key: "poweroff", Template: "poweroff", Arity: 0, Risky: true})
	return c
}

func TestParseConfigSteps(t *testing.T) {
	t.Parallel()
	catalog := configCatalog()
	text := strings.Join([]string{
		"set tx 22",
		"# this is a note",
		"",
		"region put Metro",
		"poweroff",       // risky but valid
		"set nonsense 1", // unknown
	}, "\n")

	var errs, risky []string
	persist := parseConfigSteps(text, catalog, nil, "base", &errs, &risky)

	// One unknown command recorded.
	if len(errs) != 1 || !strings.Contains(errs[0], "set nonsense 1") {
		t.Fatalf("errs = %v, want one unknown-command error", errs)
	}
	// The risky command is flagged.
	if len(risky) != 1 || risky[0] != "poweroff" {
		t.Fatalf("risky = %v, want [poweroff]", risky)
	}
	// Persisted: set tx, comment, region put, poweroff = 4 (unknown line dropped).
	if len(persist) != 4 {
		t.Fatalf("persisted %d steps, want 4: %+v", len(persist), persist)
	}
	if !persist[1].IsComment() || persist[1].Comment != "this is a note" {
		t.Fatalf("step 2 should be the comment, got %+v", persist[1])
	}
	if persist[0].CommandID == nil {
		t.Fatalf("valid command should carry a resolved command id")
	}
}

func TestParseConfigStepsRegionMarker(t *testing.T) {
	t.Parallel()
	catalog := configCatalog()

	// A marker (in flexible spelling) becomes one marker step that canonicalizes
	// and round-trips; it is neither a comment nor a runnable command.
	var errs, risky []string
	persist := parseConfigSteps("set tx 22\n{{region}}\nset tx 23", catalog, nil, "base", &errs, &risky)
	if len(errs) != 0 {
		t.Fatalf("errs = %v, want none", errs)
	}
	if len(persist) != 3 || !persist[1].IsRegionMarker() {
		t.Fatalf("steps = %+v, want a region marker at index 1", persist)
	}
	if persist[1].CommandLine != store.RegionMarker {
		t.Fatalf("marker canonical form = %q, want %q", persist[1].CommandLine, store.RegionMarker)
	}
	if got := stepsToText(persist); !strings.Contains(got, store.RegionMarker) {
		t.Fatalf("round-trip lost the marker: %q", got)
	}

	// A second marker is rejected.
	errs, risky = nil, nil
	_ = parseConfigSteps("{{ region }}\n{{ REGION }}", catalog, nil, "base", &errs, &risky)
	if len(errs) != 1 || !strings.Contains(errs[0], "once") {
		t.Fatalf("errs = %v, want one 'only once' error", errs)
	}
	_ = risky
}

// TestParseConfigStepsRadioPlaceholders: a profile references the org's radio
// through placeholders, stored canonical and validated with the real values
// filled in — and refused while the org has no radio to fill them from.
func TestParseConfigStepsRadioPlaceholders(t *testing.T) {
	t.Parallel()
	catalog := configCatalog()
	radio := &config.RadioDefaults{FreqHz: 910_525_000, BwHz: 62_500, SF: 7, CR: 5}
	line := "set radio {{radio.freq}},{{ RADIO.BW }},{{ radio.sf  }},{{radio.cr }}"

	var errs, risky []string
	persist := parseConfigSteps(line, catalog, radio, "base", &errs, &risky)
	if len(errs) != 0 || len(persist) != 1 {
		t.Fatalf("errs = %v, steps = %+v", errs, persist)
	}
	want := "set radio {{ radio.freq }},{{ radio.bw }},{{ radio.sf }},{{ radio.cr }}"
	if persist[0].CommandLine != want || persist[0].CommandID == nil {
		t.Fatalf("step = %+v, want canonical %q with a command id", persist[0], want)
	}

	// No org radio: nothing to fill in, so the line is rejected.
	errs = nil
	if got := parseConfigSteps(line, catalog, nil, "base", &errs, &risky); len(got) != 0 ||
		len(errs) != 1 || !strings.Contains(errs[0], "aren't set") {
		t.Fatalf("without a radio: steps %+v, errs %v", got, errs)
	}

	// A misspelled name, and the region marker used inline, are both unknown.
	for _, bad := range []string{"set radio {{ radio.frequency }},62.5,7,5", "set tx {{ region }}"} {
		errs = nil
		if got := parseConfigSteps(bad, catalog, radio, "base", &errs, &risky); len(got) != 0 ||
			len(errs) != 1 || !strings.Contains(errs[0], "unknown placeholder") {
			t.Errorf("%q: steps %+v, errs %v", bad, got, errs)
		}
	}
}
