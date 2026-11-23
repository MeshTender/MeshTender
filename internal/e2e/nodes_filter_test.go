//go:build browser

package e2e

import (
	"context"
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2ENodesFilter drives the client-side filtering on the Nodes list in a
// real browser: search, and each Show choice (companions, repeaters, and
// repeaters shared with me, which are a subset of repeaters). The filter
// controls stay the same whichever is chosen, a link can open the list already
// filtered with ?show=, and listfilter.js runs clean under the strict CSP.
func TestE2ENodesFilter(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "nodesfilter")
	other, err := srv.store.CreateUser(srv.ctx, "nodesowner", "")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	if _, err := srv.store.CreateCompanion(srv.ctx, user.ID, "Pocket T1", strings.Repeat("ab", 32)); err != nil {
		t.Fatalf("create companion: %v", err)
	}
	srv.newRepeater(t, user.ID, "Alpha")
	srv.newRepeater(t, user.ID, "Delta")
	gamma := srv.newRepeater(t, other.ID, "Gamma")
	if _, err := srv.store.AddShare(srv.ctx, gamma.ID, user.ID); err != nil {
		t.Fatalf("share gamma to me: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const visible = `document.querySelectorAll('[data-filter-item]:not([hidden])').length`
	const controls = `document.querySelectorAll('[role="search"] input, [role="search"] select').length`
	show := func(v string) string {
		return `(function(){var s=document.getElementById('node-search');s.value='';` +
			`var k=document.getElementById('node-show');k.value='` + v + `';k.dispatchEvent(new Event('change'));})()`
	}
	type result struct{ rows, controls int }
	var all, searched, companions, repeaters, shared, back, linked result
	read := func(r *result) chromedp.Action {
		return chromedp.ActionFunc(func(ctx context.Context) error {
			if err := chromedp.Evaluate(visible, &r.rows).Do(ctx); err != nil {
				return err
			}
			return chromedp.Evaluate(controls, &r.controls).Do(ctx)
		})
	}
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open your nodes",
			chromedp.Navigate(srv.appURL+"/nodes"),
			chromedp.WaitVisible(`#node-show`, chromedp.ByID),
			read(&all)),
		step("search", chromedp.Evaluate(`(function(){var s=document.getElementById('node-search');s.value='alpha';s.dispatchEvent(new Event('input'));})()`, nil), read(&searched)),
		step("show companions", chromedp.Evaluate(show("companion"), nil), read(&companions)),
		step("show repeaters", chromedp.Evaluate(show("repeater"), nil), read(&repeaters)),
		step("show shared with me", chromedp.Evaluate(show("shared"), nil), read(&shared)),
		step("show all", chromedp.Evaluate(show(""), nil), read(&back)),
		step("open a link to shared with me",
			chromedp.Navigate(srv.appURL+"/nodes?show=shared"),
			chromedp.WaitVisible(`#node-show`, chromedp.ByID),
			read(&linked)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	watch.assertClean(t)

	for _, c := range []struct {
		name string
		got  result
		rows int
	}{
		{"all", all, 4}, {"search alpha", searched, 1}, {"companions", companions, 1},
		{"repeaters", repeaters, 3}, {"shared with me", shared, 1}, {"back to all", back, 4},
		{"?show=shared", linked, 1},
	} {
		if c.got.rows != c.rows {
			t.Errorf("%s: %d rows visible, want %d", c.name, c.got.rows, c.rows)
		}
		if c.got.controls != 2 {
			t.Errorf("%s: %d filter controls, want the same 2 (search and Show) every time", c.name, c.got.controls)
		}
	}
}
