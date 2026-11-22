//go:build browser

package e2e

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestAccountRecoveryCeremony drives an admin-issued recovery link end to end: a
// passkey-only account whose passkey stopped working opens the link, registers a
// new passkey through a virtual authenticator, and lands signed in on the account
// page. The Go tests cover everything up to the ceremony; only a browser can
// complete it.
func TestAccountRecoveryCeremony(t *testing.T) {
	e := newE2EServer(t)
	ctx, cancel, watch := startBrowser(t)
	defer cancel()
	if err := virtualAuthenticator(ctx); err != nil {
		t.Fatalf("set up virtual authenticator: %v", err)
	}

	// The account this exists for: no password, one passkey that no longer works,
	// and a login on some other device that recovery should sign out.
	u, err := e.store.CreateUser(e.ctx, "lockedout", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := e.store.AddCredential(e.ctx, u.ID, []byte("broken-passkey"), []byte(`{}`), "Old phone"); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	otherDevice, err := e.store.CreateLogin(e.ctx, u.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}
	const addr = "lockedout@example.test"
	if err := e.store.SetEmail(e.ctx, u.ID, addr); err != nil {
		t.Fatalf("set email: %v", err)
	}
	tok, err := e.store.ReplaceEmailToken(e.ctx, u.ID, store.PurposeRecoverAccount, addr, store.RecoverTokenTTL)
	if err != nil {
		t.Fatalf("mint recovery link: %v", err)
	}

	var body string
	if err := chromedp.Run(ctx,
		step("open the recovery link",
			chromedp.Navigate(e.authURL+"/recover/"+tok),
			chromedp.WaitVisible(`[data-testid="recover-username"]`)),
		step("create a new passkey",
			chromedp.Click("#recover-passkey-btn", chromedp.ByID),
			waitForLocation(e.authURL+"/account?pk=")),
		step("read the account page",
			chromedp.Text("body", &body, chromedp.ByQuery)),
	); err != nil {
		var status, loc string
		_ = chromedp.Run(ctx, step("debug location", chromedp.Location(&loc)))
		t.Logf("location: %s", loc)
		_ = chromedp.Run(ctx, step("read the passkey status",
			chromedp.Text("#passkey-status", &status, chromedp.ByID, chromedp.AtLeast(0))))
		t.Fatalf("recovery ceremony: %v (page status %q)", err, status)
	}

	if !strings.Contains(body, "signed in with your new passkey") {
		t.Errorf("account page doesn't confirm the recovery:\n%s", body)
	}
	creds, err := e.store.ListCredentials(e.ctx, u.ID)
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	if len(creds) != 2 {
		t.Fatalf("account has %d passkeys, want the old one plus the new one", len(creds))
	}
	got, err := e.store.GetUserByID(e.ctx, u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.PasswordHash != nil {
		t.Error("recovery set a password")
	}
	if !got.EmailVerified() {
		t.Error("the link came to this address, so using it should confirm the address")
	}
	if _, ok, err := e.store.PeekEmailToken(e.ctx, store.PurposeRecoverAccount, tok); err != nil || ok {
		t.Errorf("the link still works after being used (ok=%v err=%v)", ok, err)
	}
	if _, ok, err := e.store.LoginValid(e.ctx, otherDevice); err != nil || ok {
		t.Errorf("the other device is still signed in (ok=%v err=%v)", ok, err)
	}

	// The link is single-use: opening it again explains that instead of offering
	// another passkey.
	if err := chromedp.Run(ctx, step("reopen the used link",
		chromedp.Navigate(e.authURL+"/recover/"+tok),
		chromedp.WaitVisible(`[data-testid="recover-invalid"]`))); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	watch.assertClean(t)
}

// TestAdminIssuesRecoveryLink drives the admin side in a real browser: the Actions
// menu loads the recovery modal over htmx, submitting it mails the link and shows
// it once, and the mailed link opens the recovery page for the right account.
func TestAdminIssuesRecoveryLink(t *testing.T) {
	e := newE2EServer(t)
	// login()'s first account bootstraps the manage-users capability.
	_, cookie := e.login(t, "e2eadmin")
	target, err := e.store.CreateUser(e.ctx, "needshelp", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := e.store.AddCredential(e.ctx, target.ID, []byte("broken-passkey"), []byte(`{}`), "Old phone"); err != nil {
		t.Fatalf("add credential: %v", err)
	}

	ctx, cancel, watch := startBrowser(t)
	defer cancel()

	const addr = "needshelp@example.test"
	var shown string
	if err := chromedp.Run(ctx,
		setSessionCookie(cookie),
		step("find the account",
			chromedp.Navigate(e.appURL+"/admin/users?q=needshelp"),
			chromedp.WaitVisible(`[data-testid="user-row"]`, chromedp.ByQuery)),
		step("open its actions",
			chromedp.Click(`[data-testid="user-actions"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="user-recovery"]`, chromedp.ByQuery)),
		step("open account recovery",
			chromedp.Click(`[data-testid="user-recovery"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`#user-modal-content [data-testid="recovery-email"]`, chromedp.ByQuery)),
		step("send a recovery link",
			chromedp.SendKeys(`[data-testid="recovery-email"]`, addr, chromedp.ByQuery),
			chromedp.Click(`[data-testid="recovery-submit"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="recovery-mailed"]`, chromedp.ByQuery),
			chromedp.Value(`[data-testid="recovery-link"]`, &shown, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("issue recovery link: %v", err)
	}

	mailed := e.mail.lastLink(t)
	if !strings.HasPrefix(shown, e.authURL+"/recover/") || !strings.HasSuffix(shown, mailed) {
		t.Fatalf("page shows %q, mail has %q; want the same auth-host link", shown, mailed)
	}

	var who string
	if err := chromedp.Run(ctx, step("open the mailed link",
		chromedp.Navigate(e.authURL+mailed),
		chromedp.Text(`[data-testid="recover-username"]`, &who, chromedp.ByQuery))); err != nil {
		t.Fatalf("open link: %v", err)
	}
	if who != "@needshelp" {
		t.Errorf("recovery page names %q, want @needshelp", who)
	}

	watch.assertClean(t)
}
