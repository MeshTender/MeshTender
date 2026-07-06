//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EAccountTimezonePicker verifies the account time-zone picker end to end:
// the <select> is populated from the browser's own IANA database
// (Intl.supportedValuesOf, surfaced as <optgroup>s), and choosing + saving a
// zone persists it to the user. Also asserts the page runs clean under the CSP —
// timezone-picker.js is self-hosted and must not trip any inline-script rule.
func TestE2EAccountTimezonePicker(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "tzpicker")

	// A fresh user is on auto-detect.
	if user.Timezone != "" {
		t.Fatalf("new user timezone = %q, want empty", user.Timezone)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	url := srv.authURL + "/account"
	var optgroupCount int
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(url),
		// buildPicker runs on DOMContentLoaded and fills the select from Intl; the
		// optgroups only exist once it has run. WaitReady (present in the DOM), not
		// WaitVisible — options inside a closed <select> have no layout box.
		chromedp.WaitReady(`#acct_timezone optgroup`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('#acct_timezone optgroup').length`, &optgroupCount),
		// Choose a specific zone and save.
		chromedp.SetValue(`#acct_timezone`, "America/New_York", chromedp.ByQuery),
		chromedp.Click(`[data-testid="tz-save"]`, chromedp.ByQuery),
		// The save redirects back to /account with a success flash.
		chromedp.WaitVisible(`.alert-success`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", url, err)
	}

	if optgroupCount < 5 {
		t.Fatalf("timezone picker had %d optgroups, expected the browser's full IANA list", optgroupCount)
	}

	got, err := srv.store.GetUserByID(srv.ctx, user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Timezone != "America/New_York" {
		t.Fatalf("saved timezone = %q, want America/New_York", got.Timezone)
	}

	watch.assertClean(t)
}
