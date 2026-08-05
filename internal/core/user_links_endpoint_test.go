package core

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// POST /account/profile: a scheme-less domain is accepted and stored as https://,
// and a genuinely invalid row re-renders the editor (200) with the user's own
// rows preserved and an inline error — instead of redirecting and wiping their
// work, the bug this covers.
func TestUserLinksNormalizeAndPreserveOnError(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	sso := authSSO(t, ts, h, "linkeditor")

	u, err := st.GetUserByUsername(ctx, "linkeditor")
	if err != nil {
		t.Fatalf("load user: %v", err)
	}

	// A bare domain saves, normalized to https://.
	ok := post(t, ts, h.auth, "/account/profile",
		url.Values{"link_platform": {"website"}, "link_label": {"Home"}, "link_url": {"example.com"}}, sso)
	ok.Body.Close()
	assertAccountOK(t, ok, "bare-domain link")

	links, err := st.ListUserLinks(ctx, u.ID)
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	if len(links) != 1 || links[0].URL != "https://example.com" {
		t.Fatalf("stored links = %+v, want one https://example.com", links)
	}

	// Now submit two rows: a valid bare domain and an invalid email. The whole
	// save is rejected, but the editor must come back with both rows intact.
	bad := post(t, ts, h.auth, "/account/profile", url.Values{
		"link_platform": {"website", "email"},
		"link_label":    {"Site", ""},
		"link_url":      {"another.example", "notanemail"},
	}, sso)
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusOK {
		t.Fatalf("invalid link save = %d, want 200 (re-render)", bad.StatusCode)
	}
	body, err := io.ReadAll(bad.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	page := string(body)
	// The user's typed values survive the round-trip...
	if !strings.Contains(page, "another.example") || !strings.Contains(page, "notanemail") {
		t.Fatalf("re-rendered page dropped the submitted rows")
	}
	// ...and the specific error is shown.
	if !strings.Contains(page, "Enter a valid email address.") {
		t.Fatalf("re-rendered page missing the validation error")
	}
	// The failed save left the stored links untouched (still the single link).
	links, err = st.ListUserLinks(ctx, u.ID)
	if err != nil {
		t.Fatalf("list links after failed save: %v", err)
	}
	if len(links) != 1 || links[0].URL != "https://example.com" {
		t.Fatalf("stored links after failed save = %+v, want unchanged https://example.com", links)
	}
}

// The public-profile card is one form posting to one endpoint, so a single POST
// has to persist the display name, the text fields, and the link set together —
// and a bad link row has to reject all of it, keeping what the user typed in the
// other fields rather than storing a half-composed profile.
func TestAccountProfileSavesEveryFieldTogether(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	sso := authSSO(t, ts, h, "profileform")

	u, err := st.GetUserByUsername(ctx, "profileform")
	if err != nil {
		t.Fatalf("load user: %v", err)
	}

	ok := post(t, ts, h.auth, "/account/profile", url.Values{
		"display_name":  {"Grace Hopper"},
		"bio":           {"Compiles things."},
		"location":      {"Arlington, VA"},
		"callsign":      {"W1AW"},
		"link_platform": {"website", "email"},
		"link_label":    {"Home", ""},
		"link_url":      {"https://example.com", "grace@example.com"},
		"link_primary":  {"1"},
	}, sso)
	ok.Body.Close()
	assertAccountOK(t, ok, "combined profile save")

	saved, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if saved.DisplayName == nil || *saved.DisplayName != "Grace Hopper" {
		t.Fatalf("display name = %v, want Grace Hopper", saved.DisplayName)
	}
	if saved.Bio != "Compiles things." || saved.Location != "Arlington, VA" || saved.Callsign != "W1AW" {
		t.Fatalf("profile fields = %q/%q/%q, want the submitted values", saved.Bio, saved.Location, saved.Callsign)
	}
	links, err := st.ListUserLinks(ctx, u.ID)
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	if len(links) != 2 || !links[1].IsPrimary {
		t.Fatalf("stored links = %+v, want both rows with the email primary", links)
	}

	// One bad link row rejects the whole save: nothing changes in the database,
	// and the re-rendered form still holds the text the user had typed.
	bad := post(t, ts, h.auth, "/account/profile", url.Values{
		"display_name":  {"Rear Admiral Hopper"},
		"bio":           {"Still compiling."},
		"location":      {"Arlington"},
		"callsign":      {"K1ABC"},
		"link_platform": {"email"},
		"link_url":      {"not-an-address"},
	}, sso)
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusOK {
		t.Fatalf("invalid save = %d, want 200 (re-render)", bad.StatusCode)
	}
	body, err := io.ReadAll(bad.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	page := string(body)
	for _, want := range []string{"Rear Admiral Hopper", "Still compiling.", "K1ABC", "not-an-address"} {
		if !strings.Contains(page, want) {
			t.Fatalf("re-rendered form dropped %q", want)
		}
	}

	after, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload user after failed save: %v", err)
	}
	if after.Bio != "Compiles things." || after.Callsign != "W1AW" {
		t.Fatalf("failed save still wrote the text fields: %q/%q", after.Bio, after.Callsign)
	}
	if l, err := st.ListUserLinks(ctx, u.ID); err != nil || len(l) != 2 {
		t.Fatalf("failed save changed the links: %+v (%v)", l, err)
	}
}
