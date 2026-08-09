package core

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"

	mailer "github.com/MeshTender/MeshTender/internal/mail"
)

// Black-box coverage for the optional account email: setting it, confirming it, and
// what the UI promises about it. The reset flow itself is covered separately.

// fakeSender captures outbound mail instead of delivering it.
type fakeSender struct {
	mu   sync.Mutex
	sent []mailer.Message
}

func (f *fakeSender) Send(_ context.Context, m mailer.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// last returns the most recent message, failing the test if nothing was sent.
func (f *fakeSender) last(t *testing.T) mailer.Message {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("no mail was sent")
	}
	return f.sent[len(f.sent)-1]
}

func (f *fakeSender) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = nil
}

// tokenPathRe pulls a token out of a link in a message body, so tests follow the
// same URL a recipient would click rather than reaching into the store.
var tokenPathRe = regexp.MustCompile(`https?://[^/\s]+(/(?:verify-email|reset)/[A-Za-z0-9_-]+)`)

// linkPath extracts the first recovery link's path from a message body.
func linkPath(t *testing.T, m mailer.Message) string {
	t.Helper()
	match := tokenPathRe.FindStringSubmatch(m.Text)
	if match == nil {
		t.Fatalf("no recovery link in message body:\n%s", m.Text)
	}
	return match[1]
}

// setEmail posts an address to the account page with the given SSO session.
func setEmail(t *testing.T, ts *httptest.Server, h hostEnv, sso *http.Cookie, addr string) *http.Response {
	t.Helper()
	return post(t, ts, h.auth, "/account/email", url.Values{"email": {addr}}, sso)
}

// TestSetEmailStartsUnverifiedAndMailsLink: saving an address must not trust it.
// Confirmation is what stops a typo from pointing recovery at a stranger's mailbox.
func TestSetEmailStartsUnverifiedAndMailsLink(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	sso := authSSO(t, ts, h, "emailuser")

	resp := setEmail(t, ts, h, sso, "user@example.test")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("set email = %d, want 303", resp.StatusCode)
	}

	u, err := st.GetUserByUsername(ctx, "emailuser")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.Email == nil || *u.Email != "user@example.test" {
		t.Fatalf("stored email = %v", u.Email)
	}
	if u.EmailVerified() {
		t.Error("address is verified straight from the form, with nothing proving the user owns it")
	}

	msg := sender.last(t)
	if msg.Kind != mailer.KindVerifyEmail {
		t.Errorf("Kind = %q, want %q", msg.Kind, mailer.KindVerifyEmail)
	}
	if msg.To != "user@example.test" {
		t.Errorf("To = %q", msg.To)
	}
	if msg.IdempotencyKey == "" {
		t.Error("no idempotency key, so a resubmitted form can mail the link twice")
	}
	// The raw token must not be reused as the provider-facing key.
	if strings.Contains(msg.Text, msg.IdempotencyKey) {
		t.Error("idempotency key is the token itself; it should be derived")
	}
}

// TestVerifyEmailLinkConfirmsAddress walks the confirmation the way a recipient
// does — following the link from the message body, with no session, since the
// mailbox is usually open in another browser or on a phone.
func TestVerifyEmailLinkConfirmsAddress(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	sso := authSSO(t, ts, h, "verifyuser")
	setEmail(t, ts, h, sso, "verify@example.test").Body.Close()

	path := linkPath(t, sender.last(t))
	// No cookie: a link opened in a different browser must still work.
	resp := do(t, ts, h.auth, path)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("verify = %d, want 303", resp.StatusCode)
	}

	u, err := st.GetUserByUsername(ctx, "verifyuser")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if !u.EmailVerified() {
		t.Fatal("address still unverified after following the confirmation link")
	}
	if !u.CanResetPassword() {
		t.Error("a password account with a confirmed address still can't be reset by email")
	}

	// Single-use: the same link must not confirm anything a second time.
	again := do(t, ts, h.auth, path)
	again.Body.Close()
	loc, _ := url.Parse(again.Header.Get("Location"))
	if loc.Query().Get("emerr") == "" && loc.Query().Get("error") == "" {
		t.Error("a spent confirmation link reported success on replay")
	}
}

// TestVerifyEmailRejectsGarbageToken: an unknown token is an invalid link, never a
// server error and never a silent success.
func TestVerifyEmailRejectsGarbageToken(t *testing.T) {
	t.Parallel()
	_, _, ts, h, _ := splitServerMail(t)

	resp := do(t, ts, h.auth, "/verify-email/not-a-real-token")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("verify with garbage token = %d, want 303", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("emerr") == "" && loc.Query().Get("error") == "" {
		t.Error("a bogus token produced no error message")
	}
}

// TestStaleVerifyLinkCannotConfirmNewAddress closes the swap window: ask for a link
// for one address, change the address, then click the old link. Without the
// address re-check, the stale mail would confirm an address its recipient never
// approved.
func TestStaleVerifyLinkCannotConfirmNewAddress(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	sso := authSSO(t, ts, h, "swapuser")

	setEmail(t, ts, h, sso, "first@example.test").Body.Close()
	stale := linkPath(t, sender.last(t))
	setEmail(t, ts, h, sso, "second@example.test").Body.Close()

	resp := do(t, ts, h.auth, stale)
	resp.Body.Close()

	u, err := st.GetUserByUsername(ctx, "swapuser")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.EmailVerified() {
		t.Fatal("a link issued for the previous address confirmed the current one")
	}
}

// TestSetEmailRejectsBadAddresses: the stored value ends up in a To: header, so a
// display-name form or a bare word must not get through.
func TestSetEmailRejectsBadAddresses(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	sso := authSSO(t, ts, h, "badaddr")

	for _, bad := range []string{
		"",
		"not-an-address",
		"Someone <someone@example.test>", // display-name form
		"a@example.test, b@example.test", // two addresses
		"a@example.test\nBcc: victim@example.test", // header injection attempt
	} {
		resp := setEmail(t, ts, h, sso, bad)
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if loc.Query().Get("emerr") == "" {
			t.Errorf("address %q was accepted", bad)
		}
	}
	u, err := st.GetUserByUsername(ctx, "badaddr")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.Email != nil {
		t.Errorf("stored email = %v after only invalid submissions", u.Email)
	}
	if sender.count() != 0 {
		t.Errorf("%d messages sent for invalid addresses", sender.count())
	}
}

// TestRemoveEmailIsImmediateAndKillsLinks: "optional" has to mean removable, and
// removal must invalidate links already sitting in a mailbox the user may no longer
// control.
func TestRemoveEmailIsImmediateAndKillsLinks(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	sso := authSSO(t, ts, h, "removeuser")
	setEmail(t, ts, h, sso, "gone@example.test").Body.Close()
	stale := linkPath(t, sender.last(t))

	resp := post(t, ts, h.auth, "/account/email", url.Values{"remove": {"1"}}, sso)
	resp.Body.Close()

	u, err := st.GetUserByUsername(ctx, "removeuser")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if u.Email != nil {
		t.Errorf("Email = %v after removal, want nil", u.Email)
	}
	// The outstanding confirmation link must be dead.
	do(t, ts, h.auth, stale).Body.Close()
	after, err := st.GetUserByUsername(ctx, "removeuser")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.Email != nil || after.EmailVerified() {
		t.Error("a link from before removal resurrected the address")
	}
}

// TestVerificationResendIsCapped: the per-account budget is what stops us being used
// to bury someone's inbox (and stops one account draining a metered daily quota).
func TestVerificationResendIsCapped(t *testing.T) {
	t.Parallel()
	_, _, ts, h, sender := splitServerMail(t)
	sso := authSSO(t, ts, h, "resenduser")
	setEmail(t, ts, h, sso, "resend@example.test").Body.Close()
	sender.reset()

	var capped bool
	// The first send already spent one token, so the budget runs out inside this loop.
	for range 10 {
		resp := post(t, ts, h.auth, "/account/email/verify", url.Values{}, sso)
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if strings.Contains(strings.ToLower(loc.Query().Get("emerr")), "too many") {
			capped = true
			break
		}
	}
	if !capped {
		t.Fatal("resend was never capped; an account can mail its address without limit")
	}
	if sender.count() >= 10 {
		t.Errorf("%d messages sent before the cap bit", sender.count())
	}
}

// TestAccountPageStatesAreHonest is the anti-false-promise test. What the Email card
// claims must match what the account can actually do — in particular, a passkey-only
// account must be told plainly that its address cannot recover it, rather than being
// shown reassuring copy that only applies to password holders.
func TestAccountPageStatesAreHonest(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)

	t.Run("password account with a confirmed address is told reset works", func(t *testing.T) {
		sso := authSSO(t, ts, h, "honest-pw")
		setEmail(t, ts, h, sso, "honest-pw@example.test").Body.Close()
		do(t, ts, h.auth, linkPath(t, sender.last(t))).Body.Close()

		page := authPageHTML(t, ts, h, "/account", sso)
		if !strings.Contains(page, "reset your password by email") {
			t.Error("a resettable account isn't told it can reset by email")
		}
	})

	t.Run("passkey-only account is told its address cannot recover it", func(t *testing.T) {
		sso := authSSO(t, ts, h, "honest-pk")
		setEmail(t, ts, h, sso, "honest-pk@example.test").Body.Close()
		do(t, ts, h.auth, linkPath(t, sender.last(t))).Body.Close()

		u, err := st.GetUserByUsername(ctx, "honest-pk")
		if err != nil {
			t.Fatalf("get user: %v", err)
		}
		// Drop the password directly: the UI route requires a passkey first, and this
		// test is about the resulting state, not that guard.
		if err := st.ClearPassword(ctx, u.ID); err != nil {
			t.Fatalf("clear password: %v", err)
		}

		page := authPageHTML(t, ts, h, "/account", sso)
		if !strings.Contains(page, "no password, so there's nothing to reset") {
			t.Error("a passkey-only account isn't told its address can't recover it")
		}
		if strings.Contains(page, "You can reset your password by email") {
			t.Error("a passkey-only account is promised email reset it will never get")
		}
	})
}

// TestEmailNeverAppearsOnPublicProfile: the address is recovery data, not profile
// data. Leaking it on /u/{username} would publish something users gave us for one
// narrow purpose.
func TestEmailNeverAppearsOnPublicProfile(t *testing.T) {
	t.Parallel()
	_, _, ts, h, sender := splitServerMail(t)
	const addr = "private@example.test"
	sso := authSSO(t, ts, h, "publicuser")
	setEmail(t, ts, h, sso, addr).Body.Close()
	do(t, ts, h.auth, linkPath(t, sender.last(t))).Body.Close()

	for _, page := range []struct{ host, path string }{
		{h.root, "/u/publicuser"},
		{h.app, "/u/publicuser"},
	} {
		resp := do(t, ts, page.host, page.path)
		body := readAll(t, resp)
		if strings.Contains(body, addr) {
			t.Errorf("%s%s leaks the account's email address", page.host, page.path)
		}
	}
}

// TestRecoveryEmailChecklistStepTargetsPasswordUsers: the nudge is only meaningful
// for accounts that can actually use it. Asking a passkey-only user for an address we
// would never send a reset to is asking for data we can't use.
func TestRecoveryEmailChecklistStepTargetsPasswordUsers(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, _ := splitServerMail(t)

	t.Run("password user with no address sees the step", func(t *testing.T) {
		jar := newJar(t)
		u := seedSession(t, ts, st, ctx, jar, "checklist-pw")
		if err := st.SetPassword(ctx, u.ID, testBcryptHash(t)); err != nil {
			t.Fatalf("set password: %v", err)
		}
		page := dashboardHTML(t, ts, h.app, jar.Cookies(mustURL(t, ts.URL)))
		if !strings.Contains(page, "Add a recovery email") {
			t.Error("password account isn't nudged to add a recovery email")
		}
		if !strings.Contains(page, "Add email") {
			t.Error("the step renders no CTA, so it reads as already satisfied")
		}
	})

	t.Run("passkey-only user is not asked", func(t *testing.T) {
		jar := newJar(t)
		seedSession(t, ts, st, ctx, jar, "checklist-pk") // no password
		page := dashboardHTML(t, ts, h.app, jar.Cookies(mustURL(t, ts.URL)))
		if strings.Contains(page, "Add a recovery email") {
			t.Error("a passkey-only account is asked for an address that could never recover it")
		}
	})

	t.Run("step is satisfied once the address is confirmed", func(t *testing.T) {
		jar := newJar(t)
		u := seedSession(t, ts, st, ctx, jar, "checklist-done")
		if err := st.SetPassword(ctx, u.ID, testBcryptHash(t)); err != nil {
			t.Fatalf("set password: %v", err)
		}
		if err := st.SetEmail(ctx, u.ID, "done@example.test"); err != nil {
			t.Fatalf("set email: %v", err)
		}
		if ok, err := st.MarkEmailVerified(ctx, u.ID, "done@example.test"); err != nil || !ok {
			t.Fatalf("mark verified: ok=%v err=%v", ok, err)
		}
		page := dashboardHTML(t, ts, h.app, jar.Cookies(mustURL(t, ts.URL)))
		if strings.Contains(page, "Add email") {
			t.Error("the step still shows its CTA for someone with a confirmed address")
		}
	})

}

// authPageHTML fetches an auth-host page with the given session and returns its body.
func authPageHTML(t *testing.T, ts *httptest.Server, h hostEnv, path string, sso *http.Cookie) string {
	t.Helper()
	resp := do(t, ts, h.auth, path, sso)
	return readAll(t, resp)
}

// readAll drains and closes a response body.
func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(body)
}

// newJar returns a cookie jar for driving app-host sessions.
func newJar(t *testing.T) *cookiejar.Jar {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return jar
}

// testBcryptHash is a stored password hash for fixtures that only need "this account
// has a password", without going through the sign-up form.
func testBcryptHash(t *testing.T) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(testPassword), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}
