package core

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/geo"
	"github.com/jleight/meshtender/internal/store"
)

// TestConfigRegionEditorSurfaces: the region attribute editor serves the modal
// fragment to htmx and the standalone page to everything else (the no-JS fallback),
// a validation error re-renders the fragment in place with the entered values kept,
// and a save sends the browser back to the config page.
func TestConfigRegionEditorSurfaces(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "rgnmodaladmin")
	org, err := st.CreateOrg(ctx, "Region Modal Org", admin.ID) // creator becomes admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// htmx GET → just the modal fragment (no page chrome), posting to the collection.
	frag := readBody(t, doHX(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions/new", sess))
	for _, want := range []string{"modal-header", "Add region", `hx-post="/orgs/` + org.Slug + `/config/regions"`} {
		if !strings.Contains(frag, want) {
			t.Fatalf("new-region fragment missing %q", want)
		}
	}
	if strings.Contains(frag, "<html") {
		t.Fatal("htmx request should get a bare fragment, not the full page")
	}
	// The area is drawn on its own page, so the attribute form must not carry it.
	if strings.Contains(frag, "region_geojson") {
		t.Fatal("the attribute modal must not carry the geofence field")
	}

	// Plain GET → the full page fallback, with no htmx attributes to hijack it.
	page := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions/new", sess))
	if !strings.Contains(page, "<html") || !strings.Contains(page, "Create region") {
		t.Fatal("non-htmx request should get the standalone editor page")
	}
	if strings.Contains(page, "hx-post") {
		t.Fatal("the fallback page must not carry hx-post (its target doesn't exist there)")
	}

	// A validation error on an htmx submit keeps the modal: 200 with the fragment,
	// the error, and the entered values — never a redirect that drops the work.
	bad := postHX(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions",
		url.Values{"region_display": {"Buffalo"}, "region_token": {"not a token"}}, sess)
	badBody := readBody(t, bad)
	if bad.StatusCode != http.StatusOK || bad.Header.Get("HX-Redirect") != "" {
		t.Fatalf("invalid submit = %d (HX-Redirect %q), want 200 with no redirect", bad.StatusCode, bad.Header.Get("HX-Redirect"))
	}
	for _, want := range []string{"Nothing was saved", "letters, digits", `value="Buffalo"`, `value="not a token"`} {
		if !strings.Contains(badBody, want) {
			t.Fatalf("re-rendered fragment missing %q", want)
		}
	}

	// A valid htmx submit closes the modal via HX-Redirect back to the config page.
	ok := postHX(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions",
		url.Values{"region_display": {"Buffalo"}, "region_token": {"buf"}, "region_layer": {"3"}, "region_allow_flood": {"1"}}, sess)
	ok.Body.Close()
	if got, want := ok.Header.Get("HX-Redirect"), "/orgs/"+org.Slug+"/config"; ok.StatusCode != http.StatusOK || got != want {
		t.Fatalf("valid submit = %d HX-Redirect %q, want 200 → %q", ok.StatusCode, got, want)
	}

	regions, err := st.ListRegions(ctx, org.ID)
	if err != nil || len(regions) != 1 {
		t.Fatalf("ListRegions = %v, %v; want one region", regions, err)
	}
	z := regions[0]
	if z.Token != "buf" || z.DisplayName != "Buffalo" || z.Layer != 3 || !z.AllowFlood {
		t.Fatalf("saved region = %+v", z)
	}
	// Created without an area: a draft, which applies nowhere.
	if z.Geofence != nil {
		t.Fatalf("a region created from the modal should have no area yet: %+v", z)
	}
	rid := strconv.FormatInt(z.ID, 10)

	// The edit fragment is pre-filled and posts to that region's own URL.
	editFrag := readBody(t, doHX(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions/"+rid+"/edit", sess))
	for _, want := range []string{"Edit region", `value="Buffalo"`, `value="buf"`,
		`hx-post="/orgs/` + org.Slug + `/config/regions/` + rid + `"`} {
		if !strings.Contains(editFrag, want) {
			t.Fatalf("edit fragment missing %q", want)
		}
	}

	// A duplicate token is reported as a validation error, not a 500.
	dup := postHX(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions",
		url.Values{"region_token": {"buf"}}, sess)
	dupBody := readBody(t, dup)
	if dup.StatusCode != http.StatusOK || !strings.Contains(dupBody, "already exists") {
		t.Fatalf("duplicate token = %d, body missing the friendly message: %q", dup.StatusCode, dupBody)
	}

	// Deleting from the list row (a plain POST) returns to the config page.
	del := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions/"+rid+"/delete", url.Values{}, sess)
	del.Body.Close()
	if got, want := del.Header.Get("Location"), "/orgs/"+org.Slug+"/config"; del.StatusCode != http.StatusSeeOther || got != want {
		t.Fatalf("delete = %d → %q, want 303 → %q", del.StatusCode, got, want)
	}
	if left, _ := st.ListRegions(ctx, org.ID); len(left) != 0 {
		t.Fatalf("regions after delete = %+v, want none", left)
	}
}

// TestConfigRegionAreaSaves: the area workspace edits only the geofence. Saving a
// drawn shape promotes a draft; saving an empty one clears it back to a draft; the
// region's attributes are untouched either way, and a malformed shape re-renders the
// page with the shape still in hand rather than discarding it.
func TestConfigRegionAreaSaves(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "rgnareaadmin")
	org, err := st.CreateOrg(ctx, "Region Area Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	rid, err := st.CreateRegion(ctx, org.ID, store.RegionInput{
		Token: "buf", DisplayName: "Buffalo", Layer: 3, Primary: true, AllowFlood: true,
	})
	if err != nil {
		t.Fatalf("create region: %v", err)
	}
	// A sibling with a shape, which the workspace draws as read-only context.
	if _, err := st.CreateRegion(ctx, org.ID, store.RegionInput{
		Token: "us", DisplayName: "United States", Layer: 1, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(20, -130, 50, -60),
	}); err != nil {
		t.Fatalf("create sibling: %v", err)
	}
	area := "/orgs/" + org.Slug + "/config/regions/" + strconv.FormatInt(rid, 10) + "/area"

	// The page renders the map, the region's identity, and the sibling for context.
	page := readBody(t, do(t, ts, h.app, area, sess))
	for _, want := range []string{"region-map", "region_geojson", "Buffalo", "buf", "United States", "initRegionArea"} {
		if !strings.Contains(page, want) {
			t.Fatalf("area page missing %q", want)
		}
	}

	// Saving a drawn shape promotes the draft, leaving its attributes alone.
	drawn := `{"type":"Polygon","coordinates":[[[-80,40],[-75,40],[-75,45],[-80,45],[-80,40]]]}`
	resp := post(t, ts, h.app, area, url.Values{"region_geojson": {drawn}}, sess)
	resp.Body.Close()
	if got, want := resp.Header.Get("Location"), "/orgs/"+org.Slug+"/config"; resp.StatusCode != http.StatusSeeOther || got != want {
		t.Fatalf("save area = %d → %q, want 303 → %q", resp.StatusCode, got, want)
	}
	z, err := st.GetRegion(ctx, org.ID, rid)
	if err != nil {
		t.Fatalf("get region: %v", err)
	}
	if z.Geofence == nil || !z.Geofence.Contains(42, -78) {
		t.Fatalf("area not saved: %+v", z)
	}
	if z.Token != "buf" || z.DisplayName != "Buffalo" || z.Layer != 3 || !z.Primary || !z.AllowFlood {
		t.Fatalf("saving an area disturbed the region's attributes: %+v", z)
	}

	// A malformed shape is rejected, keeps the stored area, and re-renders with the
	// submitted GeoJSON so it isn't silently lost.
	badResp := post(t, ts, h.app, area, url.Values{"region_geojson": {"{not geojson"}}, sess)
	badBody := readBody(t, badResp)
	if badResp.StatusCode != http.StatusOK {
		t.Fatalf("invalid area = %d, want 200 with the editor re-rendered", badResp.StatusCode)
	}
	if !strings.Contains(badBody, "isn&#39;t valid") && !strings.Contains(badBody, "isn't valid") {
		t.Fatalf("invalid area should explain itself: %q", badBody)
	}
	if !strings.Contains(badBody, "not geojson") {
		t.Fatal("the rejected shape should be preserved in the re-rendered form")
	}
	if z, _ = st.GetRegion(ctx, org.ID, rid); z.Geofence == nil {
		t.Fatal("a rejected save must not clear the stored area")
	}

	// An empty shape is a deliberate "Clear area" — back to a draft.
	clear := post(t, ts, h.app, area, url.Values{"region_geojson": {""}}, sess)
	clear.Body.Close()
	if clear.StatusCode != http.StatusSeeOther {
		t.Fatalf("clear area = %d, want 303", clear.StatusCode)
	}
	if z, _ = st.GetRegion(ctx, org.ID, rid); z.Geofence != nil {
		t.Fatalf("clearing the area should return the region to a draft: %+v", z)
	}
}

// TestConfigRegionEditingIsAdminOnly: the region controls render only for an org
// admin — never for a plain member, and never on the anonymous public surface, which
// shares this template. The endpoints themselves 404 for non-admins.
func TestConfigRegionEditingIsAdminOnly(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "rgnroleadmin")
	org, err := st.CreateOrg(ctx, "Region Role Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	rid, err := st.CreateRegion(ctx, org.ID, store.RegionInput{
		Token: "buf", DisplayName: "Buffalo", Layer: 3, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(40, -80, 45, -75),
	})
	if err != nil {
		t.Fatalf("create region: %v", err)
	}
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "rgnrolemember")
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}

	adminView := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", adminSess))
	for _, want := range []string{`data-testid="config-region-add"`, `data-testid="config-region-edit"`,
		`data-testid="config-region-delete"`, `data-testid="config-region-area"`,
		`data-testid="config-region-root"`, `id="config-region-modal"`, "Buffalo"} {
		if !strings.Contains(adminView, want) {
			t.Fatalf("admin config view missing %q", want)
		}
	}

	for _, v := range []struct {
		name string
		body string
	}{
		{"member", readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", memberSess))},
		{"anonymous", readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug+"/config"))},
	} {
		// Regions are public information — a member or visitor needs to see which
		// areas an org defines and where they apply. Only the controls are admin-only.
		for _, want := range []string{`data-testid="config-regions"`, `data-testid="config-region-row"`,
			"Buffalo", "buf", "region-map"} {
			if !strings.Contains(v.body, want) {
				t.Errorf("%s config view should still show the regions; missing %q", v.name, want)
			}
		}
		for _, forbidden := range []string{"config-region-add", "config-region-edit", "config-region-delete",
			"config-region-modal", "config-region-root", "/config/regions"} {
			if strings.Contains(v.body, forbidden) {
				t.Fatalf("%s config view leaked the admin control %q", v.name, forbidden)
			}
		}
	}

	// The endpoints are admin-gated, not just hidden: a member gets 404 everywhere.
	ridStr := strconv.FormatInt(rid, 10)
	for _, path := range []string{
		"/orgs/" + org.Slug + "/config/regions/new",
		"/orgs/" + org.Slug + "/config/regions/" + ridStr + "/edit",
		"/orgs/" + org.Slug + "/config/regions/" + ridStr + "/area",
	} {
		resp := do(t, ts, h.app, path, memberSess)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("member GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
	for _, path := range []string{
		"/orgs/" + org.Slug + "/config/regions",
		"/orgs/" + org.Slug + "/config/regions/" + ridStr,
		"/orgs/" + org.Slug + "/config/regions/" + ridStr + "/delete",
		"/orgs/" + org.Slug + "/config/regions/" + ridStr + "/area",
		"/orgs/" + org.Slug + "/config/root-flood",
	} {
		resp := post(t, ts, h.app, path, url.Values{"region_token": {"x"}}, memberSess)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("member POST %s = %d, want 404", path, resp.StatusCode)
		}
	}
	// And nothing a member submitted took effect.
	if z, err := st.GetRegion(ctx, org.ID, rid); err != nil || z.Token != "buf" {
		t.Fatalf("region changed under a non-admin: %+v (err %v)", z, err)
	}
}

// TestConfigMapMarksPrimaryRegion: the config map's payload flags the primary
// region, which is what it opens framed on. Without that flag the map falls back to
// containing every region, so a nationwide parent would force a continental view of
// an org that operates in one county.
func TestConfigMapMarksPrimaryRegion(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "cfgmapprimary")
	org, err := st.CreateOrg(ctx, "Map Primary Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	for _, z := range []store.RegionInput{
		{Token: "us", DisplayName: "US", Layer: 1, AllowFlood: true, GeofenceJSON: geo.Rectangle(25, -125, 49, -67)},
		{Token: "wny", DisplayName: "Western New York", Layer: 3, Primary: true, AllowFlood: true,
			GeofenceJSON: geo.Rectangle(42, -79.8, 43.5, -77.5)},
	} {
		if _, err := st.CreateRegion(ctx, org.ID, z); err != nil {
			t.Fatalf("create region %s: %v", z.Token, err)
		}
	}

	body := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", sess))
	// Exactly one region in the map payload is flagged primary, and it's the one the
	// org marked — not simply the first drawn.
	if got := strings.Count(body, `"primary":true`); got != 1 {
		t.Fatalf("map payload has %d primary regions, want exactly 1", got)
	}
	// Regions ship ordered by (layer, token), so Western New York (layer 3) is last:
	// the flag must fall after its name and nowhere before it. Slicing on braces
	// would cut inside the escaped GeoJSON, which is full of them.
	at := strings.Index(body, `"name":"Western New York"`)
	if at < 0 {
		t.Fatal("the primary region is missing from the map payload")
	}
	if strings.Contains(body[:at], `"primary":true`) {
		t.Fatal("a region before the primary one carries the primary flag")
	}
	if !strings.Contains(body[at:], `"primary":true`) {
		t.Fatal("the primary flag is not on Western New York")
	}
	// Every drawn region still ships with a color, so the legend swatches match.
	if got := strings.Count(body, `"color":"#`); got != 2 {
		t.Fatalf("map payload carries %d colors, want one per drawn region (2)", got)
	}
}

// TestConfigRootFloodToggle: the root (*) flood policy saves on its own from the
// config page — it isn't a region row, so it must not require any region to exist.
func TestConfigRootFloodToggle(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "rootfloodadmin")
	org, err := st.CreateOrg(ctx, "Root Flood Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	path := "/orgs/" + org.Slug + "/config/root-flood"

	// An unchecked switch submits nothing → deny.
	resp := post(t, ts, h.app, path, url.Values{}, sess)
	resp.Body.Close()
	if got, want := resp.Header.Get("Location"), "/orgs/"+org.Slug+"/config"; resp.StatusCode != http.StatusSeeOther || got != want {
		t.Fatalf("deny flood = %d → %q, want 303 → %q", resp.StatusCode, got, want)
	}
	if allow, err := st.RootAllowFlood(ctx, org.ID); err != nil || allow {
		t.Fatalf("root flood after deny = %v (err %v), want false", allow, err)
	}

	// A checked switch submits "1" → allow.
	resp = post(t, ts, h.app, path, url.Values{"root_allow_flood": {"1"}}, sess)
	resp.Body.Close()
	if allow, err := st.RootAllowFlood(ctx, org.ID); err != nil || !allow {
		t.Fatalf("root flood after allow = %v (err %v), want true", allow, err)
	}

	// The switch renders checked, reflecting the stored value.
	page := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", sess))
	if !strings.Contains(page, `name="root_allow_flood" value="1"`) {
		t.Fatal("config page should render the root flood switch")
	}
	// While flooding is allowed there's nothing to warn about.
	if strings.Contains(page, `data-testid="config-root-deny-warning"`) {
		t.Error("the deny warning should only show while flooding is denied")
	}

	// Denying is the consequential setting: the wildcard is the only thing that
	// repeats unscoped packets (meshcore-go node/region.go, FindFloodMatch), so
	// turning it off silently strands anyone who hasn't configured regions. Say so.
	resp = post(t, ts, h.app, path, url.Values{}, sess)
	resp.Body.Close()
	denied := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", sess))
	if !strings.Contains(denied, `data-testid="config-root-deny-warning"`) {
		t.Error("denying root flood should warn what it does to unscoped traffic")
	}
	// And the label must not claim the wildcard applies "everywhere" — it governs
	// only packets that carry no region.
	if strings.Contains(denied, "everywhere") {
		t.Error(`the root row should not describe the wildcard as "everywhere"`)
	}
}
