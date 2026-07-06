package core

import (
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/geo"
	"github.com/jleight/meshtender/internal/store"
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
	_, persist := parseConfigSteps(text, catalog, "base", &errs, &risky)

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
	_, persist := parseConfigSteps("set tx 22\n{{region}}\nset tx 23", catalog, "base", &errs, &risky)
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
	_, _ = parseConfigSteps("{{ region }}\n{{ REGION }}", catalog, "base", &errs, &risky)
	if len(errs) != 1 || !strings.Contains(errs[0], "once") {
		t.Fatalf("errs = %v, want one 'only once' error", errs)
	}
	_ = risky
}

func TestRegionGeofence(t *testing.T) {
	t.Parallel()

	// Empty shape → everywhere (nil geofence, ok).
	if gf, ok := regionGeofence(configRegionView{}, "z", new([]string)); !ok || gf != nil {
		t.Fatalf("empty shape: got (%v,%v), want (nil,true)", gf, ok)
	}

	// A drawn polygon → carried through verbatim, parseable, contains its center.
	drawn := `{"type":"Polygon","coordinates":[[[30,10],[40,10],[40,20],[30,20],[30,10]]]}`
	var errs []string
	gf, ok := regionGeofence(configRegionView{GeofenceJSON: drawn}, "z", &errs)
	if !ok || gf == nil {
		t.Fatalf("drawn polygon: got (%v,%v), want non-nil geofence ok", gf, ok)
	}
	shape, err := geo.Parse(gf)
	if err != nil {
		t.Fatalf("parse geofence: %v", err)
	}
	if !shape.Contains(15, 35) {
		t.Fatalf("geofence should contain its center")
	}

	// Malformed GeoJSON → error.
	errs = nil
	if _, ok := regionGeofence(configRegionView{GeofenceJSON: "{not geojson"}, "z", &errs); ok || len(errs) == 0 {
		t.Fatalf("invalid shape should be rejected with an error")
	}
}
