package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	mailer "github.com/jleight/meshtender/internal/mail"
	"github.com/jleight/meshtender/internal/store"
)

// Black-box coverage for the password-reset flow: /forgot and /reset/{token}.
// The rules being defended, in order of how badly they'd hurt if broken:
//   1. the form must not reveal whether an account exists;
//   2. a passkey-only account is never resettable by email;
//   3. a link is single-use, expires, and kills other sessions when spent.

// allLinkPaths extracts every recovery link path from a message body, in order — the
// fan-out case sends several in one message.
func allLinkPaths(m mailer.Message) []string {
	var out []string
	for _, match := range tokenPathRe.FindAllStringSubmatch(m.Text, -1) {
		out = append(out, match[1])
	}
	return out
}

// labeledLinkRe matches the fan-out message's "@username\n<link>" pairing, so tests
// verify the label/link association the reader depends on rather than a fixed order.
var labeledLinkRe = regexp.MustCompile(`(?m)^@([a-zA-Z0-9_.-]+)\n(https?://[^\s]+)$`)

// labeledLinks maps each username named in a message to the link listed beneath it.
func labeledLinks(m mailer.Message) map[string]string {
	out := map[string]string{}
	for _, match := range labeledLinkRe.FindAllStringSubmatch(m.Text, -1) {
		u, err := url.Parse(match[2])
		if err != nil {
			continue
		}
		out[match[1]] = u.Path
	}
	return out
}

// recoverable creates a user with a password and a confirmed address, the state the
// whole flow requires. It writes the verification directly: the confirmation link is
// covered by its own tests, and repeating it here would only slow these down.
func recoverable(t *testing.T, st *store.Store, ctx context.Context, username, addr string) *store.User {
	t.Helper()
	u, err := st.CreateUser(ctx, username, "")
	if err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	if err := st.SetPassword(ctx, u.ID, testBcryptHash(t)); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if err := st.SetEmail(ctx, u.ID, addr); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if ok, err := st.MarkEmailVerified(ctx, u.ID, addr); err != nil || !ok {
		t.Fatalf("mark verified: ok=%v err=%v", ok, err)
	}
	reloaded, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	return reloaded
}

// forgot submits the reset form.
func forgot(t *testing.T, ts *httptest.Server, h hostEnv, identifier string) *http.Response {
	t.Helper()
	return post(t, ts, h.auth, "/forgot", url.Values{"identifier": {identifier}})
}

// TestForgotResponseIsIdenticalRegardless is the enumeration guard. The form is
// reachable by anyone, so a difference in status, location, or body between "this
// address has an account" and "it doesn't" would turn it into a membership oracle for
// email addresses.
func TestForgotResponseIsIdenticalRegardless(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, _ := splitServerMail(t)
	recoverable(t, st, ctx, "known-user", "known@example.test")
	// A passkey-only account: exists, has a confirmed address, but cannot be reset.
	pk, err := st.CreateUser(ctx, "passkey-user", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.SetEmail(ctx, pk.ID, "passkey@example.test"); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if ok, err := st.MarkEmailVerified(ctx, pk.ID, "passkey@example.test"); err != nil || !ok {
		t.Fatalf("mark verified: ok=%v err=%v", ok, err)
	}

	type shape struct {
		status int
		loc    string
		body   string
	}
	seen := map[string]shape{}
	for _, identifier := range []string{
		"known@example.test",   // resettable
		"passkey@example.test", // exists, not resettable
		"nobody@example.test",  // no account at all
		"known-user",           // resettable, by username
		"no-such-user",         // no account, by username
	} {
		resp := forgot(t, ts, h, identifier)
		s := shape{status: resp.StatusCode, loc: resp.Header.Get("Location"), body: readAll(t, resp)}
		seen[identifier] = s
	}

	var first shape
	var firstKey string
	for k, v := range seen {
		if firstKey == "" {
			first, firstKey = v, k
			continue
		}
		if v != first {
			t.Errorf("response for %q differs from %q — that difference is an account oracle:\n %+v\n vs %+v",
				k, firstKey, v, first)
		}
	}
	// And the shared response must actually be the confirmation, not an error page.
	if first.status != http.StatusSeeOther || !strings.Contains(first.loc, "sent=1") {
		t.Errorf("shared response = %d %q, want 303 to ?sent=1", first.status, first.loc)
	}
}

// TestForgotSendsNothingForUnknownAddress: the quiet half of the enumeration rule. The
// page says "check your email" regardless, but no message may actually go out — mailing
// a stranger would be both a privacy leak and a way to use us for spam.
func TestForgotSendsNothingForUnknownAddress(t *testing.T) {
	t.Parallel()
	_, _, ts, h, sender := splitServerMail(t)

	forgot(t, ts, h, "nobody@example.test").Body.Close()
	forgot(t, ts, h, "no-such-username").Body.Close()

	if sender.count() != 0 {
		t.Errorf("%d messages sent for identifiers with no account", sender.count())
	}
}

// TestForgotSkipsUnverifiedAddress: an address nobody proved must not receive a reset
// link, or someone could type a victim's address on their own account and have us mail
// a takeover link for the account they control — harmless — while also spamming the
// victim. Confirmation is what closes it.
func TestForgotSkipsUnverifiedAddress(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	u, err := st.CreateUser(ctx, "unverified-user", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.SetPassword(ctx, u.ID, testBcryptHash(t)); err != nil {
		t.Fatalf("set password: %v", err)
	}
	if err := st.SetEmail(ctx, u.ID, "unconfirmed@example.test"); err != nil {
		t.Fatalf("set email: %v", err)
	}

	forgot(t, ts, h, "unconfirmed@example.test").Body.Close()
	forgot(t, ts, h, "unverified-user").Body.Close()

	if sender.count() != 0 {
		t.Errorf("%d messages sent to an unconfirmed address", sender.count())
	}
}

// TestPasskeyOnlyAccountNeverGetsAResetLink is the regression test for the rule that
// keeps the strongest accounts strong: email recovery only ever sets a password on an
// account that already has one. If this ever fails, a passkey-only account's security
// has been silently reduced to whoever controls the mailbox.
func TestPasskeyOnlyAccountNeverGetsAResetLink(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	u, err := st.CreateUser(ctx, "pk-only", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.SetEmail(ctx, u.ID, "pkonly@example.test"); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if ok, err := st.MarkEmailVerified(ctx, u.ID, "pkonly@example.test"); err != nil || !ok {
		t.Fatalf("mark verified: ok=%v err=%v", ok, err)
	}

	forgot(t, ts, h, "pkonly@example.test").Body.Close()

	// A message IS sent — silence would leave them re-submitting forever — but it must
	// carry no reset link and must explain why.
	msg := sender.last(t)
	if links := allLinkPaths(msg); len(links) != 0 {
		t.Fatalf("a passkey-only account was mailed %d reset link(s): %v", len(links), links)
	}
	if !strings.Contains(msg.Text, "no password to reset") {
		t.Errorf("explanatory mail doesn't say why there's nothing to reset:\n%s", msg.Text)
	}
}

// TestResetHappyPath walks the whole flow the way a locked-out user does, then proves
// the new password works and the old one doesn't.
func TestResetHappyPath(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	recoverable(t, st, ctx, "reset-me", "resetme@example.test")

	forgot(t, ts, h, "resetme@example.test").Body.Close()
	path := linkPath(t, sender.last(t))

	// The GET names the account, so someone with two accounts picks correctly.
	page := readAll(t, do(t, ts, h.auth, path))
	if !strings.Contains(page, "@reset-me") {
		t.Errorf("reset form doesn't name the account it will change:\n%s", page)
	}

	const newPassword = "an-entirely-new-password"
	resp := post(t, ts, h.auth, path, url.Values{"new_password": {newPassword}, "confirm_password": {newPassword}})
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Path != "/login" || loc.Query().Get("ok") == "" {
		t.Fatalf("reset POST = %d %q, want 303 /login?ok=…", resp.StatusCode, resp.Header.Get("Location"))
	}

	// New password signs in.
	good := post(t, ts, h.auth, "/login/password",
		url.Values{"username": {"reset-me"}, "password": {newPassword}})
	good.Body.Close()
	if cookieByName(good, "meshtender_session") == nil {
		t.Error("the new password doesn't sign in")
	}
	// Old one doesn't.
	old := post(t, ts, h.auth, "/login/password",
		url.Values{"username": {"reset-me"}, "password": {testPassword}})
	old.Body.Close()
	if cookieByName(old, "meshtender_session") != nil {
		t.Error("the old password still signs in after a reset")
	}
}

// TestResetLinkIsSingleUse: the link is a credential, so spending it must consume it.
// A replayable link means a mailbox archive stays dangerous indefinitely.
func TestResetLinkIsSingleUse(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	recoverable(t, st, ctx, "single-use", "single@example.test")
	forgot(t, ts, h, "single@example.test").Body.Close()
	path := linkPath(t, sender.last(t))

	first := post(t, ts, h.auth, path, url.Values{"new_password": {"first-new-password"}, "confirm_password": {"first-new-password"}})
	first.Body.Close()

	second := post(t, ts, h.auth, path, url.Values{"new_password": {"second-new-password"}, "confirm_password": {"second-new-password"}})
	body := readAll(t, second)
	if !strings.Contains(body, "This link doesn't work") {
		t.Error("a spent reset link was accepted a second time")
	}
	// And the second attempt's password must not have taken effect.
	resp := post(t, ts, h.auth, "/login/password",
		url.Values{"username": {"single-use"}, "password": {"second-new-password"}})
	resp.Body.Close()
	if cookieByName(resp, "meshtender_session") != nil {
		t.Error("the replayed link changed the password")
	}
}

// TestResetGetDoesNotSpendToken: mail clients and scanners prefetch links. If the GET
// consumed the token, the user would open a dead form every time.
func TestResetGetDoesNotSpendToken(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	recoverable(t, st, ctx, "prefetch", "prefetch@example.test")
	forgot(t, ts, h, "prefetch@example.test").Body.Close()
	path := linkPath(t, sender.last(t))

	// Two "prefetches", then the real submission.
	do(t, ts, h.auth, path).Body.Close()
	do(t, ts, h.auth, path).Body.Close()

	resp := post(t, ts, h.auth, path, url.Values{"new_password": {"still-works-password"}, "confirm_password": {"still-works-password"}})
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Path != "/login" || loc.Query().Get("ok") == "" {
		t.Fatalf("reset after prefetch = %q, want the success redirect", resp.Header.Get("Location"))
	}
}

// TestResetRejectsShortPasswordWithoutSpendingToken: a rejected password must cost a
// correction, not the user's only link. This is the ordering bug the handler is
// written to avoid — validate, then consume.
func TestResetRejectsShortPasswordWithoutSpendingToken(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	recoverable(t, st, ctx, "shortpw", "shortpw@example.test")
	forgot(t, ts, h, "shortpw@example.test").Body.Close()
	path := linkPath(t, sender.last(t))

	bad := post(t, ts, h.auth, path, url.Values{"new_password": {"short"}, "confirm_password": {"short"}})
	body := readAll(t, bad)
	if !strings.Contains(body, "at least") {
		t.Errorf("no length error shown:\n%s", body)
	}
	if strings.Contains(body, "This link doesn't work") {
		t.Fatal("the token was spent by a rejected password")
	}

	// The same link still works with an acceptable password.
	good := post(t, ts, h.auth, path, url.Values{"new_password": {"now-long-enough-password"}, "confirm_password": {"now-long-enough-password"}})
	good.Body.Close()
	loc, _ := url.Parse(good.Header.Get("Location"))
	if loc.Query().Get("ok") == "" {
		t.Errorf("retry after a short password failed: %q", good.Header.Get("Location"))
	}
}

// TestResetRejectsMismatchedConfirmationWithoutSpendingToken: the confirmation
// field is worth having on this page above all others — the person typing is
// already locked out, can't see what they're typing, and the next thing they do
// with it is sign in. Like the length check, a mismatch has to be caught before
// the token is consumed, or a typo costs them the link rather than a retry.
func TestResetRejectsMismatchedConfirmationWithoutSpendingToken(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	u := recoverable(t, st, ctx, "typopw", "typopw@example.test")
	forgot(t, ts, h, "typopw@example.test").Body.Close()
	path := linkPath(t, sender.last(t))

	bad := post(t, ts, h.auth, path, url.Values{
		"new_password":     {"a-long-enough-password"},
		"confirm_password": {"a-long-enough-passwrod"},
	})
	body := readAll(t, bad)
	if !strings.Contains(body, "passwords don&#39;t match") && !strings.Contains(body, "passwords don't match") {
		t.Errorf("no mismatch error shown:\n%s", body)
	}
	if strings.Contains(body, "This link doesn't work") {
		t.Fatal("the token was spent by a mistyped confirmation")
	}

	// Nothing was written, so the old password still signs in.
	before, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if before.PasswordHash == nil {
		t.Fatal("a rejected reset cleared the password")
	}

	// The same link still works once the two fields agree.
	good := post(t, ts, h.auth, path, url.Values{
		"new_password":     {"a-long-enough-password"},
		"confirm_password": {"a-long-enough-password"},
	})
	good.Body.Close()
	loc, _ := url.Parse(good.Header.Get("Location"))
	if loc.Query().Get("ok") == "" {
		t.Errorf("retry after a mismatch failed: %q", good.Header.Get("Location"))
	}
}

// TestResetRevokesExistingSessions: if an attacker got in with the stolen password,
// the reset is the moment they're evicted. A reset that left their session alive would
// leave the account compromised while looking recovered.
func TestResetRevokesExistingSessions(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	u := recoverable(t, st, ctx, "evict-me", "evict@example.test")

	// A live app session for this account, established through the real handoff (the
	// same path a browser takes), so the revocation is exercised end to end.
	loginID, err := st.CreateLogin(ctx, u.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}
	code, err := st.CreateAuthCode(ctx, u.ID, loginID, "/")
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	handoff := do(t, ts, h.app, "/session/callback?code="+code+"&state=s1",
		&http.Cookie{Name: "mt_state", Value: "s1"})
	handoff.Body.Close()
	victim := cookieByName(handoff, "meshtender_session")
	if victim == nil {
		t.Fatal("no session cookie after handoff")
	}

	// Confirm the session works before the reset.
	before := do(t, ts, h.app, "/", victim)
	before.Body.Close()
	if before.StatusCode == http.StatusSeeOther {
		t.Fatalf("precondition: session was already invalid (%d)", before.StatusCode)
	}

	forgot(t, ts, h, "evict@example.test").Body.Close()
	path := linkPath(t, sender.last(t))
	post(t, ts, h.auth, path, url.Values{"new_password": {"brand-new-password-here"}, "confirm_password": {"brand-new-password-here"}}).Body.Close()

	after := do(t, ts, h.app, "/", victim)
	after.Body.Close()
	if after.StatusCode != http.StatusSeeOther {
		t.Errorf("the pre-existing session survived the reset (status %d)", after.StatusCode)
	}
}

// TestResetFanoutNamesEachAccount covers the shared-address case the schema allows on
// purpose: one message, one link per account, each naming its own account. If the links
// were interchangeable — or unlabeled — the user would change the wrong password.
func TestResetFanoutNamesEachAccount(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	const shared = "shared@example.test"
	recoverable(t, st, ctx, "personal-acct", shared)
	recoverable(t, st, ctx, "ops-acct", shared)

	forgot(t, ts, h, shared).Body.Close()

	// One message, not one per account: several would spend several sends of a metered
	// daily quota and read as duplicates to the recipient.
	if sender.count() != 1 {
		t.Fatalf("%d messages sent for one address, want 1", sender.count())
	}
	msg := sender.last(t)
	for _, name := range []string{"@personal-acct", "@ops-acct"} {
		if !strings.Contains(msg.Text, name) {
			t.Errorf("message doesn't name %s:\n%s", name, msg.Text)
		}
	}
	links := allLinkPaths(msg)
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2:\n%s", len(links), msg.Text)
	}
	if links[0] == links[1] {
		t.Fatal("both accounts got the same link")
	}

	// The pairing is read out of the message rather than assumed: the listing order is
	// the store's (username-sorted), and hard-coding an order here would make this test
	// fail for a reason that has nothing to do with the property being checked. What
	// matters is that each link, followed, names the account it was listed under —
	// that's what stops someone changing the wrong account's password.
	pairs := labeledLinks(msg)
	if len(pairs) != 2 {
		t.Fatalf("parsed %d name/link pairs, want 2:\n%s", len(pairs), msg.Text)
	}
	for name, path := range pairs {
		page := readAll(t, do(t, ts, h.auth, path))
		if !strings.Contains(page, "@"+name) {
			t.Errorf("the link listed under @%s resolves to a form naming a different account:\n%s", name, page)
		}
	}
	for _, want := range []string{"personal-acct", "ops-acct"} {
		if _, ok := pairs[want]; !ok {
			t.Errorf("no link listed for @%s", want)
		}
	}
}

// TestResetFanoutMentionsUnrecoverableAccounts: when one of the accounts on an address
// can't be reset, saying so beats omitting it — the reader controls the mailbox
// anyway, and silence reads as "we lost my other account".
func TestResetFanoutMentionsUnrecoverableAccounts(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	const shared = "mixed@example.test"
	recoverable(t, st, ctx, "has-pw", shared)
	pk, err := st.CreateUser(ctx, "no-pw", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.SetEmail(ctx, pk.ID, shared); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if ok, err := st.MarkEmailVerified(ctx, pk.ID, shared); err != nil || !ok {
		t.Fatalf("mark verified: ok=%v err=%v", ok, err)
	}

	forgot(t, ts, h, shared).Body.Close()
	msg := sender.last(t)

	if links := allLinkPaths(msg); len(links) != 1 {
		t.Fatalf("got %d links, want exactly 1 (only the password account):\n%s", len(links), msg.Text)
	}
	if !strings.Contains(msg.Text, "@no-pw") {
		t.Errorf("the unrecoverable account isn't mentioned:\n%s", msg.Text)
	}
}

// TestResetRefusedIfPasswordRemovedAfterSending: the account can change between the
// link being mailed and used. Honouring a stale link would ADD a password to a
// passkey-only account from a mailbox — the exact demotion the design refuses — so the
// check has to happen at redemption, not only at send time.
func TestResetRefusedIfPasswordRemovedAfterSending(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	u := recoverable(t, st, ctx, "went-passkey", "went@example.test")
	forgot(t, ts, h, "went@example.test").Body.Close()
	path := linkPath(t, sender.last(t))

	// The user adds a passkey and drops their password before clicking.
	if err := st.ClearPassword(ctx, u.ID); err != nil {
		t.Fatalf("clear password: %v", err)
	}

	resp := post(t, ts, h.auth, path, url.Values{"new_password": {"should-not-apply-pw"}, "confirm_password": {"should-not-apply-pw"}})
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("error") == "" {
		t.Errorf("stale link was honoured: %q", resp.Header.Get("Location"))
	}

	after, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.PasswordHash != nil {
		t.Fatal("email recovery put a password back on a passkey-only account")
	}
}

// TestForgotCapsMessagesPerAddressSilently: repeated requests for one address must
// stop producing mail well before they become a way to bury someone's inbox (or drain
// a metered daily quota) — and must keep answering with the same confirmation, since a
// visible "too many requests" would confirm the address is worth targeting.
//
// The loop stays under the per-IP burst on purpose: this is about the identifier-keyed
// cap, and a per-IP 429 (generic, and identical for any identifier) would mask it.
func TestForgotCapsMessagesPerAddressSilently(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, sender := splitServerMail(t)
	recoverable(t, st, ctx, "throttled", "throttle@example.test")

	const attempts = 5
	for i := range attempts {
		resp := forgot(t, ts, h, "throttle@example.test")
		status, loc := resp.StatusCode, resp.Header.Get("Location")
		resp.Body.Close()
		// Byte-identical every time, capped or not.
		if status != http.StatusSeeOther || !strings.Contains(loc, "sent=1") {
			t.Fatalf("attempt %d = %d %q; a capped request must look exactly like an accepted one",
				i+1, status, loc)
		}
	}
	if n := sender.count(); n >= attempts {
		t.Errorf("%d messages sent for %d requests to one address — nothing capped it", n, attempts)
	}
}

// TestForgotHiddenWithoutMailProvider: with no delivery path the routes must not exist
// and the sign-in page must not advertise them — a "Forgot password?" link that leads
// nowhere is worse than none.
func TestForgotHiddenWithoutMailProvider(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServerNoMail(t)

	page := readAll(t, do(t, ts, h.auth, "/login"))
	if strings.Contains(page, "Forgot password?") {
		t.Error("sign-in page offers password reset with no mail provider configured")
	}
	resp := do(t, ts, h.auth, "/forgot")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /forgot = %d with mail disabled, want 404", resp.StatusCode)
	}
}

// TestLoginPageShowsForgotLink: the other half of U1's fix — when mail IS configured,
// the link has to be where a locked-out user looks for it.
func TestLoginPageShowsForgotLink(t *testing.T) {
	t.Parallel()
	_, _, ts, h, _ := splitServerMail(t)

	page := readAll(t, do(t, ts, h.auth, "/login"))
	if !strings.Contains(page, "Forgot password?") || !strings.Contains(page, `href="/forgot"`) {
		t.Error("sign-in page has no Forgot password? link")
	}
}
