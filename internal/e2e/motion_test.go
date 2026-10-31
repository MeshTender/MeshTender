//go:build browser

package e2e

import (
	"testing"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// TestE2EPageIsStillOnceLoaded is the regression for the flaky Settings click in
// TestE2ECompanionRegistrationAndOrgPages. Opening a URL with a fragment (the
// old /activity link redirects to /nets#activity) scrolls the page to the
// anchor, and with the page's smooth scrolling that takes ~200ms after the
// test's wait has returned. chromedp clicks by measuring where an element is
// and then pressing the mouse there, so a page still moving between the two
// clicks whatever slid under the pointer — in CI, empty space above the tabs.
//
// The harness turns motion off (see startBrowser), so once a page has loaded
// nothing moves. This checks that, rather than the click, because the click
// only misses when the timing is unlucky; the motion is there every time.
func TestE2EPageIsStillOnceLoaded(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2estill")
	org, err := srv.store.CreateOrg(srv.ctx, "Still Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	// Where the page is scrolled to now, and over the next ten frames.
	var scrolls []float64
	if err := chromedp.Run(bctx,
		setSessionCookie(cookie),
		step("open the old activity link",
			chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/activity"),
			chromedp.WaitVisible(`[data-testid="checkins-activity"]`, chromedp.ByQuery)),
		step("watch the scroll position",
			chromedp.Evaluate(`new Promise(function (resolve) {
				var ys = [];
				(function frame() {
					ys.push(window.scrollY);
					if (ys.length < 10) requestAnimationFrame(frame); else resolve(ys);
				})();
			})`, &scrolls, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
				return p.WithAwaitPromise(true)
			})),
		step("click Settings",
			chromedp.Click(`[data-testid="org-tab-settings"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="org-settings-nav"]`, chromedp.ByQuery)),
	); err != nil {
		t.Fatal(err)
	}
	for _, y := range scrolls {
		if y != scrolls[0] {
			t.Fatalf("the page was still scrolling after it loaded (scrollY over ten frames: %v) — "+
				"a click measured during that lands on whatever moved under it", scrolls)
		}
	}
	watch.assertClean(t)
}
