//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// TestE2EPasswordRecoveryRoundTrip drives the entire recovery story in a real
// browser, the way a locked-out user lives it: add an address, confirm it from the
// link, lose the password, ask for a reset from the sign-in page, follow that link,
// choose a new password, and sign in with it.
//
// The links come out of the captured mail, so nothing here reaches into the store for
// a token — the browser follows exactly what a recipient would click. Cookies are
// cleared before the reset half so the visitor really is signed out, which is the only
// state in which someone reaches for "Forgot password?".
//
// It also proves the four new pages run clean under the strict CSP, which no Go test
// can check.
func TestE2EPasswordRecoveryRoundTrip(t *testing.T) {
	srv := newE2EServer(t)

	const (
		username    = "recovery-user"
		address     = "recovery@example.test"
		oldPassword = "the-original-password"
		newPassword = "a-completely-new-password"
	)
	// The account starts signed in with a password. Sign-up itself is covered by the
	// signup-emphasis tests; seeding here keeps this test about recovery.
	user, cookie := srv.login(t, username)
	srv.setPassword(t, user.ID, oldPassword)

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	// --- add an address, then confirm it from the emailed link ---------------
	var confirmedState string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		page.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.authURL+"/account"),
		chromedp.WaitVisible(`#email`, chromedp.ByQuery),
		chromedp.SendKeys(`#email`, address, chromedp.ByQuery),
		chromedp.Click(`[data-testid="email-save"]`, chromedp.ByQuery),
		// The saved-address row is the state we need, and unlike a flash alert it can't
		// be left over from an earlier action.
		chromedp.WaitVisible(`[data-testid="email-remove"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("add address: %v", err)
	}

	// The confirmation link, read from the message we would have sent.
	verifyPath := srv.mail.lastLink(t)
	if !strings.HasPrefix(verifyPath, "/verify-email/") {
		t.Fatalf("first message linked to %q, want a /verify-email/ link", verifyPath)
	}
	if err := chromedp.Run(bctx,
		chromedp.Navigate(srv.authURL+verifyPath),
		// The confirmation redirects to /account (this browser is signed in); wait for
		// it to land before navigating again, or the next Navigate races the redirect
		// and fails with ERR_ABORTED.
		waitForLocation(srv.authURL+"/account"),
		chromedp.Navigate(srv.authURL+"/account"),
		chromedp.WaitVisible(`[data-testid="email-remove"]`, chromedp.ByQuery),
		chromedp.Text(`.badge`, &confirmedState, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("confirm address: %v", err)
	}
	if !strings.Contains(strings.ToLower(confirmedState), "confirmed") {
		t.Fatalf("address state after following the link = %q, want confirmed", confirmedState)
	}

	// --- locked out: clear the session and reset from the sign-in page --------
	var sentText, resetHeading, successText string
	if err := chromedp.Run(bctx,
		// A locked-out visitor has no session; anything else would skip /login entirely.
		network.ClearBrowserCookies(),
		chromedp.Navigate(srv.authURL+"/login"),
		chromedp.WaitVisible(`[data-testid="forgot-link"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="forgot-link"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#identifier`, chromedp.ByQuery),
		chromedp.SendKeys(`#identifier`, address, chromedp.ByQuery),
		chromedp.Click(`[data-testid="forgot-submit"]`, chromedp.ByQuery),
		// Wait on the URL, not on .card-title: the form page has a card title too, so a
		// selector wait returns instantly and reads the pre-submit heading.
		waitForLocation(srv.authURL+"/forgot?sent=1"),
		chromedp.Text(`.card-title`, &sentText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	if !strings.Contains(sentText, "Check your email") {
		t.Fatalf("after submitting the reset form the page said %q", sentText)
	}

	resetPath := srv.mail.lastLink(t)
	if !strings.HasPrefix(resetPath, "/reset/") {
		t.Fatalf("second message linked to %q, want a /reset/ link", resetPath)
	}

	if err := chromedp.Run(bctx,
		chromedp.Navigate(srv.authURL+resetPath),
		chromedp.WaitVisible(`#new_pw`, chromedp.ByQuery),
		// The form must name the account: one address can hold several, each with its
		// own link in the same message.
		chromedp.Text(`.card-body`, &resetHeading, chromedp.ByQuery),
		chromedp.SendKeys(`#new_pw`, newPassword, chromedp.ByQuery),
		chromedp.Click(`[data-testid="reset-submit"]`, chromedp.ByQuery),
		// Lands back on sign-in with the success flash.
		chromedp.WaitVisible(`.alert-success`, chromedp.ByQuery),
		chromedp.Text(`.alert-success`, &successText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("complete reset: %v", err)
	}
	if !strings.Contains(resetHeading, "@"+username) {
		t.Errorf("reset form didn't name the account (@%s):\n%s", username, resetHeading)
	}
	if !strings.Contains(successText, "Password updated") {
		t.Errorf("success flash = %q", successText)
	}

	// --- the new password actually signs in ----------------------------------
	var dashURL string
	if err := chromedp.Run(bctx,
		chromedp.WaitVisible(`#password`, chromedp.ByQuery),
		chromedp.SendKeys(`#username`, username, chromedp.ByQuery),
		chromedp.SendKeys(`#password`, newPassword, chromedp.ByQuery),
		chromedp.Submit(`#password`, chromedp.ByQuery),
		// Sign-in redirects auth host → handoff → app host.
		waitForLocation(srv.appURL),
		chromedp.Location(&dashURL),
	); err != nil {
		t.Fatalf("sign in with the new password: %v", err)
	}
	if !strings.HasPrefix(dashURL, srv.appURL) {
		t.Errorf("after signing in the browser is at %q, want the app host %q", dashURL, srv.appURL)
	}

	// The old password must be dead. Checked through the real form rather than the
	// store, since that's what an attacker holding it would use.
	var loginError string
	if err := chromedp.Run(bctx,
		network.ClearBrowserCookies(),
		chromedp.Navigate(srv.authURL+"/login"),
		chromedp.WaitVisible(`#password`, chromedp.ByQuery),
		chromedp.SendKeys(`#username`, username, chromedp.ByQuery),
		chromedp.SendKeys(`#password`, oldPassword, chromedp.ByQuery),
		chromedp.Submit(`#password`, chromedp.ByQuery),
		chromedp.WaitVisible(`.alert-danger`, chromedp.ByQuery),
		chromedp.Text(`.alert-danger`, &loginError, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("old password check: %v", err)
	}
	if !strings.Contains(loginError, "Invalid") {
		t.Errorf("signing in with the old password gave %q, want it rejected", loginError)
	}

	watch.assertClean(t)
}

// TestE2ERemoveEmailConfirmDialog covers the destructive control the HTTP tests can't:
// removal is gated behind ui.js's window.confirm, so the markup being right proves
// nothing on its own — an unanswered dialog silently blocks the whole page.
func TestE2ERemoveEmailConfirmDialog(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "remove-email-user")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	acceptDialogs(bctx)

	const address = "removeme@example.test"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		page.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.authURL+"/account"),
		chromedp.WaitVisible(`#email`, chromedp.ByQuery),
		chromedp.SendKeys(`#email`, address, chromedp.ByQuery),
		chromedp.Click(`[data-testid="email-save"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="email-remove"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("add address: %v", err)
	}

	stored2, err := srv.store.GetUserByID(srv.ctx, user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if stored2.Email == nil {
		t.Fatal("precondition: address wasn't saved")
	}

	// Click Remove and accept the confirm dialog.
	if err := chromedp.Run(bctx,
		chromedp.Click(`[data-testid="email-remove"]`, chromedp.ByQuery),
		// The address row disappearing is the only unambiguous signal: a success flash
		// from the save is still on the page, so waiting on one would pass even if the
		// confirm dialog had blocked the click entirely — which is the exact failure
		// this test exists to catch.
		chromedp.WaitNotPresent(`[data-testid="email-remove"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("remove address: %v", err)
	}

	after, err := srv.store.GetUserByID(srv.ctx, user.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.Email != nil {
		t.Errorf("Email = %v after confirming removal, want nil", after.Email)
	}
	watch.assertClean(t)
}
