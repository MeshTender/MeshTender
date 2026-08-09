package web

import (
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// lines flattens an assembled preview into "kind:text" tokens, so a test can assert
// ordering and provenance in one comparison.
func lines(t *testing.T, cv ConfigView) []string {
	t.Helper()
	var out []string
	for _, l := range cv.AssembledLines() {
		switch {
		case l.IsMarker:
			out = append(out, "marker:")
		case l.IsComment:
			out = append(out, "note:"+l.Text)
		case l.FromRegion:
			out = append(out, "region:"+l.Text)
		default:
			out = append(out, "cmd:"+l.Text)
		}
	}
	return out
}

func eq(t *testing.T, got, want []string, label string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("%s:\n got %v\nwant %v", label, got, want)
	}
}

// TestAssembledLinesSplicesAtMarker: region commands land where the profile's
// {{ region }} marker sits, and the marker itself never reaches the page as literal
// text.
func TestAssembledLinesSplicesAtMarker(t *testing.T) {
	t.Parallel()
	cv := ConfigView{
		SelectedSteps: []store.ConfigStep{
			{CommandLine: "set tx 22"},
			{Comment: "tune later"},
			{CommandLine: store.RegionMarker},
			{CommandLine: "set repeat on"},
		},
		RegionDef: []string{"region def us", "region save"},
	}
	eq(t, lines(t, cv), []string{
		"cmd:set tx 22",
		"note:tune later",
		"region:region def us",
		"region:region save",
		"cmd:set repeat on",
	}, "spliced at marker")

	for _, l := range cv.AssembledLines() {
		if strings.Contains(l.Text, "{{") {
			t.Errorf("the region marker leaked into the preview as %q", l.Text)
		}
	}
}

// TestAssembledLinesAppendsWithoutMarker: with no marker the region block follows
// every profile step — the documented default.
func TestAssembledLinesAppendsWithoutMarker(t *testing.T) {
	t.Parallel()
	cv := ConfigView{
		SelectedSteps: []store.ConfigStep{{CommandLine: "set tx 22"}, {CommandLine: "set repeat on"}},
		RegionDef:     []string{"region def us", "region save"},
	}
	eq(t, lines(t, cv), []string{
		"cmd:set tx 22",
		"cmd:set repeat on",
		"region:region def us",
		"region:region save",
	}, "appended without marker")
}

// TestAssembledLinesWithoutLocation: with no location picked there are no region
// commands. A profile that has a marker keeps it as a placeholder the page can
// explain; one without a marker simply shows its steps.
func TestAssembledLinesWithoutLocation(t *testing.T) {
	t.Parallel()
	withMarker := ConfigView{SelectedSteps: []store.ConfigStep{
		{CommandLine: "set tx 22"},
		{CommandLine: store.RegionMarker},
		{CommandLine: "set repeat on"},
	}}
	eq(t, lines(t, withMarker), []string{"cmd:set tx 22", "marker:", "cmd:set repeat on"},
		"no location, marker present")

	noMarker := ConfigView{SelectedSteps: []store.ConfigStep{{CommandLine: "set tx 22"}}}
	eq(t, lines(t, noMarker), []string{"cmd:set tx 22"}, "no location, no marker")

	// Region-only org: nothing selected, but a picked location still previews.
	regionOnly := ConfigView{RegionDef: []string{"region def us", "region save"}}
	eq(t, lines(t, regionOnly), []string{"region:region def us", "region:region save"},
		"regions with no profile")

	// Empty config assembles to nothing rather than a stray marker.
	var empty ConfigView
	if got := empty.AssembledLines(); len(got) != 0 {
		t.Errorf("empty config assembled to %v, want nothing", got)
	}
}

// TestAssembledLinesMarkerOnlyProfile: a profile that is nothing but the marker
// yields exactly the region block.
func TestAssembledLinesMarkerOnlyProfile(t *testing.T) {
	t.Parallel()
	cv := ConfigView{
		SelectedSteps: []store.ConfigStep{{CommandLine: store.RegionMarker}},
		RegionDef:     []string{"region def us"},
	}
	eq(t, lines(t, cv), []string{"region:region def us"}, "marker-only profile")
}

// TestRegionColorWrapsAround: every region gets a color, and the palette repeats
// rather than running out, so the legend swatch always matches a drawn polygon.
func TestRegionColorWrapsAround(t *testing.T) {
	t.Parallel()
	if RegionColor(0) != RegionPalette[0] {
		t.Fatalf("RegionColor(0) = %q, want %q", RegionColor(0), RegionPalette[0])
	}
	n := len(RegionPalette)
	if RegionColor(n) != RegionPalette[0] || RegionColor(n+2) != RegionPalette[2] {
		t.Fatalf("palette did not wrap: %q, %q", RegionColor(n), RegionColor(n+2))
	}
	for i := 0; i < n*2; i++ {
		if RegionColor(i) == "" {
			t.Fatalf("RegionColor(%d) is empty", i)
		}
	}
}
