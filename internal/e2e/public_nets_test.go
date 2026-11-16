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

// TestE2EPublicNetSchedule: a visitor sees an org's net schedule on its public
// page, and the directory row names the next net — both under the strict CSP.
func TestE2EPublicNetSchedule(t *testing.T) {
	srv := newE2EServer(t)
	user, _ := srv.login(t, "e2epubnets")
	org, err := srv.store.CreateOrg(srv.ctx, "Public Nets Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	now := time.Now()
	start := now.Add(2 * time.Hour).UTC()
	if _, err := srv.store.CreateNet(srv.ctx, org.ID, store.NetInput{Name: "Weekly Check-in", Weekday: start.Weekday(),
		StartMinute: start.Hour()*60 + start.Minute(), DurationMinutes: 60, Timezone: "UTC", Active: true}, user.ID, now); err != nil {
		t.Fatalf("create net: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var publicNets, dirNet string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		step("open the public org page",
			chromedp.Navigate(srv.rootURL+"/orgs/"+org.Slug),
			chromedp.WaitVisible(`[data-testid="org-nets"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="org-nets"]`, &publicNets, chromedp.ByQuery)),
		step("open the directory",
			chromedp.Navigate(srv.rootURL+"/orgs"),
			chromedp.WaitVisible(`[data-testid="org-row-net"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="org-row-net"]`, &dirNet, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	for what, text := range map[string]string{"public page": publicNets, "directory": dirNet} {
		if !strings.Contains(text, "Weekly Check-in") {
			t.Errorf("%s: net missing from %q", what, text)
		}
	}
	watch.assertClean(t)
}
