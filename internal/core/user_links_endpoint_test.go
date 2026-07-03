package core

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// POST /account/links: a scheme-less domain is accepted and stored as https://,
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
	ok := post(t, ts, h.auth, "/account/links",
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
	bad := post(t, ts, h.auth, "/account/links", url.Values{
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
