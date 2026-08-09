//go:build browser

package e2e

import (
	"strconv"
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/geo"
	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2ERegionModalSave: an admin adds a region from the Configuration page — the
// attribute editor loads into the modal via htmx, saving navigates back, and the new
// region is listed flagged as needing an area (it has no shape yet). Runs under the
// strict CSP.
func TestE2ERegionModalSave(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ergnadd")
	org, err := srv.store.CreateOrg(srv.ctx, "Region Add Org", user.ID) // creator = admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var rowText string
	var draftFlagged bool
	cfgURL := srv.appURL + "/orgs/" + org.Slug + "/config"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(cfgURL),
		chromedp.WaitVisible(`[data-testid="config-region-add"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="config-region-add"]`, chromedp.ByQuery),
		// The attribute editor arrives in the modal via htmx.
		chromedp.WaitVisible(`#config-region-modal-content input[name=region_display]`, chromedp.ByQuery),
		chromedp.SetValue(`#config-region-modal-content input[name=region_display]`, "Buffalo", chromedp.ByQuery),
		chromedp.SetValue(`#config-region-modal-content input[name=region_token]`, "buf", chromedp.ByQuery),
		chromedp.SetValue(`#config-region-modal-content input[name=region_layer]`, "3", chromedp.ByQuery),
		chromedp.Click(`[data-testid="save-region"]`, chromedp.ByQuery),
		// HX-Redirect navigates back to the config page, now listing the region.
		chromedp.WaitVisible(`[data-testid="config-region-row"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="config-region-row"]`, &rowText, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-testid="config-region-draft"]')`, &draftFlagged),
	); err != nil {
		t.Fatalf("browser run against %s: %v", cfgURL, err)
	}
	for _, want := range []string{"Buffalo", "buf"} {
		if !strings.Contains(rowText, want) {
			t.Errorf("region row = %q, want it to mention %q", rowText, want)
		}
	}
	if !draftFlagged {
		t.Error("a region created without an area should be flagged as needing one")
	}

	// It really is a draft in the database — no geofence, so it applies nowhere.
	regions, err := srv.store.ListRegions(srv.ctx, org.ID)
	if err != nil || len(regions) != 1 {
		t.Fatalf("ListRegions = %v, %v; want one region", regions, err)
	}
	if regions[0].Geofence != nil {
		t.Errorf("region should have no area yet: %+v", regions[0])
	}
	watch.assertClean(t)
}

// TestE2EConfigMapShowsRegionsAndPicksLocation: the Configuration page draws every
// shaped region on its map, and clicking the map picks a location — after which the
// regions covering that point are marked in the legend and their commands appear in
// the assembled preview. This is the read path anonymous visitors and members use,
// so it runs signed-out against the root host.
func TestE2EConfigMapShowsRegionsAndPicksLocation(t *testing.T) {
	srv := newE2EServer(t)
	user, _ := srv.login(t, "e2ecfgmap")
	org, err := srv.store.CreateOrg(srv.ctx, "Config Map Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := srv.store.CreateProfile(srv.ctx, org.ID, "ESP32",
		[]store.ConfigStep{{CommandLine: "set tx 22"}}); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	// Two nested regions plus one far away, so a pin inside the inner pair marks
	// exactly two and leaves the third alone.
	for _, z := range []store.RegionInput{
		{Token: "us", DisplayName: "US", Layer: 1, AllowFlood: true, GeofenceJSON: geo.Rectangle(30, -90, 48, -70)},
		{Token: "us-ny", DisplayName: "New York", Layer: 2, AllowFlood: true, GeofenceJSON: geo.Rectangle(40, -80, 45, -73)},
		{Token: "far", DisplayName: "Far Away", Layer: 2, AllowFlood: true, GeofenceJSON: geo.Rectangle(0, 0, 5, 5)},
	} {
		if _, err := srv.store.CreateRegion(srv.ctx, org.ID, z); err != nil {
			t.Fatalf("create region %s: %v", z.Token, err)
		}
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	// Signed out, on the public root host.
	cfgURL := srv.rootURL + "/orgs/" + org.Slug + "/config"
	var polygons, rows int
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(cfgURL),
		chromedp.WaitVisible(`#region-map .leaflet-overlay-pane path`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('#region-map .leaflet-overlay-pane path').length`, &polygons),
		chromedp.Evaluate(`document.querySelectorAll('[data-testid="config-region-row"]').length`, &rows),
	); err != nil {
		t.Fatalf("browser run against %s: %v", cfgURL, err)
	}
	if polygons != 3 {
		t.Errorf("drew %d polygons, want 3 (one per shaped region)", polygons)
	}
	if rows != 3 {
		t.Errorf("legend has %d rows, want 3", rows)
	}

	// Picking a location: the preview page marks the covering regions and assembles
	// their commands with the profile's steps.
	var matches int
	var commands string
	pinned := cfgURL + "?profile=ESP32&lat=42&lon=-78"
	if err := chromedp.Run(bctx,
		chromedp.Navigate(pinned),
		chromedp.WaitVisible(`[data-testid="config-commands"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('[data-testid="config-region-match"]').length`, &matches),
		chromedp.Text(`[data-testid="config-commands"]`, &commands, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", pinned, err)
	}
	if matches != 2 {
		t.Errorf("%d regions marked as applying, want 2 (us and us-ny, not far)", matches)
	}
	for _, want := range []string{"set tx 22", "region def us us-ny"} {
		if !strings.Contains(commands, want) {
			t.Errorf("assembled preview %q, want it to contain %q", commands, want)
		}
	}
	// Signed out, no editing controls anywhere on the page.
	var controls int
	if err := chromedp.Run(bctx, chromedp.Evaluate(
		`document.querySelectorAll('[data-testid="config-region-add"],[data-testid="config-region-edit"],[data-testid="config-region-delete"]').length`,
		&controls)); err != nil {
		t.Fatalf("control count: %v", err)
	}
	if controls != 0 {
		t.Errorf("anonymous view exposed %d region editing controls, want 0", controls)
	}
	watch.assertClean(t)
}

// TestE2ERegionIconTooltips: the region row's controls are icon-only, so each needs
// to say what it does on hover/focus. Bootstrap tooltips are opt-in and initialized
// once in ui.js against [data-tooltip]; without that wiring these silently fall back
// to the browser's slow native tooltip, which is what prompted this.
func TestE2ERegionIconTooltips(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ergntip")
	org, err := srv.store.CreateOrg(srv.ctx, "Region Tooltip Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := srv.store.CreateRegion(srv.ctx, org.ID, store.RegionInput{
		Token: "buf", DisplayName: "Buffalo", Layer: 3, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(40, -80, 45, -75),
	}); err != nil {
		t.Fatalf("create region: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	cfgURL := srv.appURL + "/orgs/" + org.Slug + "/config"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(cfgURL),
		chromedp.WaitVisible(`[data-testid="config-region-row"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", cfgURL, err)
	}

	// Focus is the reliable trigger headless (Bootstrap listens for hover *and*
	// focus); the tooltip body is rendered into .tooltip-inner.
	for _, c := range []struct{ testid, want string }{
		{"config-region-area", "Edit area"},
		{"config-region-edit", "Edit region"},
		{"config-region-delete", "Delete region"},
	} {
		var got string
		if err := chromedp.Run(bctx,
			chromedp.Focus(`[data-testid="`+c.testid+`"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`.tooltip .tooltip-inner`, chromedp.ByQuery),
			chromedp.Text(`.tooltip .tooltip-inner`, &got, chromedp.ByQuery),
			chromedp.Blur(`[data-testid="`+c.testid+`"]`, chromedp.ByQuery),
			// A dismissed tooltip lingers through its fade-out; without waiting for
			// it to leave the DOM the next read picks up the previous one.
			chromedp.WaitNotPresent(`.tooltip`, chromedp.ByQuery),
		); err != nil {
			t.Fatalf("%s tooltip: %v", c.testid, err)
		}
		if got != c.want {
			t.Errorf("%s tooltip = %q, want %q", c.testid, got, c.want)
		}
	}
	watch.assertClean(t)
}

// TestE2ERootRegionExplainer: the wildcard's info button opens a popover explaining
// what it does and doesn't govern. Bootstrap popovers are opt-in and initialized once
// in ui.js against [data-popover]; the content is the thing people most often misread,
// so it's worth proving it actually reaches the screen.
func TestE2ERootRegionExplainer(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2erootinfo")
	org, err := srv.store.CreateOrg(srv.ctx, "Root Info Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var header, body string
	cfgURL := srv.appURL + "/orgs/" + org.Slug + "/config"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(cfgURL),
		chromedp.WaitVisible(`[data-testid="config-root-info"]`, chromedp.ByQuery),
		chromedp.Focus(`[data-testid="config-root-info"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.popover .popover-body`, chromedp.ByQuery),
		chromedp.Text(`.popover .popover-header`, &header, chromedp.ByQuery),
		chromedp.Text(`.popover .popover-body`, &body, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", cfgURL, err)
	}
	if !strings.Contains(header, "flooding") {
		t.Errorf("popover header = %q, want it to mention flooding", header)
	}
	// The claims that matter, each verified against the firmware
	// (filterRecvFloodPacket / allowPacketForward in MyMesh.cpp): the wildcard
	// governs unscoped packets, scoped ones are matched against the defined regions,
	// and direct-routed packets are exempt from all of it.
	for _, want := range []string{"Unscoped", "Scoped", "direct-routed"} {
		if !strings.Contains(body, want) {
			t.Errorf("popover body missing %q; got %q", want, body)
		}
	}
	watch.assertClean(t)
}

// TestE2ERootFloodSwitchAutosubmits: the root (*) flood switch on the Configuration
// page saves by itself — it's a data-autosubmit checkbox inside its own form nested
// in a list row, so this proves the delegated handler reaches it and that the POST
// round-trips (no inline handler, strict CSP).
func TestE2ERootFloodSwitchAutosubmits(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2erootflood")
	org, err := srv.store.CreateOrg(srv.ctx, "Root Flood Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	// Default is allow, so one click must land on deny.
	if allow, err := srv.store.RootAllowFlood(srv.ctx, org.ID); err != nil || !allow {
		t.Fatalf("root flood starts at (%v, %v), want (true, nil)", allow, err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	sw := `[data-testid="config-region-root"] input[name="root_allow_flood"]`
	var checked bool
	cfgURL := srv.appURL + "/orgs/" + org.Slug + "/config"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(cfgURL),
		chromedp.WaitVisible(sw, chromedp.ByQuery),
		// Clicking it submits the form; the page reloads with the switch off.
		chromedp.Click(sw, chromedp.ByQuery),
		chromedp.WaitVisible(sw, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('`+sw+`').checked`, &checked),
	); err != nil {
		t.Fatalf("browser run against %s: %v", cfgURL, err)
	}
	if checked {
		t.Error("the root flood switch should render unchecked after toggling it off")
	}
	// Polling is necessary rather than sloppy: form.submit() navigates
	// asynchronously, so chromedp.Run can return before the POST has been handled.
	// Wait for the write to land instead of assuming it already has.
	denied := false
	for i := 0; i < 25 && !denied; i++ {
		allow, err := srv.store.RootAllowFlood(srv.ctx, org.ID)
		if err != nil {
			t.Fatalf("read root flood: %v", err)
		}
		if denied = !allow; !denied {
			time.Sleep(200 * time.Millisecond)
		}
	}
	if !denied {
		t.Error("toggling the root flood switch did not persist a deny")
	}
	watch.assertClean(t)
}

// TestE2ERegionAreaDraftEnablesDrawing: the area workspace for a draft region — no
// shape yet, which is how every region starts — initializes with the draw tools
// ENABLED, where a region that already has an area has them disabled (one region,
// one polygon). Covers the toolbar-toggling path in both directions.
func TestE2ERegionAreaDraftEnablesDrawing(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ergndraft")
	org, err := srv.store.CreateOrg(srv.ctx, "Region Draft Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	draft, err := srv.store.CreateRegion(srv.ctx, org.ID, store.RegionInput{
		Token: "buf", DisplayName: "Buffalo", Layer: 3, AllowFlood: true,
	})
	if err != nil {
		t.Fatalf("create draft region: %v", err)
	}
	shaped, err := srv.store.CreateRegion(srv.ctx, org.ID, store.RegionInput{
		Token: "us", DisplayName: "United States", Layer: 1, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(20, -130, 50, -60),
	})
	if err != nil {
		t.Fatalf("create shaped region: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	// Geoman marks a disabled toolbar button on the anchor wrapping the icon, with
	// both .pm-disabled and aria-disabled="true" (see _updateDisabled in
	// leaflet-geoman.js). Reading aria-disabled keeps the assertion unambiguous.
	drawDisabled := `(function () {
		var icon = document.querySelector('.leaflet-pm-icon-polygon');
		if (!icon) return 'missing';
		var btn = icon.closest('a');
		return btn ? btn.getAttribute('aria-disabled') : 'no-anchor';
	})()`
	areaURL := func(rid int64) string {
		return srv.appURL + "/orgs/" + org.Slug + "/config/regions/" + strconv.FormatInt(rid, 10) + "/area"
	}

	// The draft: no area, drawing available.
	var draftStatus string
	var draftDrawDisabled string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(areaURL(draft)),
		chromedp.WaitVisible(`.leaflet-pm-icon-polygon`, chromedp.ByQuery),
		chromedp.Text(`#region-area-status`, &draftStatus, chromedp.ByQuery),
		chromedp.Evaluate(drawDisabled, &draftDrawDisabled),
	); err != nil {
		t.Fatalf("browser run against the draft's area page: %v", err)
	}
	if draftStatus != "No area yet" {
		t.Errorf("draft area status = %q, want %q", draftStatus, "No area yet")
	}
	if draftDrawDisabled != "false" {
		t.Errorf("draft draw button aria-disabled = %q, want %q — a draft's area page must leave the draw tools usable",
			draftDrawDisabled, "false")
	}

	// The one that already has an area: drawing is off until it's cleared.
	var shapedDrawDisabled string
	if err := chromedp.Run(bctx,
		chromedp.Navigate(areaURL(shaped)),
		chromedp.WaitVisible(`#region-map .leaflet-overlay-pane path`, chromedp.ByQuery),
		chromedp.Evaluate(drawDisabled, &shapedDrawDisabled),
	); err != nil {
		t.Fatalf("browser run against the shaped region's area page: %v", err)
	}
	if shapedDrawDisabled != "true" {
		t.Errorf("shaped draw button aria-disabled = %q, want %q — one region holds one polygon, so drawing waits for a clear",
			shapedDrawDisabled, "true")
	}
	watch.assertClean(t)
}

// TestE2ERegionAreaEditor: the area workspace loads the region's shape into the map,
// draws its siblings as context, and saves the geometry the map produces — proving
// the Leaflet/Geoman wiring works under the strict CSP (no inline handlers).
func TestE2ERegionAreaEditor(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ergnarea")
	org, err := srv.store.CreateOrg(srv.ctx, "Region Area Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	// The region under edit already has an area, plus a sibling drawn for context.
	rid, err := srv.store.CreateRegion(srv.ctx, org.ID, store.RegionInput{
		Token: "buf", DisplayName: "Buffalo", Layer: 3, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(40, -80, 45, -75),
	})
	if err != nil {
		t.Fatalf("create region: %v", err)
	}
	if _, err := srv.store.CreateRegion(srv.ctx, org.ID, store.RegionInput{
		Token: "us", DisplayName: "United States", Layer: 1, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(20, -130, 50, -60),
	}); err != nil {
		t.Fatalf("create sibling: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var status, hidden string
	var paths int
	// countPaths counts rendered vector layers: this region plus its one sibling. It
	// is scoped to the overlay pane because Leaflet's attribution control contains a
	// decorative SVG of its own.
	countPaths := `document.querySelectorAll('#region-map .leaflet-overlay-pane path').length`
	areaURL := srv.appURL + "/orgs/" + org.Slug + "/config/regions/" + strconv.FormatInt(rid, 10) + "/area"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(areaURL),
		// The map initializes and reports the existing area.
		chromedp.WaitVisible(`#region-map .leaflet-container, #region-map.leaflet-container`, chromedp.ByQuery),
		chromedp.WaitVisible(`#region-map .leaflet-overlay-pane path`, chromedp.ByQuery),
		chromedp.Text(`#region-area-status`, &status, chromedp.ByQuery),
		chromedp.Evaluate(countPaths, &paths),
		chromedp.Value(`#region_geojson`, &hidden, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", areaURL, err)
	}
	if status != "Custom area" {
		t.Errorf("area status = %q, want %q", status, "Custom area")
	}
	if paths != 2 {
		t.Errorf("rendered %d map polygons, want exactly 2 (the region + its sibling)", paths)
	}
	if !strings.Contains(hidden, "Polygon") {
		t.Errorf("hidden geofence field = %q, want it to hold the polygon geometry", hidden)
	}

	// Clearing the area empties the field and flips the status, then saving persists
	// the cleared state — the round trip the "Clear area" button exists for.
	var clearedStatus, clearedValue string
	if err := chromedp.Run(bctx,
		chromedp.Click(`#region-area-clear`, chromedp.ByQuery),
		chromedp.Text(`#region-area-status`, &clearedStatus, chromedp.ByQuery),
		chromedp.Value(`#region_geojson`, &clearedValue, chromedp.ByQuery),
		chromedp.Click(`[data-testid="save-area"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="config-region-row"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("clear-and-save run: %v", err)
	}
	if clearedStatus != "No area yet" {
		t.Errorf("status after clear = %q, want %q", clearedStatus, "No area yet")
	}
	if clearedValue != "" {
		t.Errorf("geofence field after clear = %q, want empty", clearedValue)
	}
	z, err := srv.store.GetRegion(srv.ctx, org.ID, rid)
	if err != nil {
		t.Fatalf("get region: %v", err)
	}
	if z.Geofence != nil {
		t.Errorf("clearing the area should have persisted as a draft: %+v", z)
	}
	// The attributes survived a geometry-only save.
	if z.Token != "buf" || z.DisplayName != "Buffalo" || z.Layer != 3 {
		t.Errorf("saving the area disturbed the region's attributes: %+v", z)
	}
	watch.assertClean(t)
}
