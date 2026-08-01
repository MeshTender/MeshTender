package core

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jleight/meshtender/internal/analytics"
	"github.com/jleight/meshtender/internal/store"
)

// fetchPage GETs a path on a host and returns its body, failing on non-200.
func fetchPage(t *testing.T, ts *httptest.Server, host, path string) string {
	t.Helper()
	resp := do(t, ts, host, path)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s%s = %d, want 200", host, path, resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	return string(raw)
}

// TestLegalPagesArePublic: both documents must render for a signed-out visitor,
// on the public host, with no session. Someone deciding whether to sign up has to
// be able to read them first.
func TestLegalPagesArePublic(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	for _, tc := range []struct{ path, want string }{
		{"/privacy", "What we store"},
		{"/terms", "Fair use"},
	} {
		body := fetchPage(t, ts, h.root, tc.path)
		if !strings.Contains(body, tc.want) {
			t.Errorf("%s does not contain %q", tc.path, tc.want)
		}
		// They cross-reference each other, so a reader lands on both.
		if !strings.Contains(body, "/privacy") || !strings.Contains(body, "/terms") {
			t.Errorf("%s does not link to its sibling document", tc.path)
		}
	}
}

// TestFooterLinksToLegalPages is audit finding U1: the footer had no links at
// all. They must appear on EVERY surface — the app and auth hosts too, where the
// links have to be absolute because only the root host serves those routes.
func TestFooterLinksToLegalPages(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	// Root host: a public page, signed out.
	rootBody := fetchPage(t, ts, h.root, "/")
	for _, want := range []string{"/privacy", "/terms"} {
		if !strings.Contains(rootBody, want) {
			t.Errorf("root landing page footer has no %s link", want)
		}
	}

	// App host, signed in: the links must be absolute to the root host, since
	// /privacy doesn't exist here.
	_, sess := appLogin(t, ts, st, ctx, h.app, "footeruser")
	resp := do(t, ts, h.app, "/", sess)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	appBody := string(raw)
	if !strings.Contains(appBody, `href="http`) || !strings.Contains(appBody, "/privacy") {
		t.Error("app dashboard footer has no privacy link")
	}
	if strings.Contains(appBody, `href="/privacy"`) {
		t.Error("app footer links /privacy relatively; the app host doesn't serve it")
	}

	// And the route really is absent on the app host, which is what makes the
	// absolute link necessary rather than a style choice.
	missing := do(t, ts, h.app, "/privacy")
	defer missing.Body.Close()
	if missing.StatusCode == http.StatusOK {
		t.Error("the app host now serves /privacy; the absolute footer link is no longer required")
	}
}

// TestPrivacyPageStatesRealRetention binds the published retention windows to the
// constants the code actually enforces. A privacy policy is a promise, and the
// cheapest way for it to become a lie is for someone to tune a TTL and never
// think about this page.
func TestPrivacyPageStatesRealRetention(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	body := fetchPage(t, ts, h.root, "/privacy")

	for _, tc := range []struct {
		what string
		want string
	}{
		{"analytics retention", fmt.Sprintf("%d days", analytics.RetentionDays)},
		{"password reset TTL", durationPhrase(store.ResetTokenTTL)},
		{"email verify TTL", durationPhrase(store.VerifyTokenTTL)},
		{"handoff code TTL", durationPhrase(store.AuthCodeTTL)},
		{"share link TTL", durationPhrase(store.InviteTTL)},
		{"expired link grace", durationPhrase(store.ExpiredInviteGrace)},
		{"username cooldown", durationPhrase(store.UsernameReleaseCooldown)},
	} {
		if !strings.Contains(body, tc.want) {
			t.Errorf("privacy page doesn't state the real %s (%q). "+
				"Either the constant changed and the page is now wrong, or the wording drifted.",
				tc.what, tc.want)
		}
	}
}

// durationPhrase renders a retention window the way the privacy page words it.
// It picks the largest unit that divides evenly AND yields at least two of them,
// which is how the prose reads naturally: 24 hours rather than "1 day", 60
// seconds rather than "1 minute", but 90 days rather than 2160 hours.
func durationPhrase(d time.Duration) string {
	units := []struct {
		size time.Duration
		name string
	}{
		{24 * time.Hour, "day"},
		{time.Hour, "hour"},
		{time.Minute, "minute"},
		{time.Second, "second"},
	}
	for _, u := range units {
		if d%u.size == 0 && d/u.size >= 2 {
			return fmt.Sprintf("%d %ss", d/u.size, u.name)
		}
	}
	return d.String()
}
