package core

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestPathHashSetting: an admin sets the org's path hash size on Settings ›
// Radio; the Guide tells members (with the firmware 1.14 caveat for 2 and 3
// bytes); a profile can use {{ path.hash.mode }}, filled in on the
// configuration page; the setting can't be cleared while that profile uses it;
// a mode outside 0–2 is refused; and a plain member can't change it.
func TestPathHashSetting(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "hashadmin")
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "hashmember")
	org, err := st.CreateOrg(ctx, "Hash Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	set := func(mode string, cookie *http.Cookie) *http.Response {
		return post(t, ts, h.app, "/orgs/"+org.Slug+"/config/path-hash", url.Values{"path_hash_mode": {mode}}, cookie)
	}
	guide := "/orgs/" + org.Slug + "/guide"

	if body := getBody(t, ts, h.root, guide); strings.Contains(body, `data-testid="guide-path-hash"`) {
		t.Error("the Guide shows a path hash size the org hasn't set")
	}
	// A profile can't use the placeholder before the org sets it.
	bad := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles",
		url.Values{"profile_name": {"Hash"}, "profile_steps": {"set path.hash.mode {{ path.hash.mode }}"}}, sess)
	if body := readBody(t, bad); bad.StatusCode != http.StatusOK || !strings.Contains(body, "set it under Settings › Radio first") {
		t.Fatalf("placeholder profile saved without a path hash size: %d", bad.StatusCode)
	}

	resp := set("1", sess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/orgs/"+org.Slug+"/settings/radio?") {
		t.Fatalf("save = %d → %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if m, _ := st.OrgPathHashMode(ctx, org.ID); m == nil || *m != 1 {
		t.Fatalf("saved mode = %v, want 1", m)
	}
	body := getBody(t, ts, h.root, guide)
	if _, hash, ok := strings.Cut(body, `data-testid="guide-path-hash"`); !ok || !strings.Contains(hash, "2 bytes") || !strings.Contains(hash, "firmware 1.14") {
		t.Error("the Guide doesn't give the path hash size and its firmware caveat")
	}

	ok := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles",
		url.Values{"profile_name": {"Hash"}, "profile_steps": {"set path.hash.mode {{ path.hash.mode }}"}}, sess)
	ok.Body.Close()
	if ok.StatusCode != http.StatusSeeOther {
		t.Fatalf("placeholder profile = %d, want 303", ok.StatusCode)
	}
	if page := getBody(t, ts, h.root, "/orgs/"+org.Slug+"/guide/repeaters?profile=Hash"); !strings.Contains(page, "set path.hash.mode 1") {
		t.Error("the configuration page doesn't fill in {{ path.hash.mode }}")
	}
	clear := set("", sess)
	if body := readBody(t, clear); clear.StatusCode != http.StatusOK || !strings.Contains(body, "Remove it from every profile") {
		t.Errorf("clearing a used path hash size = %d", clear.StatusCode)
	}
	if body := readBody(t, set("3", sess)); !strings.Contains(body, "Pick a path hash size of 1, 2 or 3 bytes") {
		t.Error("the reserved mode 3 wasn't refused")
	}
	set("0", memberSess).Body.Close()
	if m, _ := st.OrgPathHashMode(ctx, org.ID); m == nil || *m != 1 {
		t.Errorf("a plain member changed the path hash mode to %v", m)
	}
}

// TestDefaultScopeSetting: an admin marks one region the default scope on its
// edit page, separately from the primary region, and the Guide tells members to
// set it — even before the org has set its radio; without one the Guide says
// nothing about scope.
func TestDefaultScopeSetting(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "scopeadmin")
	org, err := st.CreateOrg(ctx, "Scope Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	guide := "/orgs/" + org.Slug + "/guide"
	if _, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "us", DisplayName: "United States", Layer: 1, Primary: true}); err != nil {
		t.Fatal(err)
	}
	if body := getBody(t, ts, h.root, guide); strings.Contains(body, `data-testid="guide-default-scope"`) {
		t.Error("the Guide recommends a default scope the org hasn't set")
	}

	resp := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions", url.Values{
		"region_token": {"lake"}, "region_display": {"Lakeshore"}, "region_layer": {"2"},
		"region_default_scope": {"1"}, "region_allow_flood": {"1"},
	}, sess)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create region = %d", resp.StatusCode)
	}
	body := getBody(t, ts, h.root, guide)
	if _, scope, ok := strings.Cut(body, `data-testid="guide-default-scope"`); !ok || !strings.Contains(scope, "<code>lake</code> · Lakeshore") {
		t.Error("the Guide doesn't name the default scope")
	}
	regions, err := st.ListRegions(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, z := range regions {
		if z.Token == "us" && (!z.Primary || z.DefaultScope) {
			t.Errorf("us: primary %v, default scope %v; want primary only", z.Primary, z.DefaultScope)
		}
	}
	if page := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions/new", sess); !strings.Contains(page, `name="region_default_scope"`) {
		t.Error("the region form lacks the default scope toggle")
	}
}
