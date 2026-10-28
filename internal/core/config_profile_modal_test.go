package core

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestConfigProfileEditorPages: New and Edit are Settings pages of their own; a
// validation error re-renders the page with the entered values kept, and a save
// returns to Settings › Configuration profiles with that profile selected.
func TestConfigProfileEditorPages(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "profpageadmin")
	org, err := st.CreateOrg(ctx, "Profile Page Org", admin.ID) // creator becomes admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	page := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles/new", sess))
	for _, want := range []string{"New profile", `action="/orgs/` + org.Slug + `/config/profiles"`, `data-testid="org-settings-nav"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("new-profile page missing %q", want)
		}
	}

	// A validation error keeps the work: 200 with the page, the error, and the values.
	bad := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles",
		url.Values{"profile_name": {"ESP32"}, "profile_steps": {"definitely not a command"}}, sess)
	badBody := readBody(t, bad)
	if bad.StatusCode != http.StatusOK {
		t.Fatalf("invalid submit = %d, want 200 (re-rendered page)", bad.StatusCode)
	}
	for _, want := range []string{"Nothing was saved", "definitely not a command", `value="ESP32"`} {
		if !strings.Contains(badBody, want) {
			t.Fatalf("re-rendered page missing %q", want)
		}
	}

	ok := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles",
		url.Values{"profile_name": {"ESP32"}, "profile_steps": {"# esp base"}}, sess)
	ok.Body.Close()
	if got, want := ok.Header.Get("Location"), "/orgs/"+org.Slug+"/settings/profiles?profile=ESP32"; ok.StatusCode != http.StatusSeeOther || got != want {
		t.Fatalf("valid submit = %d → %q, want 303 → %q", ok.StatusCode, got, want)
	}

	profiles, err := st.ListProfiles(ctx, org.ID)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("ListProfiles = %v, %v; want one profile", profiles, err)
	}
	pid := strconv.FormatInt(profiles[0].ID, 10)

	// The edit page is pre-filled and posts to that profile's own URL.
	edit := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles/"+pid+"/edit", sess))
	for _, want := range []string{"Edit profile", `value="ESP32"`, "# esp base",
		`action="/orgs/` + org.Slug + `/config/profiles/` + pid + `"`} {
		if !strings.Contains(edit, want) {
			t.Fatalf("edit page missing %q", want)
		}
	}

	// Deleting from the list row (a plain POST) returns to the list.
	del := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles/"+pid+"/delete", url.Values{}, sess)
	del.Body.Close()
	if got, want := del.Header.Get("Location"), "/orgs/"+org.Slug+"/settings/profiles"; del.StatusCode != http.StatusSeeOther || got != want {
		t.Fatalf("delete = %d → %q, want 303 → %q", del.StatusCode, got, want)
	}
}

// TestConfigPageProfileEditingIsAdminOnly: the profile controls (new, view, edit,
// delete) live in Settings › Configuration profiles, for an org admin. The recommended configuration — for admins, members and the anonymous
// public surface alike — is read-only.
func TestConfigPageProfileEditingIsAdminOnly(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, adminSess := appLogin(t, ts, st, ctx, h.app, "cfgroleadmin")
	org, err := st.CreateOrg(ctx, "Role Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if _, err := st.CreateProfile(ctx, org.ID, "ESP32", nil); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	member, memberSess := appLogin(t, ts, st, ctx, h.app, "cfgrolemember")
	if err := st.AddOrgMember(ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}

	adminView := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/settings/profiles", adminSess))
	for _, want := range []string{`data-testid="config-profile-add"`, `data-testid="config-profile-view"`,
		`data-testid="config-profile-edit"`, `data-testid="config-profile-delete"`} {
		if !strings.Contains(adminView, want) {
			t.Fatalf("admin profile settings missing %q", want)
		}
	}
	// Selecting a profile is a plain link.
	if !strings.Contains(adminView, "/settings/profiles?profile=ESP32") {
		t.Fatal("profile rows should link to ?profile=<name>")
	}
	if got := status(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/settings/profiles", memberSess)); got != http.StatusNotFound {
		t.Fatalf("member GET profile settings = %d, want 404", got)
	}

	for _, v := range []struct {
		name string
		body string
	}{
		{"admin", readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", adminSess))},
		{"member", readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", memberSess))},
		{"anonymous", readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug+"/config"))},
	} {
		if !strings.Contains(v.body, "ESP32") {
			t.Fatalf("%s config view should still list the profile", v.name)
		}
		for _, forbidden := range []string{"config-profile-add", "config-profile-edit", "config-profile-delete",
			"config-profile-modal", "/config/profiles"} {
			if strings.Contains(v.body, forbidden) {
				t.Fatalf("%s config view leaked the admin control %q", v.name, forbidden)
			}
		}
	}
}

// TestConfigPageEmptyStates: an admin with no profiles yet gets New profile and an
// explanation of what a profile is in Settings › Configuration profiles, so there's
// an entry point; the recommended configuration just says nothing is defined yet.
func TestConfigPageEmptyStates(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "cfgemptyadmin")
	org, err := st.CreateOrg(ctx, "Empty Cfg Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	adminView := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/settings/profiles", sess))
	for _, want := range []string{`data-testid="config-profile-add"`, "No configuration profiles yet",
		"bring their repeater in spec"} {
		if !strings.Contains(adminView, want) {
			t.Fatalf("empty profile settings missing %q", want)
		}
	}

	anon := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug+"/config"))
	if !strings.Contains(anon, "No recommended configuration yet") {
		t.Fatal("anonymous view of a config-less org should say so")
	}
	if strings.Contains(anon, "config-profile-add") {
		t.Fatal("anonymous view must not offer to add a profile")
	}
}
