//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EAlertIconsSitBesideTheBody checks an alert's icon renders in the left
// gutter beside its body rather than stacked above it. Tabler only positions an
// .alert-icon that is a direct child of the .alert; one wrapped in a <div> fell
// into normal flow above the title (see TestAlertIconsAreDirectChildren). The
// add-repeater consent step's admin-grant warning is the alert checked.
func TestE2EAlertIconsSitBesideTheBody(t *testing.T) {
	srv := newE2EServer(t)
	_, cookie := srv.login(t, "alerter")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	// For each alert with an icon, report whether the icon ends left of the body
	// and starts above the body's bottom — i.e. beside it, not on its own line.
	const layout = `Array.prototype.map.call(document.querySelectorAll('.alert'), function (a) {
		var icon = a.querySelector('.alert-icon');
		var body = icon && a.querySelector('.alert-title');
		if (!body) return null;
		var i = icon.getBoundingClientRect(), b = body.getBoundingClientRect();
		return i.right <= b.left && i.top < b.bottom;
	}).filter(function (v) { return v !== null; })`

	var beside []bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the consent step",
			chromedp.Navigate(srv.appURL+"/repeaters/add?step=consent&method=kiss"),
			chromedp.WaitVisible(`#ack`, chromedp.ByQuery),
			chromedp.Evaluate(layout, &beside)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if len(beside) == 0 {
		t.Fatal("found no icon alerts with a title")
	}
	for i, ok := range beside {
		if !ok {
			t.Errorf("icon alert %d: icon is not beside its title", i)
		}
	}

	watch.assertClean(t)
}
