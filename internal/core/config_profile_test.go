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

func TestRegionGeofence(t *testing.T) {
	t.Parallel()

	// All-blank box → everywhere (nil geofence, ok).
	if gf, ok := regionGeofence(configRegionView{}, "z", new([]string)); !ok || gf != nil {
		t.Fatalf("blank box: got (%v,%v), want (nil,true)", gf, ok)
	}

	// Full box → a parseable polygon containing its center.
	var errs []string
	gf, ok := regionGeofence(configRegionView{MinLat: "10", MinLon: "30", MaxLat: "20", MaxLon: "40"}, "z", &errs)
	if !ok || gf == nil {
		t.Fatalf("full box: got (%v,%v), want non-nil geofence ok", gf, ok)
	}
	shape, err := geo.Parse(gf)
	if err != nil {
		t.Fatalf("parse built geofence: %v", err)
	}
	if !shape.Contains(15, 35) {
		t.Fatalf("built geofence should contain its center")
	}

	// Partial box → error.
	errs = nil
	if _, ok := regionGeofence(configRegionView{MinLat: "10"}, "z", &errs); ok || len(errs) == 0 {
		t.Fatalf("partial box should be rejected with an error")
	}

	// Non-numeric coordinate → error.
	errs = nil
	if _, ok := regionGeofence(configRegionView{MinLat: "x", MinLon: "30", MaxLat: "20", MaxLon: "40"}, "z", &errs); ok || len(errs) == 0 {
		t.Fatalf("invalid coordinate should be rejected with an error")
	}
}
