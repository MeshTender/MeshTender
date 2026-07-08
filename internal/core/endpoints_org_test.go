package core

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
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

// #56 update config profile, #57 delete config profile. (Create #54 and regions
// #59 are covered by TestOrgConfigProfilesFlow.)
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
	if loc, _ := url.Parse(upd.Header.Get("Location")); upd.StatusCode != http.StatusSeeOther || loc.Path != base+"/edit" {
		t.Fatalf("update profile = %d %q, want 303 → config/edit", upd.StatusCode, upd.Header.Get("Location"))
	}

	del := post(t, ts, h.app, base+"/profiles/"+strconv.FormatInt(pid, 10)+"/delete", url.Values{}, sess)
	del.Body.Close()
	if loc, _ := url.Parse(del.Header.Get("Location")); del.StatusCode != http.StatusSeeOther || loc.Path != base+"/edit" {
		t.Fatalf("delete profile = %d %q, want 303 → config/edit", del.StatusCode, del.Header.Get("Location"))
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

// TestOrgTabsHeaderConsistent: the shared org-header renders the Actions menu on
// EVERY member tab (not just Home) — the fix for the menu vanishing when you switch
// tabs. Admins also get "Edit configuration" in it.
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
		for _, want := range []string{">Actions<", "Leave organization", "View public page", "Edit configuration"} {
			if !strings.Contains(body, want) {
				t.Errorf("tab %q missing %q from the shared Actions header", tab, want)
			}
		}
	}
}
