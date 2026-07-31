//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// Browser coverage for the CSP form-action directive.
//
// Every form in this app POSTs to one surface and may redirect to another (sign-in and
// sign-up hand off to the app host; sign-out lands on the root host). Chrome enforces
// form-action across that redirect, so a policy of `form-action 'self'` makes the browser
// drop the redirect and leave the page sitting where it was. The POST is delivered and
// the handler succeeds, so nothing server-side looks wrong — only the navigation is
// lost. These tests are the only thing that can see that: handler tests enforce no CSP,
// and a fetch() is governed by connect-src instead.

// TestE2EPasswordFormsSurviveCSP guards the CSP regression directly, because the
// round-trip above would only fail at its last step and the cause would read as a
// recovery bug rather than what it is.
//
// Both credential forms POST to the auth host and redirect to the app host, and Chrome
// checks form-action against that redirect. Under `form-action 'self'` the browser
// dropped the redirect from both, leaving the visitor on the form with no indication
// anything had happened — while the account was in fact created and the server logged a
// 303. That gap is why every server-side test passed for a month while the default
// sign-up and sign-in paths were broken in the majority browser.
//
// Deliberately driven through the rendered forms rather than posting directly: the
// block happens in the browser, so only a browser can catch it.
func TestE2EPasswordFormsSurviveCSP(t *testing.T) {
	srv := newE2EServer(t)

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const (
		username = "csp-form-user"
		password = "a-perfectly-fine-password"
	)

	// Sign up with a password: auth host → handoff → app host.
	var afterSignup string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(srv.authURL+"/signup"),
		chromedp.WaitVisible(`#password`, chromedp.ByQuery),
		chromedp.SendKeys(`#username`, username, chromedp.ByQuery),
		chromedp.SendKeys(`#password`, password, chromedp.ByQuery),
		chromedp.Submit(`#password`, chromedp.ByQuery),
		waitForLocation(srv.appURL),
		chromedp.Location(&afterSignup),
	); err != nil {
		t.Fatalf("password sign-up in the browser: %v", err)
	}
	if !strings.HasPrefix(afterSignup, srv.appURL) {
		t.Errorf("after sign-up the browser is at %q, want the app host", afterSignup)
	}

	// And sign in again from a clean session.
	var afterLogin string
	if err := chromedp.Run(bctx,
		network.ClearBrowserCookies(),
		chromedp.Navigate(srv.authURL+"/login"),
		chromedp.WaitVisible(`#password`, chromedp.ByQuery),
		chromedp.SendKeys(`#username`, username, chromedp.ByQuery),
		chromedp.SendKeys(`#password`, password, chromedp.ByQuery),
		chromedp.Submit(`#password`, chromedp.ByQuery),
		waitForLocation(srv.appURL),
		chromedp.Location(&afterLogin),
	); err != nil {
		t.Fatalf("password sign-in in the browser: %v", err)
	}
	if !strings.HasPrefix(afterLogin, srv.appURL) {
		t.Errorf("after sign-in the browser is at %q, want the app host", afterLogin)
	}

	// assertClean only looks for CSP violations, which is exactly the failure mode:
	// a form-action block reports here and nowhere else.
	watch.assertClean(t)
}

// TestE2ELogoutFormNavigatesCrossHost completes the form-action coverage. Sign-out is
// the third flow the old policy broke: POST /logout answers 303 to the root host, so
// Chrome blocked it exactly like sign-in.
//
// The existing logout test drives the same endpoint with fetch() and reads the form's
// action attribute — deliberately, to stay independent of the cross-host redirect. But
// fetch() is governed by connect-src, not form-action, so it cannot see this class of
// bug. This test submits the real form and follows the navigation.
func TestE2ELogoutFormNavigatesCrossHost(t *testing.T) {
	srv := newE2EServer(t)
	_, cookie := srv.login(t, "logout-form-user")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var landed string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/repeaters"),
		// The form lives in the user-menu dropdown: in the DOM but not laid out until
		// the menu opens, so wait for readiness rather than visibility.
		chromedp.WaitReady(`[data-testid="logout-form"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-testid="logout-form"]').submit()`, nil),
		// Sign-out lands on the public root host.
		waitForLocation(srv.rootURL),
		chromedp.Location(&landed),
	); err != nil {
		t.Fatalf("submit the sign-out form: %v", err)
	}
	if !strings.HasPrefix(landed, srv.rootURL) {
		t.Errorf("after sign-out the browser is at %q, want the root host %q", landed, srv.rootURL)
	}
	watch.assertClean(t)
}
