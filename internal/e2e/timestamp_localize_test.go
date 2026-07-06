//go:build browser

package e2e

import (
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2ETimestampLocalizesToUserZone is the payoff for the whole feature: a
// server-emitted <time> element is rewritten by ui.js into the viewer's saved
// zone. The repeater's created_at is pinned to 2026-07-05T02:00:00Z; with the
// user's zone set to America/New_York (UTC-4 in July) that instant falls on
// July 4 locally — so a correct localization shows "Jul 4", not "Jul 5" (the UTC
// day the server fallback would show).
func TestE2ETimestampLocalizesToUserZone(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "tzlocal")
	rep := srv.newRepeater(t, user.ID, "Clock Repeater")

	pinned := time.Date(2026, 7, 5, 2, 0, 0, 0, time.UTC)
	if _, err := srv.store.Pool().Exec(srv.ctx,
		`UPDATE repeaters SET created_at = $1 WHERE id = $2`, pinned, rep.ID); err != nil {
		t.Fatalf("pin created_at: %v", err)
	}
	if err := srv.store.SetTimezone(srv.ctx, user.ID, "America/New_York"); err != nil {
		t.Fatalf("set timezone: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	// Read the localized element and, in the same browser, the value Intl should
	// produce for that instant in the user's zone — so the assertion is exact and
	// locale-independent (both use the browser's own locale).
	var res struct {
		Datetime string `json:"datetime"`
		Text     string `json:"text"`
		Expected string `json:"expected"`
		Tz       string `json:"tz"`
	}
	url := srv.appURL + "/repeaters/" + rep.PublicID
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(url),
		chromedp.WaitReady(`time[data-fmt="date"]`, chromedp.ByQuery),
		chromedp.Evaluate(`(function () {
			var el = document.querySelector('time[data-fmt="date"]');
			var dt = el.getAttribute('datetime');
			var expected = new Intl.DateTimeFormat(undefined, {
				dateStyle: 'medium', timeZone: document.documentElement.dataset.tz
			}).format(new Date(dt));
			return {
				datetime: dt,
				text: el.textContent,
				expected: expected,
				tz: document.documentElement.dataset.tz
			};
		})()`, &res),
	); err != nil {
		t.Fatalf("browser run against %s: %v", url, err)
	}

	if res.Tz != "America/New_York" {
		t.Fatalf("data-tz = %q, want America/New_York (UserTZ not threaded?)", res.Tz)
	}
	if res.Datetime != "2026-07-05T02:00:00Z" {
		t.Fatalf("datetime attr = %q, want the pinned UTC instant", res.Datetime)
	}
	if res.Text != res.Expected {
		t.Fatalf("rendered %q, but Intl for the zone gives %q (ui.js didn't localize correctly)", res.Text, res.Expected)
	}
	if !strings.Contains(res.Text, "Jul 4") {
		t.Fatalf("rendered %q, expected a Jul 4 local date (02:00Z → prior day in New York); zone not applied?", res.Text)
	}
	watch.assertClean(t)
}
