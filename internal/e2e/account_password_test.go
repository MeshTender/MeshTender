//go:build browser

package e2e

import (
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestE2ERemovePasswordNeedsPasskeyCeremony is the accepting half of the rule
// TestRemovePasswordRequiresFreshPasskeyProof pins from the server side: no Go
// test can complete a WebAuthn assertion, so the only way to prove the button
// actually removes the password is to drive the real ceremony.
//
// It also exercises the account page's second re-auth button — the page now runs
// two passkey ceremonies (add a passkey, verify to remove a password), each
// writing to its own status line — under the strict CSP.
func TestE2ERemovePasswordNeedsPasskeyCeremony(t *testing.T) {
	e := newE2EServer(t)
	user, cookie := e.login(t, "e2eremovepw")
	e.setPassword(t, user.ID, "correct-horse-battery-staple")

	ctx, cancel, watch := startBrowser(t)
	defer cancel()
	if err := virtualAuthenticator(ctx); err != nil {
		t.Fatalf("set up virtual authenticator: %v", err)
	}
	acceptDialogs(ctx)

	// Register a passkey: without one, removal is refused before it ever reaches
	// the assertion check.
	if err := chromedp.Run(ctx,
		setSessionCookie(cookie),
		chromedp.Navigate(e.authURL+"/account"),
		expandSection("manage-passkeys", "#add-passkey-btn"),
		chromedp.Click("#add-passkey-btn", chromedp.ByID),
		chromedp.WaitVisible(`[data-testid="passkey-list-toggle"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("add a passkey: %v", err)
	}

	// Verify with the passkey; the button submits the remove form once the
	// assertion stamps the session.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(e.authURL+"/account"),
		expandSection("manage-password", "#new_pw"),
		chromedp.Click(`[data-testid="remove-password"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.alert-success`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("remove password: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		u, err := e.store.GetUserByID(e.ctx, user.ID)
		if err != nil {
			t.Fatalf("reload user: %v", err)
		}
		if u.PasswordHash == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("password was never removed after the passkey ceremony")
		}
		time.Sleep(200 * time.Millisecond)
	}

	watch.assertClean(t)
}
