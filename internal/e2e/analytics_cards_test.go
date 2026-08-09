//go:build browser

package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestAnalyticsKindCards drives the traffic dashboard in a real browser after
// seeding one of every event kind. It asserts the split the whole `kind` column
// exists for: a scanner replaying a wordlist is counted in its own card and is
// absent from the visitor and busiest-pages figures, where it used to outweigh
// every real reader. Also catches a CSP trip on the new markup, which no Go
// handler test can see.
func TestAnalyticsKindCards(t *testing.T) {
	srv := newE2EServer(t)
	// login()'s first account bootstraps the capabilities that reach /admin.
	_, cookie := srv.login(t, "e2eadmin")

	now := time.Now()
	ev := func(path, kind, visitor string, status int) store.AnalyticsEvent {
		return store.AnalyticsEvent{
			Ts: now, Surface: "root", Host: "root.example", Path: path,
			Method: "GET", Status: status, Kind: kind, Visitor: visitor,
		}
	}
	evs := []store.AnalyticsEvent{
		ev("/", "visit", "alice", 200),
		ev("/docs", "visit", "bob", 200),
		ev("/gone", "notfound", "alice", 404),
		ev("/orgs", "bot", "googlebot", 200),
		ev("/catalog", "bot", "googlebot", 200),
	}
	// One scanner, five paths — the shape that swamped the dashboard.
	for _, p := range []string{"/.env", "/wp-login.php", "/.git/config", "/index.php", "/phpinfo.php"} {
		evs = append(evs, ev(p, "probe", "scanner", 404))
	}
	if err := srv.store.InsertAnalyticsEvents(srv.ctx, evs); err != nil {
		t.Fatalf("seed analytics events: %v", err)
	}
	// The path cards read the rollups, not raw events.
	if err := srv.store.RollupAnalytics(srv.ctx); err != nil {
		t.Fatalf("roll up analytics: %v", err)
	}

	ctx, cancel, watch := startBrowser(t)
	defer cancel()

	var probeReq, botReq, notFoundReq, totalReq, probeCard, pagesCard string
	if err := chromedp.Run(ctx,
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/admin/analytics"),
		chromedp.WaitVisible(`[data-testid="probe-card"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="probe-requests"]`, &probeReq, chromedp.ByQuery),
		chromedp.Text(`[data-testid="bot-requests"]`, &botReq, chromedp.ByQuery),
		chromedp.Text(`[data-testid="notfound-requests"]`, &notFoundReq, chromedp.ByQuery),
		chromedp.Text(`[data-testid="probe-card"]`, &probeCard, chromedp.ByQuery),
		// The summary tile and the busiest-pages card must both be visits-only.
		chromedp.Text(`.card-sm .h1`, &totalReq, chromedp.ByQuery),
		chromedp.Text(`.row-cards`, &pagesCard, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("drive analytics page: %v", err)
	}

	for _, c := range []struct{ name, got, want string }{
		{"probe requests", probeReq, "5"},
		{"bot requests", botReq, "2"},
		{"notfound requests", notFoundReq, "1"},
		{"total requests (visits only)", totalReq, "2"},
	} {
		if strings.TrimSpace(c.got) != c.want {
			t.Errorf("%s = %q, want %q", c.name, strings.TrimSpace(c.got), c.want)
		}
	}

	// The scanner's paths belong in the probe card and nowhere else.
	if !strings.Contains(probeCard, "/wp-login.php") {
		t.Errorf("probe card doesn't list the probed paths:\n%s", probeCard)
	}
	if strings.Contains(pagesCard, "/wp-login.php") {
		t.Error("a probed path leaked into the visit-facing cards — the kind filter isn't applied")
	}

	watch.assertClean(t)
}
