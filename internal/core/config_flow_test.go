package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestOrgConfigProfilesFlow exercises #11 end to end: an admin saves multiple
// named profiles + a region, the read view shows them with a profile selector,
// and the Configuration tab hides on a config-less org's public page. Steps are
// comment-only so the flow doesn't depend on the seeded command catalog.
func TestOrgConfigProfilesFlow(t *testing.T) {
	st, ctx, ts, h := splitServer(t)

	// Sign in via the handoff (the splitServer-proven pattern) and grab the app
	// session cookie to carry on later requests.
	admin, _ := st.CreateUser(ctx, "cfgadmin", "")
	loginID, _ := st.CreateLogin(ctx, admin.ID)
	code, _ := st.CreateAuthCode(ctx, admin.ID, loginID, "/")
	cb := do(t, ts, h.app, "/session/callback?code="+code+"&state=s1", &http.Cookie{Name: "mt_state", Value: "s1"})
	cb.Body.Close()
	sess := cookieByName(cb, "meshtender_session")
	if sess == nil {
		t.Fatal("no app session from callback")
	}

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
	// A geofenced region, saved on the region page.
	rsave := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions", url.Values{
		"region_display":     {"Mountains"},
		"region_token":       {"mtns"},
		"region_layer":       {"2"},
		"region_allow_flood": {"1"},
		"root_allow_flood":   {"1"},
		"region_geojson":     {`{"type":"Polygon","coordinates":[[[30,10],[40,10],[40,20],[30,20],[30,10]]]}`},
	}, sess)
	rsave.Body.Close()
	if rsave.StatusCode != http.StatusSeeOther {
		t.Fatalf("save regions status = %d, want 303", rsave.StatusCode)
	}

	body := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", sess))
	// The profile list, with per-row admin actions; the geofenced region surfaces as
	// the click-to-preview location map (regions aren't listed individually).
	for _, want := range []string{
		"ESP32", "nRF52", `data-testid="config-profile-list"`,
		`data-testid="config-profile-add"`, `data-testid="config-profile-edit"`, `data-testid="config-profile-delete"`,
		`id="config-profile-modal"`, "Preview a location", "region-map",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("config view missing %q", want)
		}
	}
	// Default selection shows the first profile's base settings.
	if !strings.Contains(body, "ESP32 · base settings") {
		t.Fatalf("default config view should show the first profile's base settings")
	}
	// The profile selector works: ?profile= switches the shown base settings.
	sel := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config?profile=nRF52", sess))
	if !strings.Contains(sel, "nRF52 · base settings") {
		t.Fatalf("?profile=nRF52 should show nRF52's base settings")
	}

	// The admin editor pages render: the hub summarizes the regions (profiles live
	// on the config page now), the new-profile page shows the base-settings editor,
	// and the region page has the map + a region row.
	hub := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config/edit", sess))
	for _, want := range []string{"Edit regions", "1 region"} {
		if !strings.Contains(hub, want) {
			t.Fatalf("config hub missing %q", want)
		}
	}
	if strings.Contains(hub, "Add profile") {
		t.Fatalf("config hub should no longer manage profiles")
	}
	newProf := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles/new", sess))
	if !strings.Contains(newProf, "Base settings") || !strings.Contains(newProf, "Create profile") {
		t.Fatalf("new-profile page missing its form")
	}
	rgn := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions", sess))
	for _, want := range []string{"region-map", "Mountains", "mtns", "Add region", "everywhere", "Allow flood"} {
		if !strings.Contains(rgn, want) {
			t.Fatalf("regions editor missing %q", want)
		}
	}

	// A non-admin member of a config-less org does NOT see the Configuration tab.
	plain, _ := st.CreateUser(ctx, "plainmember", "")
	noCfg, _ := st.CreateOrg(ctx, "NoCfg Org", admin.ID)
	if err := st.AddOrgMember(ctx, noCfg.ID, plain.ID, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	pLogin, _ := st.CreateLogin(ctx, plain.ID)
	pCode, _ := st.CreateAuthCode(ctx, plain.ID, pLogin, "/")
	pcb := do(t, ts, h.app, "/session/callback?code="+pCode+"&state=s1", &http.Cookie{Name: "mt_state", Value: "s1"})
	pcb.Body.Close()
	pSess := cookieByName(pcb, "meshtender_session")
	memberView := readBody(t, do(t, ts, h.app, "/orgs/"+noCfg.Slug, pSess))
	if strings.Contains(memberView, "/orgs/"+noCfg.Slug+"/config") {
		t.Fatalf("a non-admin member should not see the config tab when there's no config")
	}

	// A config-less org hides the Configuration tab on its public page; the
	// configured org shows it.
	empty, _ := st.CreateOrg(ctx, "Empty Org", admin.ID)
	pub := readBody(t, do(t, ts, h.root, "/orgs/"+empty.Slug))
	if strings.Contains(pub, "/orgs/"+empty.Slug+"/config") {
		t.Fatalf("public org page should hide the config tab when there's no config")
	}
	pub2 := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug))
	if !strings.Contains(pub2, "/orgs/"+org.Slug+"/config") {
		t.Fatalf("public org page should show the config tab when config exists")
	}
}
