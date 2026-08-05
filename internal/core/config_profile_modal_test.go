package core

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestConfigProfileEditorSurfaces: the profile editor serves the modal fragment to
// htmx and the standalone page to everything else (the no-JS fallback), a
// validation error re-renders the fragment in place with the entered values kept,
// and a save sends the browser back to the config page with that profile selected.
func TestConfigProfileEditorSurfaces(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "profmodaladmin")
	org, err := st.CreateOrg(ctx, "Modal Org", admin.ID) // creator becomes admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// htmx GET → just the modal fragment (no page chrome), posting to the collection.
	frag := readBody(t, doHX(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles/new", sess))
	for _, want := range []string{"modal-header", "Add profile", `hx-post="/orgs/` + org.Slug + `/config/profiles"`} {
		if !strings.Contains(frag, want) {
			t.Fatalf("new-profile fragment missing %q", want)
		}
	}
	if strings.Contains(frag, "<html") {
		t.Fatal("htmx request should get a bare fragment, not the full page")
	}

	// Plain GET → the full page fallback, with no htmx attributes to hijack it.
	page := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles/new", sess))
	if !strings.Contains(page, "<html") || !strings.Contains(page, "Create profile") {
		t.Fatal("non-htmx request should get the standalone editor page")
	}
	if strings.Contains(page, "hx-post") {
		t.Fatal("the fallback page must not carry hx-post (its target doesn't exist there)")
	}

	// A validation error on an htmx submit keeps the modal: 200 with the fragment,
	// the error, and the entered values — never a redirect that drops the work.
	bad := postHX(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles",
		url.Values{"profile_name": {"ESP32"}, "profile_steps": {"definitely not a command"}}, sess)
	badBody := readBody(t, bad)
	if bad.StatusCode != http.StatusOK || bad.Header.Get("HX-Redirect") != "" {
		t.Fatalf("invalid submit = %d (HX-Redirect %q), want 200 with no redirect", bad.StatusCode, bad.Header.Get("HX-Redirect"))
	}
	for _, want := range []string{"Nothing was saved", "definitely not a command", `value="ESP32"`} {
		if !strings.Contains(badBody, want) {
			t.Fatalf("re-rendered fragment missing %q", want)
		}
	}

	// A valid htmx submit closes the modal via HX-Redirect onto the saved profile.
	ok := postHX(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles",
		url.Values{"profile_name": {"ESP32"}, "profile_steps": {"# esp base"}}, sess)
	ok.Body.Close()
	if got, want := ok.Header.Get("HX-Redirect"), "/orgs/"+org.Slug+"/config?profile=ESP32"; ok.StatusCode != http.StatusOK || got != want {
		t.Fatalf("valid submit = %d HX-Redirect %q, want 200 → %q", ok.StatusCode, got, want)
	}

	profiles, err := st.ListProfiles(ctx, org.ID)
	if err != nil || len(profiles) != 1 {
		t.Fatalf("ListProfiles = %v, %v; want one profile", profiles, err)
	}
	pid := strconv.FormatInt(profiles[0].ID, 10)

	// The edit fragment is pre-filled and posts to that profile's own URL.
	editFrag := readBody(t, doHX(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles/"+pid+"/edit", sess))
	for _, want := range []string{"Edit profile", `value="ESP32"`, "# esp base",
		`hx-post="/orgs/` + org.Slug + `/config/profiles/` + pid + `"`} {
		if !strings.Contains(editFrag, want) {
			t.Fatalf("edit fragment missing %q", want)
		}
	}

	// Deleting from the list row (a plain POST) returns to the config page.
	del := post(t, ts, h.app, "/orgs/"+org.Slug+"/config/profiles/"+pid+"/delete", url.Values{}, sess)
	del.Body.Close()
	if got, want := del.Header.Get("Location"), "/orgs/"+org.Slug+"/config"; del.StatusCode != http.StatusSeeOther || got != want {
		t.Fatalf("delete = %d → %q, want 303 → %q", del.StatusCode, got, want)
	}
}

// TestConfigPageProfileEditingIsAdminOnly: the inline profile controls (add, edit,
// delete, and the editor modal itself) render only for an org admin — never for a
// plain member, and never on the anonymous public surface, which shares this
// template.
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

	adminView := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", adminSess))
	for _, want := range []string{`data-testid="config-profile-add"`, `data-testid="config-profile-edit"`,
		`data-testid="config-profile-delete"`, `id="config-profile-modal"`} {
		if !strings.Contains(adminView, want) {
			t.Fatalf("admin config view missing %q", want)
		}
	}
	// Selecting a profile is a plain link on both surfaces.
	if !strings.Contains(adminView, "/config?profile=ESP32") {
		t.Fatal("profile rows should link to ?profile=<name>")
	}

	for _, v := range []struct {
		name string
		body string
	}{
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

// TestConfigPageEmptyStates: an admin with no config yet gets the profile card
// (with Add profile) plus an explanation of what a profile is, so there's an entry
// point; everyone else still sees the plain "nothing defined yet" note.
func TestConfigPageEmptyStates(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "cfgemptyadmin")
	org, err := st.CreateOrg(ctx, "Empty Cfg Org", admin.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	adminView := readBody(t, do(t, ts, h.app, "/orgs/"+org.Slug+"/config", sess))
	for _, want := range []string{`data-testid="config-profile-add"`, "What's a profile?",
		"A <strong>profile</strong> is", "No profiles yet."} {
		if !strings.Contains(adminView, want) {
			t.Fatalf("empty admin config view missing %q", want)
		}
	}

	anon := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug+"/config"))
	if !strings.Contains(anon, "hasn't defined any configuration yet") {
		t.Fatal("anonymous view of a config-less org should say so")
	}
	if strings.Contains(anon, "config-profile-add") {
		t.Fatal("anonymous view must not offer to add a profile")
	}
}
