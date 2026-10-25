package core

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// Black-box coverage for the org-management POST endpoints (create/edit/links/
// members and join/leave). Each asserts the 303 redirect target and a cheap store
// side-effect; none render anything.

// #43 create, #44 edit, #104 links, #48 member role.
func TestOrgManagementPosts(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	_, sess := appLogin(t, ts, st, ctx, h.app, "orgadmin")

	// #43 create → 303 to the new org's page.
	create := post(t, ts, h.app, "/orgs", url.Values{"name": {"Test Org"}}, sess)
	create.Body.Close()
	loc, _ := url.Parse(create.Header.Get("Location"))
	if create.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc.Path, "/orgs/") {
		t.Fatalf("create org = %d %q, want 303 → /orgs/{slug}", create.StatusCode, create.Header.Get("Location"))
	}
	slug := strings.TrimPrefix(loc.Path, "/orgs/")

	// #44 edit (renames the slug) → 303 to Settings at the new canonical URL.
	edit := post(t, ts, h.app, "/orgs/"+slug+"/edit",
		url.Values{"name": {"Renamed Org"}, "slug": {"renamed-org"}, "description": {"desc"}, "region": {"NA"}}, sess)
	edit.Body.Close()
	if loc, _ := url.Parse(edit.Header.Get("Location")); edit.StatusCode != http.StatusSeeOther || loc.Path != "/orgs/renamed-org/settings" {
		t.Fatalf("edit org = %d %q, want 303 → /orgs/renamed-org/settings", edit.StatusCode, edit.Header.Get("Location"))
	}
	slug = "renamed-org"

	// #104 links → 303 back to Settings.
	links := post(t, ts, h.app, "/orgs/"+slug+"/links",
		url.Values{"link_platform": {"website"}, "link_label": {"Home"}, "link_url": {"https://example.org"}}, sess)
	links.Body.Close()
	if loc, _ := url.Parse(links.Header.Get("Location")); links.StatusCode != http.StatusSeeOther || loc.Path != "/orgs/"+slug+"/settings" {
		t.Fatalf("set org links = %d %q, want 303 → /orgs/%s/settings", links.StatusCode, links.Header.Get("Location"), slug)
	}

	// #48 member role — promote a second member to admin.
	orgID, ok := func() (int64, bool) { id, err := st.OrgIDBySlug(ctx, slug); return id, err == nil }()
	if !ok {
		t.Fatal("could not resolve renamed org slug")
	}
	other, err := st.CreateUser(ctx, "orgmember", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, orgID, other.ID, "member"); err != nil {
		t.Fatal(err)
	}
	promote := post(t, ts, h.app, "/orgs/"+slug+"/members/"+strconv.FormatInt(other.ID, 10),
		url.Values{"action": {"promote"}}, sess)
	promote.Body.Close()
	if loc, _ := url.Parse(promote.Header.Get("Location")); promote.StatusCode != http.StatusSeeOther || loc.Path != "/orgs/"+slug+"/members" {
		t.Fatalf("promote member = %d %q, want 303 → members", promote.StatusCode, promote.Header.Get("Location"))
	}
	if admin, _ := st.IsOrgAdmin(ctx, orgID, other.ID); !admin {
		t.Fatal("promote did not make the member an admin")
	}
}

// #56 update config profile, #57 delete config profile, and the matching region
// writes (#107 update, #108 delete, #110 area, #111 root flood). (Profile create #54
// and region create #59 are covered by TestOrgConfigProfilesFlow; the modal/area
// surfaces in config_region_modal_test.go.)
func TestOrgConfigProfileUpdateDelete(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "cfgadmin")
	org, err := st.CreateOrg(ctx, "Cfg Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := st.CreateProfile(ctx, org.ID, "ESP32", nil)
	if err != nil {
		t.Fatal(err)
	}
	base := "/orgs/" + org.Slug + "/config"

	upd := post(t, ts, h.app, base+"/profiles/"+strconv.FormatInt(pid, 10),
		url.Values{"profile_name": {"Heltec"}, "profile_steps": {"# base"}}, sess)
	upd.Body.Close()
	// Both land back on Settings › Profiles, where profiles are managed.
	profiles := "/orgs/" + org.Slug + "/settings/profiles"
	if loc, _ := url.Parse(upd.Header.Get("Location")); upd.StatusCode != http.StatusSeeOther || loc.Path != profiles {
		t.Fatalf("update profile = %d %q, want 303 → %s", upd.StatusCode, upd.Header.Get("Location"), profiles)
	}

	del := post(t, ts, h.app, base+"/profiles/"+strconv.FormatInt(pid, 10)+"/delete", url.Values{}, sess)
	del.Body.Close()
	if loc, _ := url.Parse(del.Header.Get("Location")); del.StatusCode != http.StatusSeeOther || loc.Path != profiles {
		t.Fatalf("delete profile = %d %q, want 303 → %s", del.StatusCode, del.Header.Get("Location"), profiles)
	}

	// Regions mirror the same shape: every write lands back on Settings › Regions.
	regions := "/orgs/" + org.Slug + "/settings/regions"
	rid, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "buf", DisplayName: "Buffalo", AllowFlood: true})
	if err != nil {
		t.Fatal(err)
	}
	region := base + "/regions/" + strconv.FormatInt(rid, 10)
	for _, step := range []struct {
		name string
		path string
		form url.Values
	}{
		{"update region", region, url.Values{"region_token": {"buf"}, "region_display": {"Buffalo NY"}}},
		{"save area", region + "/area", url.Values{"region_geojson": {`{"type":"Polygon","coordinates":[[[30,10],[40,10],[40,20],[30,20],[30,10]]]}`}}},
		{"root flood", base + "/root-flood", url.Values{"root_allow_flood": {"1"}}},
		{"delete region", region + "/delete", url.Values{}},
	} {
		resp := post(t, ts, h.app, step.path, step.form, sess)
		resp.Body.Close()
		if loc, _ := url.Parse(resp.Header.Get("Location")); resp.StatusCode != http.StatusSeeOther || loc.Path != regions {
			t.Errorf("%s = %d %q, want 303 → %s", step.name, resp.StatusCode, resp.Header.Get("Location"), regions)
		}
	}
}

// #46 join, #47 leave — against an org owned by someone else.
func TestOrgJoinLeavePosts(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, err := st.CreateUser(ctx, "otherowner", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "Joinable Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	joiner, sess := appLogin(t, ts, st, ctx, h.app, "joiner")

	join := post(t, ts, h.app, "/orgs/"+org.Slug+"/join", url.Values{}, sess)
	join.Body.Close()
	if loc, _ := url.Parse(join.Header.Get("Location")); join.StatusCode != http.StatusSeeOther || loc.Path != "/orgs/"+org.Slug {
		t.Fatalf("join = %d %q, want 303 → /orgs/%s", join.StatusCode, join.Header.Get("Location"), org.Slug)
	}
	if _, isMember, _ := st.OrgRole(ctx, org.ID, joiner.ID); !isMember {
		t.Fatal("join did not add membership")
	}

	leave := post(t, ts, h.app, "/orgs/"+org.Slug+"/leave", url.Values{}, sess)
	leave.Body.Close()
	if loc, _ := url.Parse(leave.Header.Get("Location")); leave.StatusCode != http.StatusSeeOther || loc.Path != "/orgs" {
		t.Fatalf("leave = %d %q, want 303 → /orgs", leave.StatusCode, leave.Header.Get("Location"))
	}
	if _, isMember, _ := st.OrgRole(ctx, org.ID, joiner.ID); isMember {
		t.Fatal("leave did not remove membership")
	}
}

// TestOrgSettingsProfileValidation: a rejected Settings › Profile form re-renders
// the page (200) with the error and the entered values kept — never a redirect
// that drops the work. A save redirects back with a confirmation.
func TestOrgSettingsProfileValidation(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "orgvalidadmin")
	org, err := st.CreateOrg(ctx, "Valid Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := "/orgs/" + org.Slug

	// Invalid profile (empty name): the page again, entered slug preserved.
	bad := post(t, ts, h.app, base+"/edit",
		url.Values{"name": {""}, "slug": {"keep-this-slug"}, "region": {"R"}, "description": {"D"}}, sess)
	body := readBody(t, bad)
	if bad.StatusCode != http.StatusOK {
		t.Fatalf("invalid profile POST = %d, want 200 (re-rendered page)", bad.StatusCode)
	}
	if !strings.Contains(body, "Enter an organization name.") || !strings.Contains(body, `data-testid="settings-form-error"`) {
		t.Fatalf("re-rendered profile page missing the error:\n%s", body)
	}
	if !strings.Contains(body, `value="keep-this-slug"`) || !strings.Contains(body, `data-testid="org-settings-nav"`) {
		t.Fatalf("re-rendered profile page didn't keep the entered values:\n%s", body)
	}

	// Valid profile: back to Settings at the new canonical URL, with a confirmation.
	good := post(t, ts, h.app, base+"/edit",
		url.Values{"name": {"Renamed"}, "slug": {"renamed-valid"}, "region": {""}, "description": {""}}, sess)
	good.Body.Close()
	if loc, _ := url.Parse(good.Header.Get("Location")); good.StatusCode != http.StatusSeeOther ||
		loc.Path != "/orgs/renamed-valid/settings" || loc.Query().Get("ok") == "" {
		t.Fatalf("valid profile POST = %d %q, want 303 → /orgs/renamed-valid/settings?ok=…", good.StatusCode, good.Header.Get("Location"))
	}

	// Invalid links (bad URL): the page again with the bad value kept.
	badLinks := post(t, ts, h.app, "/orgs/renamed-valid/links",
		url.Values{"link_platform": {"website"}, "link_label": {""}, "link_url": {"not a url"}}, sess)
	lb := readBody(t, badLinks)
	if badLinks.StatusCode != http.StatusOK || !strings.Contains(lb, "data-link-editor") || !strings.Contains(lb, "not a url") {
		t.Fatalf("re-rendered links form didn't keep the bad entry:\n%s", lb)
	}
}

// TestOrgSettingsProfileInline: Settings › Profile (admins only) edits the org's
// profile, links and radio in place — three forms on the page, no modals. Home
// is read-only for everyone.
func TestOrgSettingsProfileInline(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "orgeditadmin") // creator = admin
	org, err := st.CreateOrg(ctx, "Edit Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}

	settings := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/settings", sess))
	for _, want := range []string{
		`action="/orgs/` + org.Slug + `/edit"`, `name="slug"`, // profile
		`action="/orgs/` + org.Slug + `/links"`, "data-link-editor", // links
		`action="/orgs/` + org.Slug + `/config/radio"`, `name="radio_freq_mhz"`, // radio
	} {
		if !strings.Contains(settings, want) {
			t.Errorf("Settings › Profile missing %q", want)
		}
	}
	if strings.Contains(settings, `data-bs-toggle="modal"`) {
		t.Error("Settings › Profile still uses a modal")
	}
	if home := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug, sess)); strings.Contains(home, `action="/orgs/`+org.Slug+`/edit"`) {
		t.Fatal("org Home has the profile form")
	}

	// The old modal fragments are gone.
	for _, p := range []string{"/edit", "/links", "/config/radio/edit"} {
		if got := status(t, do(t, ts, h.app, "/orgs/"+org.Slug+p, sess)); got == http.StatusOK {
			t.Errorf("GET %s still serves a page", p)
		}
	}

	// A plain member: no forms on Home, and Settings 404s.
	member, msess := appLogin(t, ts, st, ctx, h.app, "orgeditmember")
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	if got := status(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/settings", msess)); got != http.StatusNotFound {
		t.Fatalf("member GET /settings = %d, want 404", got)
	}
}

// TestOrgTabsHeaderConsistent: every org page renders the same header and tabs —
// Home, Members, Repeaters, Check-ins, and Settings for admins — with the tab the
// page lives under marked active, and its sub-nav where the tab groups pages.
// Plain members get no Settings tab.
func TestOrgTabsHeaderConsistent(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "tabsadmin") // creator = admin member
	org, err := st.CreateOrg(ctx, "Tabs Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	member, msess := appLogin(t, ts, st, ctx, h.app, "tabsmember")
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	activeTab := regexp.MustCompile(`class="nav-link active"[^>]*data-testid="(org-tab-[a-z]+)"`)
	for _, c := range []struct{ path, tab, subnav string }{
		{"", "org-tab-home", ""},
		{"/members", "org-tab-members", ""},
		{"/repeaters", "org-tab-repeaters", ""},
		{"/nets", "org-tab-checkins", "org-checkins-nav"},
		{"/activity", "org-tab-checkins", "org-checkins-nav"},
		{"/config", "org-tab-home", ""}, // the read-only recommended configuration
		{"/settings", "org-tab-settings", "org-settings-nav"},
		{"/settings/regions", "org-tab-settings", "org-settings-nav"},
		{"/settings/profiles", "org-tab-settings", "org-settings-nav"},
		{"/settings/mqtt", "org-tab-settings", "org-settings-nav"},
		{"/settings/mqtt/observers/new", "org-tab-settings", "org-settings-nav"},
		{"/settings/nets", "org-tab-settings", "org-settings-nav"},
		{"/settings/nets/new", "org-tab-settings", "org-settings-nav"},
		{"/config/regions/new", "org-tab-settings", "org-settings-nav"},
		{"/config/profiles/new", "org-tab-settings", "org-settings-nav"},
	} {
		body := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+c.path, sess))
		if !strings.Contains(body, `data-testid="org-public-page"`) {
			t.Errorf("%q: header lacks the public page link", c.path)
		}
		if strings.Contains(body, `data-testid="org-actions"`) {
			t.Errorf("%q: the old Actions menu is back", c.path)
		}
		for _, tab := range []string{"org-tab-home", "org-tab-members", "org-tab-repeaters", "org-tab-checkins", "org-tab-settings"} {
			if !strings.Contains(body, `data-testid="`+tab+`"`) {
				t.Errorf("%q: missing tab %s", c.path, tab)
			}
		}
		if m := activeTab.FindStringSubmatch(body); m == nil || m[1] != c.tab {
			t.Errorf("%q: active tab = %v, want %s", c.path, m, c.tab)
		}
		if c.subnav != "" && !strings.Contains(body, `data-testid="`+c.subnav+`"`) {
			t.Errorf("%q: missing %s", c.path, c.subnav)
		}
	}

	// A plain member: no Settings tab, and the admin pages 404.
	if body := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug, msess)); strings.Contains(body, `data-testid="org-tab-settings"`) ||
		!strings.Contains(body, `data-testid="leave-org"`) {
		t.Error("plain member's Home: Settings tab shown, or Leave missing")
	}
	for _, p := range []string{"/settings", "/settings/mqtt", "/settings/mqtt/observers/new", "/settings/nets/new"} {
		if got := status(t, do(t, ts, h.app, "/orgs/"+org.Slug+p, msess)); got != http.StatusNotFound {
			t.Errorf("plain member GET %s = %d, want 404", p, got)
		}
	}
	// An unknown section 404s; the old Observers address redirects to Settings.
	if got := status(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/settings/nope", sess)); got != http.StatusNotFound {
		t.Errorf("unknown settings section = %d, want 404", got)
	}
	old := do(t, ts, h.app, "/orgs/"+org.Slug+"/observers", sess)
	old.Body.Close()
	if old.StatusCode != http.StatusMovedPermanently || old.Header.Get("Location") != "/orgs/"+org.Slug+"/settings/mqtt" {
		t.Errorf("old /observers = %d %q, want 301 → Settings › MQTT", old.StatusCode, old.Header.Get("Location"))
	}
}
