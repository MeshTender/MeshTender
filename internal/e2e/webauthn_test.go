//go:build browser

package e2e

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chromedp/cdproto/security"
	"github.com/chromedp/cdproto/webauthn"
	"github.com/chromedp/chromedp"

	"github.com/jleight/meshtender/internal/store"
)

// virtualAuthenticator enables the WebAuthn CDP domain and installs a virtual
// authenticator that auto-approves user presence/verification, so
// navigator.credentials.create/get succeed headlessly. It also tells the browser
// to accept the harness's self-signed cert (WebAuthn needs a secure context, and
// an HTTPS origin — even with an untrusted cert — qualifies, whereas plain HTTP
// on a non-localhost host does not).
func virtualAuthenticator(ctx context.Context) error {
	return chromedp.Run(ctx,
		security.SetIgnoreCertificateErrors(true),
		webauthn.Enable(),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := webauthn.AddVirtualAuthenticator(&webauthn.VirtualAuthenticatorOptions{
				Protocol:                    webauthn.AuthenticatorProtocolCtap2,
				Transport:                   webauthn.AuthenticatorTransportInternal,
				HasResidentKey:              true,
				HasUserVerification:         true,
				IsUserVerified:              true,
				AutomaticPresenceSimulation: true,
			}).Do(ctx)
			return err
		}),
	)
}

// waitForUser polls until the account exists (the ceremony's server side runs
// asynchronously after the click) or the deadline passes.
func waitForUser(t *testing.T, e *e2eServer, username string) *store.User {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		u, err := e.store.GetUserByUsername(e.ctx, username)
		if err == nil {
			return u
		}
		if !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetUserByUsername: %v", err)
		}
		if time.Now().After(deadline) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestPasskeySignupCeremony drives the real passkey signup end to end through a
// virtual authenticator: the browser runs navigator.credentials.create on the
// live signup page, and only when the credential is verified at finish does the
// account get written (the deferred-creation flow). This is the browser-level
// proof of item 3 that the Go tests can't give (they can't complete a ceremony).
func TestPasskeySignupCeremony(t *testing.T) {
	e := newE2ETLSServer(t, authReachableHosts())
	ctx, cancel, watch := startBrowser(t)
	defer cancel()

	if err := virtualAuthenticator(ctx); err != nil {
		t.Fatalf("set up virtual authenticator: %v", err)
	}

	const username = "passkeysignup"
	// Pre-condition: no such account yet.
	if _, err := e.store.GetUserByUsername(e.ctx, username); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("account should not exist before the ceremony (err=%v)", err)
	}

	if err := chromedp.Run(ctx,
		chromedp.Navigate(e.browserURL+"/signup"),
		chromedp.WaitVisible("#signup-passkey-btn", chromedp.ByID),
		chromedp.SendKeys("#username", username, chromedp.ByID),
		chromedp.Click("#signup-passkey-btn", chromedp.ByID),
	); err != nil {
		t.Fatalf("drive signup page: %v", err)
	}

	u := waitForUser(t, e, username)
	if u == nil {
		// Surface the on-page status for diagnostics.
		var status string
		_ = chromedp.Run(ctx, chromedp.Text("#passkey-status", &status, chromedp.ByID, chromedp.AtLeast(0)))
		t.Fatalf("account was not created by the passkey ceremony; page status = %q", status)
	}

	// A verified credential must have been persisted for the new account.
	creds, err := e.store.GetCredentials(e.ctx, u.ID)
	if err != nil {
		t.Fatalf("GetCredentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("new account has %d credentials, want exactly 1", len(creds))
	}

	watch.assertClean(t) // the signup page + ceremony must not trip the CSP
}
