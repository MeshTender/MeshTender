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
	var counts sourceCounts
	var rows int
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(cfgURL),
		mapReady("region-map", "regions-fill"),
		mapEval("region-map", countIn("regions"), &counts),
		chromedp.Evaluate(`document.querySelectorAll('[data-testid="config-region-row"]').length`, &rows),
	); err != nil {
		t.Fatalf("browser run against %s: %v", cfgURL, err)
	}
	if counts.Features != 3 {
		t.Errorf("drew %d polygons, want 3 (one per shaped region)", counts.Features)
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

	// Terra Draw ships no UI, so the toolbar is ours (DrawControl in regionmap.js)
	// and a disabled tool is a plain disabled <button>.
	drawButton := `[data-testid="region-draw-polygon"]`
	drawDisabled := `(function () {
		var btn = document.querySelector('` + drawButton + `');
		return btn ? String(btn.disabled) : 'missing';
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
		mapReady("region-map", "siblings-fill"),
		chromedp.WaitVisible(drawButton, chromedp.ByQuery),
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
		mapReady("region-map", "siblings-fill"),
		chromedp.Poll(`(function () {
			var e = window.MESHTENDER_MAPS["region-map"];
			return !!(e && e.draw && e.draw.getSnapshot().length);
		})()`, nil, chromedp.WithPollingTimeout(30*time.Second)),
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
// the MapLibre/Terra Draw wiring works under the strict CSP (no inline handlers,
// and a worker loaded from a same-origin URL rather than a blob:).
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
	var siblings sourceCounts
	var editable int
	// The two shapes live in different places now: the read-only sibling is a layer
	// the page adds, while the one under edit belongs to Terra Draw's own store.
	countEditable := `(function () {
		return window.MESHTENDER_MAPS["region-map"].draw.getSnapshot()
			.filter(function (f) { return f.geometry.type === "Polygon"; }).length;
	})()`
	areaURL := srv.appURL + "/orgs/" + org.Slug + "/config/regions/" + strconv.FormatInt(rid, 10) + "/area"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(areaURL),
		// The map initializes and reports the existing area.
		mapReady("region-map", "siblings-fill"),
		chromedp.Text(`#region-area-status`, &status, chromedp.ByQuery),
		mapEval("region-map", countIn("siblings"), &siblings),
		chromedp.Evaluate(countEditable, &editable),
		chromedp.Value(`#region_geojson`, &hidden, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", areaURL, err)
	}
	if status != "Custom area" {
		t.Errorf("area status = %q, want %q", status, "Custom area")
	}
	if siblings.Features != 1 {
		t.Errorf("drew %d sibling outlines, want exactly 1", siblings.Features)
	}
	if editable != 1 {
		t.Errorf("loaded %d editable polygons, want exactly 1 (the region under edit)", editable)
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

// TestE2ERegionAreaDrawsAPolygon draws an area the way an operator does — press the
// tool, click the corners on the map, press Enter — and checks the geometry lands in
// the field the form submits.
//
// This is the interaction the migration rebuilt from scratch: Leaflet-Geoman brought
// its own toolbar and edit handles, while Terra Draw is headless, so the tool
// buttons, the mode switching and the serialization are all ours (DrawControl and
// initRegionArea in regionmap.js). None of that is exercised by asserting on a
// pre-loaded shape, and all of it runs on click handlers the strict CSP would reject
// if they were ever written inline.
func TestE2ERegionAreaDrawsAPolygon(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ergndraw")
	org, err := srv.store.CreateOrg(srv.ctx, "Region Draw Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	// A draft: no area yet, so the draw tools start enabled.
	rid, err := srv.store.CreateRegion(srv.ctx, org.ID, store.RegionInput{
		Token: "buf", DisplayName: "Buffalo", Layer: 3, AllowFlood: true,
	})
	if err != nil {
		t.Fatalf("create region: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	// The corners, as offsets inside the map container.
	corners := []struct{ dx, dy float64 }{{120, 90}, {260, 110}, {200, 220}}

	var rect struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	var status, geojson string
	areaURL := srv.appURL + "/orgs/" + org.Slug + "/config/regions/" + strconv.FormatInt(rid, 10) + "/area"

	tasks := chromedp.Tasks{
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(areaURL),
		mapReady("region-map", "siblings-fill"),
		chromedp.Click(`[data-testid="region-draw-polygon"]`, chromedp.ByQuery),
		chromedp.Evaluate(`(function () {
			var r = document.getElementById("region-map").getBoundingClientRect();
			return { x: r.left, y: r.top };
		})()`, &rect),
	}
	if err := chromedp.Run(bctx, tasks); err != nil {
		t.Fatalf("browser run against %s: %v", areaURL, err)
	}

	clicks := chromedp.Tasks{}
	for _, c := range corners {
		clicks = append(clicks, chromedp.MouseClickXY(rect.X+c.dx, rect.Y+c.dy))
	}
	if err := chromedp.Run(bctx,
		clicks,
		// Terra Draw's polygon mode closes the ring on Enter, which is far steadier
		// than trying to land a click back on the first vertex.
		chromedp.KeyEvent("\r"),
		chromedp.Poll(`document.getElementById("region_geojson").value !== ""`, nil,
			chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Text(`#region-area-status`, &status, chromedp.ByQuery),
		chromedp.Value(`#region_geojson`, &geojson, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("drawing the polygon: %v", err)
	}

	if status != "Custom area" {
		t.Errorf("status after drawing = %q, want %q", status, "Custom area")
	}
	if !strings.Contains(geojson, `"Polygon"`) {
		t.Errorf("drawn geometry = %q, want a GeoJSON Polygon", geojson)
	}

	// Saving persists what was drawn, which is the whole point of the workspace.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-testid="save-area"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="config-region-row"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("saving the drawn area: %v", err)
	}
	z, err := srv.store.GetRegion(srv.ctx, org.ID, rid)
	if err != nil {
		t.Fatalf("get region: %v", err)
	}
	if z.Geofence == nil {
		t.Fatal("the drawn area did not persist; the region is still a draft")
	}
	watch.assertClean(t)
}

// TestE2ERegionAreaDrawsAConcaveArea is the regression test for a reported bug: an
// operator drawing a coastline-shaped region — south down the state line, east along
// the bottom, then farther south around a peninsula — found the tool simply stopped
// adding points, with no message.
//
// The cause was where the self-intersection rule was enforced. Terra Draw runs a
// mode's `validation` on provisional updates as well as on finish, and a polygon
// under construction is auto-closed back to its first point. An outline that has gone
// south then east crosses that closing edge the moment it heads south again — so the
// intermediate ring is self-intersecting even though the finished shape never is, and
// the click was rejected silently. Only the finished shape is judged now.
//
// The second half asserts the rule still does its job: a genuinely crossed outline is
// refused, says why, and — the part that matters most — never reaches the field the
// form submits, so Save cannot persist a geofence with no inside.
func TestE2ERegionAreaDrawsAConcaveArea(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ergnconcave")
	org, err := srv.store.CreateOrg(srv.ctx, "Concave Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	rid, err := srv.store.CreateRegion(srv.ctx, org.ID, store.RegionInput{
		Token: "ny", DisplayName: "New York", Layer: 2, AllowFlood: true,
	})
	if err != nil {
		t.Fatalf("create region: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	// mapOrigin re-reads where the map sits in the viewport. It has to be re-read
	// after anything that scrolls (clicking a footer button scrolls it into view),
	// or clicks computed from a stale origin land outside the map entirely.
	var origin struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	mapOrigin := chromedp.Tasks{
		chromedp.Evaluate(`(function () {
			document.getElementById("region-map").scrollIntoView({ block: "start" });
			return true;
		})()`, nil),
		chromedp.Sleep(300 * time.Millisecond),
		chromedp.Evaluate(`(function () {
			var r = document.getElementById("region-map").getBoundingClientRect();
			return { x: r.left, y: r.top };
		})()`, &origin),
	}
	// ringLength is how many coordinates the shape under the cursor currently has.
	const ringLength = `(function () {
		var f = window.MESHTENDER_MAPS["region-map"].draw.getSnapshot()
			.filter(function (x) { return x.geometry.type === "Polygon"; })[0];
		return f ? f.geometry.coordinates[0].length : 0;
	})()`

	areaURL := srv.appURL + "/orgs/" + org.Slug + "/config/regions/" + strconv.FormatInt(rid, 10) + "/area"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(areaURL),
		mapReady("region-map", "siblings-fill"),
		chromedp.Click(`[data-testid="region-draw-polygon"]`, chromedp.ByQuery),
		mapOrigin,
	); err != nil {
		t.Fatalf("browser run against %s: %v", areaURL, err)
	}

	// The concave outline. Corner 4 — heading farther south after going east — is
	// the one the bug swallowed.
	concave := []struct{ dx, dy float64 }{
		{120, 80}, {120, 200}, {330, 200}, {330, 270}, {400, 270}, {400, 120}, {250, 120},
	}
	for i, c := range concave {
		var ring int
		if err := chromedp.Run(bctx,
			chromedp.MouseClickXY(origin.X+c.dx, origin.Y+c.dy),
			chromedp.Sleep(250*time.Millisecond),
			chromedp.Evaluate(ringLength, &ring),
		); err != nil {
			t.Fatalf("corner %d: %v", i+1, err)
		}
		// Terra Draw seeds a closed ring from the first click, so the count runs
		// ahead of the corner number; what matters is that it keeps growing.
		if want := i + 2; i > 0 && ring < want {
			t.Fatalf("after corner %d the ring has %d coordinates, want at least %d — "+
				"the click was rejected, which is the concave-drawing bug", i+1, ring, want)
		}
	}

	var status, geojson string
	if err := chromedp.Run(bctx,
		chromedp.KeyEvent("\r"),
		chromedp.Poll(`document.getElementById("region_geojson").value !== ""`, nil,
			chromedp.WithPollingTimeout(15*time.Second)),
		chromedp.Text(`#region-area-status`, &status, chromedp.ByQuery),
		chromedp.Value(`#region_geojson`, &geojson, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("finishing the concave outline: %v", err)
	}
	if status != "Custom area" {
		t.Errorf("status = %q, want %q", status, "Custom area")
	}
	// Seven corners plus the repeated closing coordinate.
	if n := strings.Count(geojson, "],["); n != 7 {
		t.Errorf("drawn ring has %d segments, want 7 — corners were dropped: %s", n, geojson)
	}

	// Now a genuinely self-intersecting outline: clear, then draw a bowtie.
	if err := chromedp.Run(bctx,
		chromedp.Click(`#region-area-clear`, chromedp.ByQuery),
		mapOrigin,
	); err != nil {
		t.Fatalf("clearing before the crossed outline: %v", err)
	}
	for _, c := range []struct{ dx, dy float64 }{{120, 80}, {330, 80}, {120, 220}, {330, 220}} {
		if err := chromedp.Run(bctx,
			chromedp.MouseClickXY(origin.X+c.dx, origin.Y+c.dy),
			chromedp.Sleep(250*time.Millisecond),
		); err != nil {
			t.Fatalf("crossed outline: %v", err)
		}
	}
	var crossedStatus, crossedValue string
	if err := chromedp.Run(bctx,
		chromedp.KeyEvent("\r"),
		chromedp.Sleep(700*time.Millisecond),
		chromedp.Text(`#region-area-status`, &crossedStatus, chromedp.ByQuery),
		chromedp.Value(`#region_geojson`, &crossedValue, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("finishing the crossed outline: %v", err)
	}
	if crossedValue != "" {
		t.Errorf("a self-intersecting outline reached the submitted field (%q); Save would "+
			"persist a geofence with no well-defined inside", crossedValue)
	}
	if !strings.Contains(crossedStatus, "cross") {
		t.Errorf("status after a refused outline = %q, want it to say why the shape was "+
			"rejected — a silent refusal is the bug this whole test exists for", crossedStatus)
	}
	watch.assertClean(t)
}

// TestE2ERegionAreaVertexSpacing pins both sides of one trade-off.
//
// Terra Draw's pointerDistance is the radius, in screen pixels, within which a click
// counts as "the same point". It is measured against the previous vertex as well as
// the first, so at its default of 40 no two corners could be placed closer than that
// — tracing a shoreline or a county line meant zooming far in and stitching. The
// editor lowers it (POINTER_DISTANCE in regionmap.js).
//
// The cost is that closing the ring by clicking the first corner is a smaller target,
// so both halves are asserted here: fine detail has to be drawable, and closing by
// click has to keep working. Tightening the constant further without checking the
// second half would quietly make the outline hard to finish.
func TestE2ERegionAreaVertexSpacing(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ergnspacing")
	org, err := srv.store.CreateOrg(srv.ctx, "Spacing Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	rid, err := srv.store.CreateRegion(srv.ctx, org.ID, store.RegionInput{
		Token: "sp", DisplayName: "Spacing", Layer: 2, AllowFlood: true,
	})
	if err != nil {
		t.Fatalf("create region: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var origin struct {
		X float64 `json:"x"`
		Y float64 `json:"y"`
	}
	areaURL := srv.appURL + "/orgs/" + org.Slug + "/config/regions/" + strconv.FormatInt(rid, 10) + "/area"
	startDrawing := chromedp.Tasks{
		chromedp.Navigate(areaURL),
		mapReady("region-map", "siblings-fill"),
		chromedp.Click(`[data-testid="region-draw-polygon"]`, chromedp.ByQuery),
		chromedp.Evaluate(`(function () {
			document.getElementById("region-map").scrollIntoView({ block: "start" });
			return true;
		})()`, nil),
		chromedp.Sleep(300 * time.Millisecond),
		chromedp.Evaluate(`(function () {
			var r = document.getElementById("region-map").getBoundingClientRect();
			return { x: r.left, y: r.top };
		})()`, &origin),
	}
	const ringLength = `(function () {
		var f = window.MESHTENDER_MAPS["region-map"].draw.getSnapshot()
			.filter(function (x) { return x.geometry.type === "Polygon"; })[0];
		return f ? f.geometry.coordinates[0].length : 0;
	})()`

	// Detail: a staircase whose corners are ~14px apart — well inside the old 40px
	// floor. Every click must land.
	const gap = 10.0
	var ring int
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		startDrawing,
	); err != nil {
		t.Fatalf("browser run against %s: %v", areaURL, err)
	}
	for i := range 5 {
		if err := chromedp.Run(bctx,
			chromedp.MouseClickXY(origin.X+150+float64(i)*gap, origin.Y+150+float64(i%2)*gap),
			chromedp.Sleep(220*time.Millisecond),
			chromedp.Evaluate(ringLength, &ring),
		); err != nil {
			t.Fatalf("close-spaced corner %d: %v", i+1, err)
		}
	}
	// Five placed corners, plus the ring's repeated closing coordinate and the one
	// tracking the cursor.
	if ring != 7 {
		t.Errorf("after five corners ~%.0fpx apart the ring has %d coordinates, want 7 — "+
			"clicks that close together are being swallowed, so fine detail can't be drawn",
			gap*1.414, ring)
	}

	// Closing: a click back on the first corner, slightly off-centre, still finishes.
	var st struct {
		Mode   string `json:"mode"`
		Filled bool   `json:"filled"`
	}
	if err := chromedp.Run(bctx,
		startDrawing,
		chromedp.MouseClickXY(origin.X+150, origin.Y+150),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.MouseClickXY(origin.X+320, origin.Y+150),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.MouseClickXY(origin.X+320, origin.Y+300),
		chromedp.Sleep(200*time.Millisecond),
		chromedp.MouseClickXY(origin.X+155, origin.Y+155),
		chromedp.Sleep(600*time.Millisecond),
		chromedp.Evaluate(`(function () {
			var e = window.MESHTENDER_MAPS["region-map"];
			return { mode: e.draw.getMode(),
			         filled: document.getElementById("region_geojson").value !== "" };
		})()`, &st),
	); err != nil {
		t.Fatalf("closing by click: %v", err)
	}
	if st.Mode != "select" || !st.Filled {
		t.Errorf("clicking back on the first corner left mode=%q filled=%v, want the ring "+
			"closed and handed to the edit tool — the closing target has become too small",
			st.Mode, st.Filled)
	}
	watch.assertClean(t)
}
