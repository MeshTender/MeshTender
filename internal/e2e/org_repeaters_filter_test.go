//go:build browser

package e2e

import (
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EOrgRepeatersSearchSort drives an org's repeater list in a real
// browser: search hides rows (a row's flex layout mustn't beat [hidden]), and
// sorting by last heard puts the most recently heard first and the never-heard
// last. Runs clean under the strict CSP.
func TestE2EOrgRepeatersSearchSort(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "orgrepsort")
	org, err := srv.store.CreateOrg(srv.ctx, "Sort Org", user.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Alpha heard an hour ago, Bravo just now, Charlie never.
	for name, ago := range map[string]time.Duration{"Alpha": time.Hour, "Bravo": time.Minute, "Charlie": 0} {
		rep := srv.newRepeater(t, user.ID, name)
		if ago == 0 {
			continue
		}
		at := time.Now().Add(-ago)
		if _, err := srv.store.RecordRepeaterAdvert(srv.ctx, store.RepeaterAdvert{PublicKeyHex: rep.PublicKeyHex,
			AdvertTS: uint32(at.Unix()), ReceivedAt: at, Name: name}); err != nil { //nolint:gosec // test clock
			t.Fatal(err)
		}
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const order = `Array.from(document.querySelectorAll('[data-filter-item]')).map(function(e){return e.querySelector('.fw-bold').textContent}).join(',')`
	const visible = `Array.from(document.querySelectorAll('[data-filter-item]')).filter(function(e){return e.offsetParent!==null}).length`
	var byName, byHeard string
	var shown int
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/repeaters"),
		chromedp.WaitVisible(`#org-rep-search`, chromedp.ByID),
		chromedp.Evaluate(order, &byName),
		chromedp.SetValue(`[data-testid="org-rep-sort"]`, "heard:desc", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-testid="org-rep-sort"]').dispatchEvent(new Event('change'))`, nil),
		chromedp.Evaluate(order, &byHeard),
		chromedp.SendKeys(`#org-rep-search`, "brav", chromedp.ByID),
		// Measured by layout, not the attribute: a hidden row must actually vanish.
		chromedp.Evaluate(visible, &shown),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if byName != "Alpha,Bravo,Charlie" {
		t.Errorf("default order = %s, want by name", byName)
	}
	if byHeard != "Bravo,Alpha,Charlie" {
		t.Errorf("by last heard = %s, want Bravo,Alpha,Charlie", byHeard)
	}
	if shown != 1 {
		t.Errorf("searching %q shows %d rows, want 1", "brav", shown)
	}
	watch.assertClean(t)
}
