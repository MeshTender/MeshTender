//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// TestE2ELogout proves sign-out works in a real browser: the signed-in page
// chrome carries a POST /logout form, submitting it clears the session, and a
// protected page then bounces the (now anonymous) browser to sign-in. Driving the
// endpoint with same-origin fetch (rather than clicking through) keeps the test
// independent of where the single-host harness lands after logout, while still
// exercising real browser cookies and the strict CSP (connect-src 'self').
func TestE2ELogout(t *testing.T) {
	srv := newE2EServer(t)
	_, cookie := srv.login(t, "logoutuser")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	dash := srv.browserURL + "/"

	// Log out, then probe a protected page — both with redirect:'manual' so a
	// sign-in bounce surfaces as an opaqueredirect instead of being followed.
	const logoutAndProbe = `(async () => {
		const lo = await fetch('/logout', { method: 'POST', redirect: 'manual' });
		const prot = await fetch('/repeaters', { redirect: 'manual' });
		return { logoutType: lo.type, protType: prot.type, protStatus: prot.status };
	})()`
	awaitPromise := func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}

	var formAction string
	var result struct {
		LogoutType string `json:"logoutType"`
		ProtType   string `json:"protType"`
		ProtStatus int    `json:"protStatus"`
	}
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(dash),
		// The sign-out form lives in the user-menu dropdown (present in the DOM even
		// while the dropdown is collapsed).
		chromedp.WaitReady(`[data-testid="logout-form"]`, chromedp.ByQuery),
		chromedp.AttributeValue(`[data-testid="logout-form"]`, "action", &formAction, nil),
		chromedp.Evaluate(logoutAndProbe, &result, awaitPromise),
	); err != nil {
		t.Fatalf("browser run against %s: %v", dash, err)
	}

	if !strings.HasSuffix(formAction, "/logout") {
		t.Fatalf("sign-out form action = %q, want it to POST to /logout", formAction)
	}
	// The POST was accepted and redirected (not 405/blocked): a manual-redirect
	// fetch reports an opaqueredirect for the 303.
	if result.LogoutType != "opaqueredirect" {
		t.Fatalf("POST /logout fetch type = %q, want opaqueredirect (accepted + redirected)", result.LogoutType)
	}
	// After logout the protected page no longer returns 200 — the browser is
	// bounced to sign-in (another opaqueredirect), proving the session was cleared.
	if result.ProtType != "opaqueredirect" {
		t.Fatalf("post-logout /repeaters type = %q (status %d), want a sign-in redirect", result.ProtType, result.ProtStatus)
	}
	watch.assertClean(t)
}
