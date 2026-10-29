//go:build browser

package e2e

import (
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EOrgHomeLiveNetBanner: while a net is in session, the org's Home shows
// a banner whose "Open roster" goes to the session's roster, under the strict
// CSP.
func TestE2EOrgHomeLiveNetBanner(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ehomelive")
	org, err := srv.store.CreateOrg(srv.ctx, "Home Live Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	now := time.Now()
	start := now.Add(-10 * time.Minute).UTC()
	if _, err := srv.store.CreateNet(srv.ctx, org.ID, store.NetInput{Name: "Banner Net", Weekday: start.Weekday(),
		StartMinute: start.Hour()*60 + start.Minute(), DurationMinutes: 60, Timezone: "UTC", Active: true}, user.ID, now); err != nil {
		t.Fatalf("create net: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var banner string
	homeURL := srv.appURL + "/orgs/" + org.Slug
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the org's Home",
			chromedp.Navigate(homeURL),
			chromedp.WaitVisible(`[data-testid="home-live-net"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="home-live-net"]`, &banner, chromedp.ByQuery)),
		step("open the live net's roster",
			chromedp.Click(`[data-testid="home-live-roster"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="roster-card"]`, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("browser run against %s: %v", homeURL, err)
	}
	if !strings.Contains(banner, "Banner Net") || !strings.Contains(banner, "0 checked in") {
		t.Fatalf("banner = %q", banner)
	}
	watch.assertClean(t)
}
