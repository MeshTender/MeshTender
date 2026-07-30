//go:build browser

package e2e

import (
	"encoding/json"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// signupState reports what the sign-up form looks like after webauthn.js has adapted
// it: whether each half is visible, and whether the password input is disabled.
type signupState struct {
	PasskeyVisible   bool
	PasswordVisible  bool
	TogglePresent    bool
	PasswordDisabled bool
	NoticeVisible    bool
}

// readSignupState drives the sign-up page and reads the resulting emphasis. withAuth
// controls whether a virtual platform authenticator is installed first, which is what
// isUserVerifyingPlatformAuthenticatorAvailable() reports on.
func readSignupState(t *testing.T, srv *e2eServer, withAuth bool) signupState {
	t.Helper()
	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	if withAuth {
		if err := virtualAuthenticator(bctx); err != nil {
			t.Fatalf("virtual authenticator: %v", err)
		}
	}

	// One expression so the whole state is sampled at the same instant.
	const probe = `(() => {
	  const vis = (id) => {
	    const el = document.getElementById(id);
	    return !!el && !el.classList.contains("d-none");
	  };
	  const pw = document.getElementById("password");
	  return JSON.stringify({
	    PasskeyVisible: vis("passkey-section"),
	    PasswordVisible: vis("password-section"),
	    TogglePresent: vis("use-password-wrap"),
	    PasswordDisabled: !!pw && pw.disabled,
	    NoticeVisible: vis("passkey-unavailable"),
	  });
	})()`

	var raw string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(srv.authURL+"/signup"),
		chromedp.WaitVisible(`#signup-form`, chromedp.ByQuery),
		// The adaptation awaits a promise, so give the microtask queue a beat.
		chromedp.Sleep(700*time.Millisecond),
		chromedp.Evaluate(probe, &raw),
	); err != nil {
		t.Fatalf("read signup state: %v", err)
	}
	watch.assertClean(t)

	var st signupState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("decode state %q: %v", raw, err)
	}
	return st
}

// TestE2ESignupCollapsesPasswordWhenPlatformAuthenticatorAvailable is the state-3
// case: a device that can create a passkey gets the password half collapsed behind a
// toggle, so the safe path is the default rather than one of two equal options.
//
// The password option must remain reachable — this is a nudge, not a wall — so the
// toggle is asserted to reveal it, and the input to become submittable again.
func TestE2ESignupCollapsesPasswordWhenPlatformAuthenticatorAvailable(t *testing.T) {
	srv := newE2EServer(t)
	st := readSignupState(t, srv, true)

	if !st.PasskeyVisible {
		t.Error("passkey option is hidden on a device that supports it")
	}
	if st.PasswordVisible {
		t.Error("password half is still expanded; it should collapse behind the toggle")
	}
	if !st.TogglePresent {
		t.Fatal("no \"Use a password instead\" toggle, so the password path is unreachable")
	}
	if !st.PasswordDisabled {
		t.Error("hidden password input is still enabled — a hidden required control makes " +
			"the browser refuse to submit, which anyone pressing Enter would hit")
	}
	if st.NoticeVisible {
		t.Error("the unsupported-browser notice is showing on a supported device")
	}
}

// TestE2ESignupTogglerRevealsPassword: the escape hatch has to actually work, or the
// collapse becomes a wall for anyone who wants a password anyway.
func TestE2ESignupTogglerRevealsPassword(t *testing.T) {
	srv := newE2EServer(t)

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	if err := virtualAuthenticator(bctx); err != nil {
		t.Fatalf("virtual authenticator: %v", err)
	}

	var visible, disabled bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(srv.authURL+"/signup"),
		chromedp.WaitVisible(`#use-password`, chromedp.ByQuery),
		chromedp.Click(`#use-password`, chromedp.ByQuery),
		chromedp.WaitVisible(`#password`, chromedp.ByQuery),
		chromedp.Evaluate(`!document.getElementById("password-section").classList.contains("d-none")`, &visible),
		chromedp.Evaluate(`document.getElementById("password").disabled`, &disabled),
	); err != nil {
		t.Fatalf("toggle password: %v", err)
	}
	watch.assertClean(t)

	if !visible {
		t.Error("the toggle didn't reveal the password half")
	}
	if disabled {
		t.Error("password input is still disabled after revealing it, so it won't submit")
	}
}

// TestE2ESignupEnterStartsPasskeyWhenCollapsed covers the trap a naive collapse falls
// into. With the password half hidden, the form's only submit button is the hidden
// one, so pressing Enter in the username field would post an empty password form —
// and because the hidden input is required, the browser refuses to submit at all and
// the keystroke silently does nothing.
//
// Enter should instead run the visible primary action. Proven end to end: a real
// account appears, which only happens if the passkey ceremony completed.
func TestE2ESignupEnterStartsPasskeyWhenCollapsed(t *testing.T) {
	srv := newE2EServer(t)

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	if err := virtualAuthenticator(bctx); err != nil {
		t.Fatalf("virtual authenticator: %v", err)
	}

	const username = "e2eenterkey"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(srv.authURL+"/signup"),
		// Wait for the toggle: its presence means the collapse has been applied, so
		// Enter is being pressed in exactly the state under test.
		chromedp.WaitVisible(`#use-password`, chromedp.ByQuery),
		chromedp.SendKeys(`#username`, username, chromedp.ByQuery),
		chromedp.SendKeys(`#username`, "\r", chromedp.ByQuery),
	); err != nil {
		t.Fatalf("submit via Enter: %v", err)
	}

	// The ceremony's server side finishes asynchronously after the keystroke.
	// waitForUser returns nil on timeout rather than failing, so the nil check is what
	// makes this assert anything at all — without it the test passes even when Enter
	// does nothing.
	if u := waitForUser(t, srv, username); u == nil {
		var status string
		_ = chromedp.Run(bctx, chromedp.Text("#passkey-status", &status, chromedp.ByID, chromedp.AtLeast(0)))
		t.Fatalf("Enter in the username field didn't start the passkey ceremony while the "+
			"password half was collapsed; page status = %q", status)
	}
	watch.assertClean(t)
}

// TestE2ESignupLeavesFormAloneWithoutPlatformAuthenticator is the state-2 case, and
// the reason the logic isn't a simple available/unavailable binary: headless Chrome
// with no virtual authenticator reports no *platform* authenticator, but registration
// asks only for a preferred resident key with no attachment constraint, so a roaming
// security key would still work. Nothing should be hidden or collapsed.
func TestE2ESignupLeavesFormAloneWithoutPlatformAuthenticator(t *testing.T) {
	srv := newE2EServer(t)
	st := readSignupState(t, srv, false)

	if !st.PasskeyVisible {
		t.Error("passkey option was hidden, but a security key would still work here")
	}
	if !st.PasswordVisible {
		t.Error("password half was collapsed without a platform authenticator to justify it")
	}
	if st.TogglePresent {
		t.Error("the toggle is showing even though nothing was collapsed")
	}
	if st.PasswordDisabled {
		t.Error("password input is disabled while visible — it wouldn't submit")
	}
	if st.NoticeVisible {
		t.Error("the unsupported-browser notice is showing, but WebAuthn is present")
	}
}
