package core

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestOrgConfigProfilesFlow exercises #11 end to end: an admin saves multiple
// named profiles + a region, the read view shows them with a profile selector,
// and the Configuration tab hides on a config-less org's public page. Steps are
// comment-only so the flow doesn't depend on the seeded command catalog.
func TestOrgConfigProfilesFlow(t *testing.T) {
	st, ctx, ts, h := splitServer(t)

	// appLogin creates the user and signs them in through the same handoff this test
	// used to hand-roll. It checks each step, which matters: the hand-rolled version
	// discarded CreateUser's error, so a failure there surfaced as a nil-pointer
	// panic on the next line — a segfault where the actual cause (CI starving the
	// database) was never printed.
	admin, sess := appLogin(t, ts, st, ctx, h.app, "cfgadmin")

	org, err := st.CreateOrg(ctx, "Config Org", admin.ID) // creator becomes admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Two profiles, each created through the editor. A save lands back on the
	// config page with the new profile selected.
	for _, p := range []struct{ name, steps string }{{"ESP32", "# esp base"}, {"nRF52", "# nrf base"}} {
		save := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles",
			url.Values{"profile_name": {p.name}, "profile_steps": {p.steps}}, sess)
		save.Body.Close()
		if save.StatusCode != http.StatusSeeOther {
			t.Fatalf("create profile %s status = %d, want 303", p.name, save.StatusCode)
		}
		if got, want := save.Header.Get("Location"), "/orgs/"+org.Slug+"/config?profile="+url.QueryEscape(p.name); got != want {
			t.Fatalf("create profile %s redirected to %q, want %q", p.name, got, want)
		}
	}
	// A region, created in two steps the way the editor does it: the attribute form
	// creates it as a draft, then the area page saves its drawn shape.
	rsave := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions", url.Values{
		"region_display":     {"Mountains"},
		"region_token":       {"mtns"},
		"region_layer":       {"2"},
		"region_allow_flood": {"1"},
	}, sess)
	rsave.Body.Close()
	if rsave.StatusCode != http.StatusSeeOther {
		t.Fatalf("create region status = %d, want 303", rsave.StatusCode)
	}
	regions, err := st.ListRegions(ctx, org.ID)
	if err != nil || len(regions) != 1 {
		t.Fatalf("ListRegions = %v, %v; want one region", regions, err)
	}
	rid := strconv.FormatInt(regions[0].ID, 10)

	// Before an area is drawn the region is a draft: flagged on the config page and
	// contributing nothing to a location's region def.
	draftView := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config?lat=15&lon=35", sess))
	if !strings.Contains(draftView, `data-testid="config-region-draft"`) {
		t.Fatal("a region with no area should be flagged as needing one")
	}
	if strings.Contains(draftView, "region def") {
		t.Fatal("a draft region must not produce region def commands")
	}

	asave := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions/"+rid+"/area", url.Values{
		"region_geojson": {`{"type":"Polygon","coordinates":[[[30,10],[40,10],[40,20],[30,20],[30,10]]]}`},
	}, sess)
	asave.Body.Close()
	if asave.StatusCode != http.StatusSeeOther {
		t.Fatalf("save area status = %d, want 303", asave.StatusCode)
	}

	body := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", sess))
	// The profile list and the region list, each with per-row admin actions, plus the
	// click-to-preview location map the drawn geofence enables.
	for _, want := range []string{
		"ESP32", "nRF52", `data-testid="config-profile-list"`,
		`data-testid="config-profile-add"`, `data-testid="config-profile-edit"`, `data-testid="config-profile-delete"`,
		`id="config-profile-modal"`, `data-testid="config-regions"`, "region-map",
		"Mountains", "mtns", `data-testid="config-region-row"`, `data-testid="config-region-add"`,
		`data-testid="config-region-area"`, `id="config-region-modal"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("config view missing %q", want)
		}
	}
	// With an area drawn, the region is no longer flagged as a draft.
	if strings.Contains(body, `data-testid="config-region-draft"`) {
		t.Fatal("a region with an area should not be flagged as needing one")
	}
	// And it now applies at a location inside it.
	preview := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config?lat=15&lon=35", sess))
	if !strings.Contains(preview, "region def mtns") {
		t.Fatal("a drawn region should apply at a location inside it")
	}
	// Default selection shows the first profile's steps, not the second's.
	if !strings.Contains(body, "esp base") || strings.Contains(body, "nrf base") {
		t.Fatal("default config view should show the first profile's base settings")
	}
	// The profile selector works: ?profile= switches the shown base settings.
	sel := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config?profile=nRF52", sess))
	if !strings.Contains(sel, "nrf base") || strings.Contains(sel, "esp base") {
		t.Fatal("?profile=nRF52 should show nRF52's base settings")
	}

	// The preview is assembled: with a location picked, the selected profile's steps
	// and that location's region commands appear in one block, which is what a
	// repeater owner actually runs.
	assembled := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config?profile=ESP32&lat=15&lon=35", sess))
	block := assembled[strings.Index(assembled, `data-testid="config-commands"`):]
	block = block[:strings.Index(block, "</code>")]
	for _, want := range []string{"esp base", "region def mtns", "region save"} {
		if !strings.Contains(block, want) {
			t.Fatalf("assembled preview missing %q; got %q", want, block)
		}
	}
	// Region-contributed lines are marked so the page can show where they came from.
	if !strings.Contains(block, `data-testid="config-region-line"`) {
		t.Fatal("region commands in the assembled preview should be marked as region lines")
	}
	// And the legend marks the regions that cover the picked location.
	if !strings.Contains(assembled, `data-testid="config-region-match"`) {
		t.Fatal("the legend should mark which regions apply at the previewed location")
	}

	// The standalone editor pages render (the no-JS fallbacks): the new-profile page
	// shows the base-settings editor, and the region area page has the map.
	newProf := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles/new", sess))
	if !strings.Contains(newProf, "Base settings") || !strings.Contains(newProf, "Create profile") {
		t.Fatalf("new-profile page missing its form")
	}
	rgn := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions/"+rid+"/area", sess))
	for _, want := range []string{"region-map", "Mountains", "mtns", "Save area", "Clear area"} {
		if !strings.Contains(rgn, want) {
			t.Fatalf("region area editor missing %q", want)
		}
	}
	// The old config hub is gone entirely (404), and the old bulk region editor's
	// path is now POST-only — the collection you create a region through — so a GET
	// there is a 405 rather than a page.
	hub := do(t, ts, h.app, "/orgs/"+org.Slug+"/config/edit", sess)
	hub.Body.Close()
	if hub.StatusCode != http.StatusNotFound {
		t.Errorf("GET /config/edit = %d, want 404 (route removed)", hub.StatusCode)
	}
	bulk := do(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions", sess)
	bulk.Body.Close()
	if bulk.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /config/regions = %d, want 405 (POST-only collection)", bulk.StatusCode)
	}

	// A non-admin member of a config-less org does NOT see the Configuration tab.
	plain, err := st.CreateUser(ctx, "plainmember", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	noCfg, err := st.CreateOrg(ctx, "NoCfg Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := st.AddOrgMember(ctx, noCfg.ID, plain.ID, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	pLogin, err := st.CreateLogin(ctx, plain.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}
	pCode, err := st.CreateAuthCode(ctx, plain.ID, pLogin, "/")
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	pcb := do(t, ts, h.app, "/session/callback?code="+pCode+"&state=s1", &http.Cookie{Name: "mt_state", Value: "s1"})
	pcb.Body.Close()
	pSess := cookieByName(pcb, "meshtender_session")
	memberView := readBody(t, do(t, ts, h.app, "/orgs/"+noCfg.Slug, pSess))
	if strings.Contains(memberView, "/orgs/"+noCfg.Slug+"/config") {
		t.Fatalf("a non-admin member should not see the config tab when there's no config")
	}

	// A config-less org hides the Configuration tab on its public page; the
	// configured org shows it.
	empty, err := st.CreateOrg(ctx, "Empty Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	pub := readBody(t, do(t, ts, h.root, "/orgs/"+empty.Slug))
	if strings.Contains(pub, "/orgs/"+empty.Slug+"/config") {
		t.Fatalf("public org page should hide the config tab when there's no config")
	}
	pub2 := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug))
	if !strings.Contains(pub2, "/orgs/"+org.Slug+"/config") {
		t.Fatalf("public org page should show the config tab when config exists")
	}
}
