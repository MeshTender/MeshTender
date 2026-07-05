//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2ERepeatersFilter drives the client-side filtering on the "My repeaters"
// page in a real browser: search, the access (owned / shared-with-me) and status
// (confirmed / unconfirmed) selects, and the "shared with others" flag each hide
// the right rows. It also asserts the page (and listfilter.js) runs clean under
// the strict CSP.
func TestE2ERepeatersFilter(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "filteruser")
	other, err := srv.store.CreateUser(srv.ctx, "otheruser", "")
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}

	// Alpha: owned, unconfirmed. Beta: owned, confirmed. Gamma: shared WITH me
	// (owned by someone else). Delta: owned and shared OUT to someone else.
	srv.newRepeater(t, user.ID, "Alpha")
	beta := srv.newRepeater(t, user.ID, "Beta")
	if err := srv.store.SetRepeaterConfirmed(srv.ctx, beta.ID, user.ID, true, 3); err != nil {
		t.Fatalf("confirm beta: %v", err)
	}
	gamma := srv.newRepeater(t, other.ID, "Gamma")
	if _, err := srv.store.AddShare(srv.ctx, gamma.ID, user.ID); err != nil {
		t.Fatalf("share gamma to me: %v", err)
	}
	delta := srv.newRepeater(t, user.ID, "Delta")
	if _, err := srv.store.AddShare(srv.ctx, delta.ID, other.ID); err != nil {
		t.Fatalf("share delta out: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const countVisible = `document.querySelectorAll('[data-filter-item]:not([hidden])').length`
	// step resets every control (no event), applies one mutation that fires the
	// filter, and returns how many rows remain visible.
	const reset = `document.getElementById('rep-search').value='';` +
		`document.getElementById('rep-access').value='';` +
		`document.getElementById('rep-status').value='';` +
		`var __cb=document.querySelector('[data-filter-flag]');if(__cb)__cb.checked=false;`
	step := func(mutate string) int {
		var n int
		expr := "(function(){" + reset + mutate + "return " + countVisible + ";})()"
		if err := chromedp.Run(bctx, chromedp.Evaluate(expr, &n)); err != nil {
			t.Fatalf("evaluate: %v", err)
		}
		return n
	}
	setSelect := func(id, val string) string {
		return "var s=document.getElementById('" + id + "');s.value='" + val + "';s.dispatchEvent(new Event('change'));"
	}

	var initial int
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/repeaters"),
		chromedp.WaitVisible(`#rep-search`, chromedp.ByID),
		chromedp.Evaluate(countVisible, &initial),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}

	checks := []struct {
		name   string
		mutate string
		want   int
	}{
		{"all visible initially", "", 4},
		{"search Alpha", "var s=document.getElementById('rep-search');s.value='alpha';s.dispatchEvent(new Event('input'));", 1},
		{"access owned", setSelect("rep-access", "owned"), 3},
		{"access shared with me", setSelect("rep-access", "shared"), 1},
		{"status confirmed", setSelect("rep-status", "confirmed"), 1},
		{"status unconfirmed", setSelect("rep-status", "unconfirmed"), 3},
		{"shared with others", "var c=document.querySelector('[data-filter-flag]');c.checked=true;c.dispatchEvent(new Event('change'));", 1},
	}
	if initial != 4 {
		t.Fatalf("initial visible = %d, want 4", initial)
	}
	for _, c := range checks {
		if got := step(c.mutate); got != c.want {
			t.Fatalf("%s: %d rows visible, want %d", c.name, got, c.want)
		}
	}
	watch.assertClean(t)
}
