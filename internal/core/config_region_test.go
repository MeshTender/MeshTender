package core

import (
	"testing"

	"github.com/jleight/meshtender/internal/geo"
)

func TestRegionGeofence(t *testing.T) {
	t.Parallel()

	// No shape → a draft (nil geofence, accepted). The region simply applies
	// nowhere until an area is drawn.
	if gf, ok := regionGeofence("", new([]string)); !ok || gf != nil {
		t.Fatalf("empty shape: got (%v,%v), want (nil,true)", gf, ok)
	}
	// Whitespace-only is the same thing (a cleared hidden input).
	if gf, ok := regionGeofence("   ", new([]string)); !ok || gf != nil {
		t.Fatalf("blank shape: got (%v,%v), want (nil,true)", gf, ok)
	}

	// A drawn polygon → carried through verbatim, parseable, contains its center.
	drawn := `{"type":"Polygon","coordinates":[[[30,10],[40,10],[40,20],[30,20],[30,10]]]}`
	var errs []string
	gf, ok := regionGeofence(drawn, &errs)
	if !ok || gf == nil {
		t.Fatalf("drawn polygon: got (%v,%v), want non-nil geofence ok", gf, ok)
	}
	shape, err := geo.Parse(gf)
	if err != nil {
		t.Fatalf("parse geofence: %v", err)
	}
	if !shape.Contains(15, 35) {
		t.Fatal("geofence should contain its center")
	}

	// Malformed GeoJSON → error.
	errs = nil
	if _, ok := regionGeofence("{not geojson", &errs); ok || len(errs) == 0 {
		t.Fatal("invalid shape should be rejected with an error")
	}

	// A non-area geometry is rejected too — geofences are Polygon/MultiPolygon.
	errs = nil
	if _, ok := regionGeofence(`{"type":"Point","coordinates":[30,10]}`, &errs); ok || len(errs) == 0 {
		t.Fatal("a Point should be rejected as a region area")
	}
}

func TestValidRegionToken(t *testing.T) {
	t.Parallel()
	for _, tok := range []string{"buf", "us-ny", "us_ny", "L3", "0"} {
		if !validRegionToken(tok) {
			t.Errorf("validRegionToken(%q) = false, want true", tok)
		}
	}
	// Tokens are space-joined into one `region def` line, so separators and spaces
	// would corrupt the command.
	for _, tok := range []string{"", "two words", "a|b", "a,b", "a/b", "né"} {
		if validRegionToken(tok) {
			t.Errorf("validRegionToken(%q) = true, want false", tok)
		}
	}
}

func TestRegionFormAction(t *testing.T) {
	t.Parallel()
	if got, want := regionFormAction("acme", 0), "/orgs/acme/config/regions"; got != want {
		t.Errorf("new region action = %q, want %q", got, want)
	}
	if got, want := regionFormAction("acme", 7), "/orgs/acme/config/regions/7"; got != want {
		t.Errorf("existing region action = %q, want %q", got, want)
	}
}
