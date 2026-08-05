//go:build browser

package e2e

import (
	"errors"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/jleight/meshtender/internal/store"
)

// waitForUserGone polls until the account is gone, so the assertion doesn't race
// the server side of the delete request.
func waitForUserGone(t *testing.T, e *e2eServer, id int64) bool {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		_, err := e.store.GetUserByID(e.ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

// TestE2EDeleteAccountWithPasskey is the passkey-only deletion path, which no Go
// test can reach: the account has no password, so the ONLY way to prove presence
// is a real assertion. The button runs the ceremony against a virtual
// authenticator and, once the server stamps the session, submits the form.
//
// It also proves the confirm page runs clean under the strict CSP — this page
// carries a password toggle, a confirm gate and a WebAuthn ceremony, all of
// which are exactly the things a CSP breaks silently.
func TestE2EDeleteAccountWithPasskey(t *testing.T) {
	e := newE2EServer(t)
	// The first account is auto-promoted to superadmin and could never delete
	// itself (it'd be the last administrator), so park one before the subject.
	e.login(t, "e2edeleteadmin")
	victim, cookie := e.login(t, "e2edeleteme")

	ctx, cancel, watch := startBrowser(t)
	defer cancel()
	if err := virtualAuthenticator(ctx); err != nil {
		t.Fatalf("set up virtual authenticator: %v", err)
	}
	acceptDialogs(ctx)

	// Register a passkey on the account page, so the account has a passkey and no
	// password — the state that forces the re-auth ceremony.
	if err := chromedp.Run(ctx,
		setSessionCookie(cookie),
		chromedp.Navigate(e.authURL+"/account"),
		// The add-a-passkey form lives behind the Passkeys row's toggle.
		expandSection("manage-passkeys", "#add-passkey-btn"),
		chromedp.Click("#add-passkey-btn", chromedp.ByID),
	); err != nil {
		t.Fatalf("add a passkey: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		creds, err := e.store.GetCredentials(e.ctx, victim.ID)
		if err != nil {
			t.Fatalf("GetCredentials: %v", err)
		}
		if len(creds) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("passkey registration never landed")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Now delete the account: verify with the passkey, which submits the form.
	if err := chromedp.Run(ctx,
		chromedp.Navigate(e.authURL+"/account/delete"),
		chromedp.WaitVisible(`[data-testid="verify-passkey"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="verify-passkey"]`, chromedp.ByQuery),
		waitForLocation(e.authURL+"/login"),
	); err != nil {
		var status string
		_ = chromedp.Run(ctx, chromedp.Text("#passkey-status", &status, chromedp.ByID, chromedp.AtLeast(0)))
		t.Fatalf("drive the delete page: %v (page status = %q)", err, status)
	}

	if !waitForUserGone(t, e, victim.ID) {
		t.Fatal("the account still exists after a verified deletion")
	}
	watch.assertClean(t)
}

// TestE2EDeleteAccountWithoutVerifying: clicking delete without proving presence
// must not destroy the account. This is the whole point of the re-auth gate —
// a live session on an unattended browser isn't enough.
func TestE2EDeleteAccountWithoutVerifying(t *testing.T) {
	e := newE2EServer(t)
	e.login(t, "e2ekeepadmin")
	victim, cookie := e.login(t, "e2ekeepme")
	e.setPassword(t, victim.ID, "correct-horse-battery")

	ctx, cancel, watch := startBrowser(t)
	defer cancel()
	acceptDialogs(ctx)

	var errText string
	if err := chromedp.Run(ctx,
		setSessionCookie(cookie),
		chromedp.Navigate(e.authURL+"/account/delete"),
		// Submit with the password field left empty.
		chromedp.Click(`[data-testid="confirm-delete"]`, chromedp.ByQuery),
		waitForLocation(e.authURL+"/account/delete?error="),
		chromedp.Text(".alert-danger", &errText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("drive the delete page: %v", err)
	}
	if errText == "" {
		t.Fatal("no error shown after submitting without a password")
	}
	if _, err := e.store.GetUserByID(e.ctx, victim.ID); err != nil {
		t.Fatalf("account was deleted without any proof of presence: %v", err)
	}
	watch.assertClean(t)
}
