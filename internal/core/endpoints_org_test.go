package core

import (
	"net/http"
	"net/http/httptest"
	"net/url"
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

	// #44 edit (renames the slug) → 303 to the new canonical URL.
	edit := post(t, ts, h.app, "/orgs/"+slug+"/edit",
		url.Values{"name": {"Renamed Org"}, "slug": {"renamed-org"}, "description": {"desc"}, "region": {"NA"}}, sess)
	edit.Body.Close()
	if loc, _ := url.Parse(edit.Header.Get("Location")); edit.StatusCode != http.StatusSeeOther || loc.Path != "/orgs/renamed-org" {
		t.Fatalf("edit org = %d %q, want 303 → /orgs/renamed-org", edit.StatusCode, edit.Header.Get("Location"))
	}
	slug = "renamed-org"

	// #104 links → 303 back to the org page.
	links := post(t, ts, h.app, "/orgs/"+slug+"/links",
		url.Values{"link_platform": {"website"}, "link_label": {"Home"}, "link_url": {"https://example.org"}}, sess)
	links.Body.Close()
	if loc, _ := url.Parse(links.Header.Get("Location")); links.StatusCode != http.StatusSeeOther || loc.Path != "/orgs/"+slug {
		t.Fatalf("set org links = %d %q, want 303 → /orgs/%s", links.StatusCode, links.Header.Get("Location"), slug)
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
	// Both land back on the config page, where profiles are managed.
	if loc, _ := url.Parse(upd.Header.Get("Location")); upd.StatusCode != http.StatusSeeOther || loc.Path != base {
		t.Fatalf("update profile = %d %q, want 303 → config", upd.StatusCode, upd.Header.Get("Location"))
	}

	del := post(t, ts, h.app, base+"/profiles/"+strconv.FormatInt(pid, 10)+"/delete", url.Values{}, sess)
	del.Body.Close()
	if loc, _ := url.Parse(del.Header.Get("Location")); del.StatusCode != http.StatusSeeOther || loc.Path != base {
		t.Fatalf("delete profile = %d %q, want 303 → config", del.StatusCode, del.Header.Get("Location"))
	}

	// Regions mirror the same shape: every write lands back on the config page.
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
		if loc, _ := url.Parse(resp.Header.Get("Location")); resp.StatusCode != http.StatusSeeOther || loc.Path != base {
			t.Errorf("%s = %d %q, want 303 → config", step.name, resp.StatusCode, resp.Header.Get("Location"))
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

// postHX posts a form with the HX-Request header, as an htmx modal submit does.
func postHX(t *testing.T, ts *httptest.Server, host, path string, form url.Values, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("post %s%s: %v", host, path, err)
	}
	return resp
}

// TestOrgEditModalValidationKeepsModal: a validation error on an htmx modal submit
// re-renders the modal in place (200) with the error and the entered values kept —
// never a redirect that drops the modal and the work. Success sends HX-Redirect.
func TestOrgEditModalValidationKeepsModal(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "orgvalidadmin")
	org, err := st.CreateOrg(ctx, "Valid Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	base := "/orgs/" + org.Slug

	// Invalid profile (empty name): re-rendered modal, entered slug preserved.
	bad := postHX(t, ts, h.app, base+"/edit",
		url.Values{"name": {""}, "slug": {"keep-this-slug"}, "region": {"R"}, "description": {"D"}}, sess)
	body := readBody(t, bad)
	if bad.StatusCode != http.StatusOK {
		t.Fatalf("invalid profile htmx POST = %d, want 200 (re-rendered modal)", bad.StatusCode)
	}
	if !strings.Contains(body, "Enter an organization name.") {
		t.Fatalf("re-rendered profile modal missing the error:\n%s", body)
	}
	if !strings.Contains(body, `value="keep-this-slug"`) || !strings.Contains(body, `form="org-profile-form"`) {
		t.Fatalf("re-rendered profile modal didn't preserve entered values:\n%s", body)
	}

	// Valid profile: HX-Redirect to the new canonical URL (closes the modal).
	good := postHX(t, ts, h.app, base+"/edit",
		url.Values{"name": {"Renamed"}, "slug": {"renamed-valid"}, "region": {""}, "description": {""}}, sess)
	good.Body.Close()
	if good.StatusCode != http.StatusOK || good.Header.Get("HX-Redirect") != "/orgs/renamed-valid" {
		t.Fatalf("valid profile htmx POST = %d HX-Redirect=%q, want 200 → /orgs/renamed-valid", good.StatusCode, good.Header.Get("HX-Redirect"))
	}

	// Invalid links (bad URL): re-rendered editor with the bad value kept.
	badLinks := postHX(t, ts, h.app, "/orgs/renamed-valid/links",
		url.Values{"link_platform": {"website"}, "link_label": {""}, "link_url": {"not a url"}}, sess)
	lb := readBody(t, badLinks)
	if badLinks.StatusCode != http.StatusOK || !strings.Contains(lb, "data-link-editor") || !strings.Contains(lb, "not a url") {
		t.Fatalf("re-rendered links modal didn't preserve the bad entry:\n%s", lb)
	}
}

// TestOrgEditModals: the Home tab shows read-only profile/links for everyone, with
// admin-only "Edit" buttons that open modal fragments (admin-gated GET endpoints).
func TestOrgEditModals(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "orgeditadmin") // creator = admin
	org, err := st.CreateOrg(ctx, "Edit Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Admin Home: read-only About + the two Edit buttons.
	home := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug, sess))
	if !strings.Contains(home, `data-testid="edit-profile"`) || !strings.Contains(home, `data-testid="edit-links"`) {
		t.Fatal("admin org Home missing the Edit buttons")
	}
	if strings.Contains(home, `action="/orgs/`+org.Slug+`/edit"`) {
		t.Fatal("admin org Home should not render the profile form inline (it's in a modal now)")
	}

	// Profile modal fragment: the form, no page chrome.
	prof := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/edit", sess))
	if !strings.Contains(prof, `form="org-profile-form"`) || !strings.Contains(prof, `name="name"`) || !strings.Contains(prof, `name="slug"`) {
		t.Fatalf("profile modal fragment missing expected fields:\n%s", prof)
	}
	if strings.Contains(prof, "back-link") {
		t.Fatal("profile modal fragment should be modal chrome, not a full page")
	}

	// Links modal fragment: the link editor.
	links := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/links", sess))
	if !strings.Contains(links, "data-link-editor") || !strings.Contains(links, `form="org-links-form"`) {
		t.Fatalf("links modal fragment missing the editor:\n%s", links)
	}

	// A plain member: read-only Home, no Edit buttons, and the modal GETs 404.
	member, msess := appLogin(t, ts, st, ctx, h.app, "orgeditmember")
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	mhome := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug, msess))
	if strings.Contains(mhome, `data-testid="edit-profile"`) || strings.Contains(mhome, `data-testid="edit-links"`) {
		t.Fatal("non-admin member should not see the Edit buttons")
	}
	for _, p := range []string{"/edit", "/links"} {
		resp := do(t, ts, h.app, "/orgs/"+org.Slug+p, msess)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("non-admin GET %s = %d, want 404", p, resp.StatusCode)
		}
	}
}

// TestOrgTabsHeaderConsistent: the shared org-header renders the Actions menu on
// EVERY member tab (not just Home) — the fix for the menu vanishing when you switch
// tabs.
func TestOrgTabsHeaderConsistent(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, sess := appLogin(t, ts, st, ctx, h.app, "tabsadmin") // creator = admin member
	org, err := st.CreateOrg(ctx, "Tabs Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tab := range []string{"", "/members", "/repeaters", "/config"} {
		body := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+tab, sess))
		for _, want := range []string{">Actions<", "Leave organization", "View public page"} {
			if !strings.Contains(body, want) {
				t.Errorf("tab %q missing %q from the shared Actions header", tab, want)
			}
		}
		// Configuration is reached by its own tab, which an admin always sees — even
		// before the org has any config — so the Actions menu doesn't duplicate it.
		if strings.Contains(body, "Edit configuration") {
			t.Errorf("tab %q still offers Edit configuration in the Actions menu", tab)
		}
		if !strings.Contains(body, "/orgs/"+org.Slug+"/config") {
			t.Errorf("tab %q does not link to the Configuration tab", tab)
		}
	}
}
