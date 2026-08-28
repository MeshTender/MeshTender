//go:build browser

package e2e

import (
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EBasemapToggleKeepsOverlays covers the sharpest edge in the MapLibre
// migration.
//
// Switching basemap is map.setStyle(), and setStyle discards every source and layer
// the page added along with the old style — so the repeaters, the region outlines,
// everything we drew, silently vanish the first time someone presses Light. Nothing
// errors; the map just goes empty. basemap.js exists partly to prevent that: it owns
// the swap and re-runs the page's `overlays` callback afterwards.
//
// The test presses Light for real and asserts the points came back, then checks the
// choice was remembered — the whole point of the control being sticky across pages.
func TestE2EBasemapToggleKeepsOverlays(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ebasemap")

	rep := srv.newRepeater(t, user.ID, "Toggle Node")
	if err := srv.store.SetRepeaterLocation(srv.ctx, rep.ID, 42.9, -78.8); err != nil {
		t.Fatalf("set location: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var before, after sourceCounts
	var styleBefore, styleAfter, remembered string
	pageURL := srv.appURL + "/repeaters/" + rep.PublicID

	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(pageURL),
		mapReady("map", "points"),
		mapEval("map", countIn("points"), &before),
		mapEval("map", `return map.getStyle().name;`, &styleBefore),

		// Press Light, then wait for the new style to finish loading *and* the
		// overlays to be put back on it.
		chromedp.Click(`.mesh-basemap-ctrl button[data-basemap="light"]`, chromedp.ByQuery),
		chromedp.Poll(`(function () {
			var m = window.MESHTENDER_MAPS["map"].map;
			return m.isStyleLoaded() && m.getStyle().name !== `+"`"+`Dark Matter`+"`"+` && !!m.getLayer("points");
		})()`, nil, chromedp.WithPollingTimeout(30*time.Second)),

		mapEval("map", countIn("points"), &after),
		mapEval("map", `return map.getStyle().name;`, &styleAfter),
		chromedp.Evaluate(`localStorage.getItem("mt_map_base")`, &remembered),
	); err != nil {
		t.Fatalf("browser run against %s: %v", pageURL, err)
	}

	if styleBefore == styleAfter {
		t.Errorf("basemap style is still %q after pressing Light; the swap did not happen", styleAfter)
	}
	if before.Features == 0 {
		t.Fatal("no repeater point rendered before the swap; the rest of this test proves nothing")
	}
	if after.Features != before.Features {
		t.Errorf("after switching basemap the map has %d points, want the %d it started with — "+
			"setStyle discards page layers, so basemap.js must re-add them", after.Features, before.Features)
	}
	if remembered != "light" {
		t.Errorf("localStorage mt_map_base = %q, want %q so the choice carries to the next map", remembered, "light")
	}
	watch.assertClean(t)
}
