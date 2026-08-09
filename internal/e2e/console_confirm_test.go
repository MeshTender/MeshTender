//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EConsoleConfirmBanner covers the console's two state banners, which are
// the only place confirmation surfaces: an unconfirmed repeater shows the "not
// confirmed yet" banner, and a confirmed repeater with no known location shows
// the location prompt with a "Fetch location" button. Both must render under the
// strict CSP with no violations (the console's inline nonce'd bootstrap plus
// console.js wiring the banner/button).
func TestE2EConsoleConfirmBanner(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "owner")

	// Case 1: brand-new (unconfirmed) repeater → confirm prompt, no location prompt.
	unconfirmed := srv.newRepeater(t, owner.ID, "Unconfirmed Rep")

	// Case 2: confirmed with admin access but no stored location → location prompt.
	located := srv.newRepeater(t, owner.ID, "Confirmed Rep")
	if err := srv.store.SetRepeaterConfirmed(srv.ctx, located.ID, owner.ID, true, 3); err != nil {
		t.Fatalf("confirm repeater: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	// --- unconfirmed: confirm-banner visible; location-banner present but hidden
	// (it's revealed live by console.js once the repeater confirms with admin) ---
	var confirmBanner, locBannerHidden bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/repeaters/"+unconfirmed.PublicID+"/console"),
		chromedp.WaitVisible(`[data-testid="allowed-commands"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-testid="confirm-banner"]')`, &confirmBanner),
		// Computed display must be none — guards the Bootstrap gotcha where .d-flex
		// (display:flex !important) would override the [hidden] attribute.
		chromedp.Evaluate(`(function () { var b = document.querySelector('[data-testid="location-banner"]'); return !!b && getComputedStyle(b).display === 'none'; })()`, &locBannerHidden),
	); err != nil {
		t.Fatalf("browser run (unconfirmed): %v", err)
	}
	if !confirmBanner {
		t.Error("unconfirmed repeater console is missing the confirm banner")
	}
	if !locBannerHidden {
		t.Error("unconfirmed repeater console should render the location banner hidden (revealed after confirm)")
	}

	// --- confirmed, no location: location-banner + fetch button, no confirm banner ---
	var confirmBannerB, fetchBtn bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(srv.appURL+"/repeaters/"+located.PublicID+"/console"),
		chromedp.WaitVisible(`[data-testid="location-banner"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-testid="confirm-banner"]')`, &confirmBannerB),
		chromedp.Evaluate(`!!document.querySelector('[data-testid="fetch-location"]')`, &fetchBtn),
	); err != nil {
		t.Fatalf("browser run (confirmed): %v", err)
	}
	if confirmBannerB {
		t.Error("confirmed repeater console should not show the confirm banner")
	}
	if !fetchBtn {
		t.Error("confirmed repeater without a location is missing the Fetch location button")
	}

	watch.assertClean(t)
}
