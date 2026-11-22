package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/auth"
	mailer "github.com/MeshTender/MeshTender/internal/mail"
	"github.com/MeshTender/MeshTender/internal/store"
)

// Admin-issued account recovery: the way back in for a passkey-only account whose
// passkey stopped working. These cover issuing the link and everything about
// redeeming it short of completing a WebAuthn ceremony, which only a browser can
// do (see internal/e2e/recovery_test.go).

// recoveryAuth builds an auth service with captured mail, for calling
// IssueRecoveryLink directly.
func recoveryAuth(t *testing.T, mailEnabled bool) (*auth.Service, *store.Store, context.Context, *fakeSender) {
	t.Helper()
	st, ctx := coreStore(t)
	sender := &fakeSender{}
	cfg := testAuthConfig()
	cfg.Mail, cfg.MailEnabled = sender, mailEnabled
	return testAuth(t, st, cfg), st, ctx, sender
}

// adminRequest stands in for the admin's request, which only supplies the port
// the link's origin is built with.
func adminRequest() *http.Request {
	return httptest.NewRequest(http.MethodPost, "http://"+testAppHost+"/admin/users/1/recovery", nil)
}

// recoveryToken pulls the token out of a recovery link.
func recoveryToken(t *testing.T, link string) string {
	t.Helper()
	_, tok, ok := strings.Cut(link, "/recover/")
	if !ok || tok == "" {
		t.Fatalf("not a recovery link: %q", link)
	}
	return tok
}

// passkeyOnlyUser makes the account this feature exists for: no password, no
// address.
func passkeyOnlyUser(t *testing.T, st *store.Store, ctx context.Context, name string) *store.User {
	t.Helper()
	u, err := st.CreateUser(ctx, name, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := st.AddCredential(ctx, u.ID, []byte("broken-"+name), []byte(`{}`), "Old phone"); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	return u
}

// TestIssueRecoveryLinkMailsAndSavesAddress: the case we built this for. The
// address the person gave the admin is saved, unconfirmed, and the link goes to it.
func TestIssueRecoveryLinkMailsAndSavesAddress(t *testing.T) {
	t.Parallel()
	svc, st, ctx, sender := recoveryAuth(t, true)
	u := passkeyOnlyUser(t, st, ctx, "lockedout")

	link, err := svc.IssueRecoveryLink(ctx, adminRequest(), u.ID, "  Locked@Example.test ")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if link.MailErr != nil || link.MailedTo != "Locked@Example.test" {
		t.Fatalf("MailedTo = %q, MailErr = %v; want mailed to the trimmed address", link.MailedTo, link.MailErr)
	}
	if !strings.HasPrefix(link.URL, "http://"+testAuthHost) {
		t.Errorf("link %q isn't on the auth host, where passkeys are registered", link.URL)
	}

	msg := sender.last(t)
	if msg.Kind != mailer.KindAccountRecovery || msg.To != "Locked@Example.test" {
		t.Errorf("mail kind=%q to=%q", msg.Kind, msg.To)
	}
	if !strings.Contains(msg.Text, link.URL) || !strings.Contains(msg.Text, "@lockedout") {
		t.Errorf("mail body doesn't carry the link and the account:\n%s", msg.Text)
	}
	if msg.IdempotencyKey == "" || strings.Contains(link.URL, msg.IdempotencyKey) {
		t.Error("idempotency key missing, or it's the raw token")
	}

	got, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Email == nil || *got.Email != "Locked@Example.test" {
		t.Errorf("stored email = %v", got.Email)
	}
	if got.EmailVerified() {
		t.Error("address confirmed just because an admin typed it")
	}
	// The link confirms that address when it's used.
	tok, ok, err := st.PeekEmailToken(ctx, store.PurposeRecoverAccount, recoveryToken(t, link.URL))
	if err != nil || !ok || tok.Email != "Locked@Example.test" {
		t.Errorf("token = %+v ok=%v err=%v; want it bound to the mailed address", tok, ok, err)
	}
	// Still a passkey account: recovery never adds a password.
	if got.PasswordHash != nil {
		t.Error("issuing a recovery link set a password")
	}
}

// TestIssueRecoveryLinkKeepsConfirmedAddress: re-sending to an address the account
// already confirmed (typed in a different case) mustn't unconfirm it.
func TestIssueRecoveryLinkKeepsConfirmedAddress(t *testing.T) {
	t.Parallel()
	svc, st, ctx, _ := recoveryAuth(t, true)
	u := passkeyOnlyUser(t, st, ctx, "hasmail")
	if err := st.SetEmail(ctx, u.ID, "me@example.test"); err != nil {
		t.Fatalf("set email: %v", err)
	}
	if ok, err := st.MarkEmailVerified(ctx, u.ID, "me@example.test"); err != nil || !ok {
		t.Fatalf("verify: ok=%v err=%v", ok, err)
	}

	if _, err := svc.IssueRecoveryLink(ctx, adminRequest(), u.ID, "ME@example.test"); err != nil {
		t.Fatalf("issue: %v", err)
	}
	got, _ := st.GetUserByID(ctx, u.ID)
	if !got.EmailVerified() || *got.Email != "me@example.test" {
		t.Errorf("email = %v verified=%v; want the confirmed address untouched", got.Email, got.EmailVerified())
	}
}

// TestIssueRecoveryLinkWithoutMail: with no address, or no mail provider, the link
// is only shown to the admin. It mustn't be bound to an address, because using it
// proves nothing about any mailbox.
func TestIssueRecoveryLinkWithoutMail(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		mailEnabled bool
		addr        string
	}{
		{"no address", true, ""},
		{"mail not configured", false, "someone@example.test"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			svc, st, ctx, sender := recoveryAuth(t, c.mailEnabled)
			u := passkeyOnlyUser(t, st, ctx, "handover")

			link, err := svc.IssueRecoveryLink(ctx, adminRequest(), u.ID, c.addr)
			if err != nil {
				t.Fatalf("issue: %v", err)
			}
			if link.MailedTo != "" || sender.count() != 0 {
				t.Errorf("mailed to %q (%d sent); want nothing sent", link.MailedTo, sender.count())
			}
			tok, ok, err := st.PeekEmailToken(ctx, store.PurposeRecoverAccount, recoveryToken(t, link.URL))
			if err != nil || !ok {
				t.Fatalf("link doesn't work: ok=%v err=%v", ok, err)
			}
			if tok.Email != "" {
				t.Errorf("token bound to %q, but nothing was mailed there", tok.Email)
			}
		})
	}
}

// TestIssueRecoveryLinkRejectsBadAddress: the same address rules as the account
// page, so a display-name form can't put text into a To: header.
func TestIssueRecoveryLinkRejectsBadAddress(t *testing.T) {
	t.Parallel()
	svc, st, ctx, sender := recoveryAuth(t, true)
	u := passkeyOnlyUser(t, st, ctx, "badaddr")

	for _, addr := range []string{"not an address", "Someone <a@example.test>", "a@example.test, b@example.test"} {
		if _, err := svc.IssueRecoveryLink(ctx, adminRequest(), u.ID, addr); !errors.Is(err, auth.ErrInvalidEmail) {
			t.Errorf("IssueRecoveryLink(%q) err = %v, want ErrInvalidEmail", addr, err)
		}
	}
	if sender.count() != 0 {
		t.Error("mail sent for a rejected address")
	}
	if got, _ := st.GetUserByID(ctx, u.ID); got.Email != nil {
		t.Errorf("rejected address was saved: %q", *got.Email)
	}
}

// mintRecovery issues a recovery token straight from the store, for the redemption
// tests.
func mintRecovery(t *testing.T, st *store.Store, ctx context.Context, userID int64) string {
	t.Helper()
	tok, err := st.ReplaceEmailToken(ctx, userID, store.PurposeRecoverAccount, "", store.RecoverTokenTTL)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	return tok
}

// recoveryStillWorks reports whether a token can still be redeemed.
func recoveryStillWorks(t *testing.T, st *store.Store, ctx context.Context, tok string) bool {
	t.Helper()
	_, ok, err := st.PeekEmailToken(ctx, store.PurposeRecoverAccount, tok)
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	return ok
}

// TestRecoverPageDoesNotSpendLink: mail scanners open links before people do. The
// page names the account and leaves the link working.
func TestRecoverPageDoesNotSpendLink(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	u := passkeyOnlyUser(t, st, ctx, "pagepeek")
	tok := mintRecovery(t, st, ctx, u.ID)

	for i := 0; i < 2; i++ {
		resp := do(t, ts, h.auth, "/recover/"+tok)
		body := readBody(t, resp)
		if resp.StatusCode != http.StatusOK || !strings.Contains(body, "@pagepeek") || !strings.Contains(body, `id="recover-passkey-btn"`) {
			t.Fatalf("visit %d: status %d, body lacks the account or the button:\n%s", i+1, resp.StatusCode, body)
		}
		if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
			t.Errorf("Cache-Control = %q; a page holding a live link must not be cached", cc)
		}
	}
	if !recoveryStillWorks(t, st, ctx, tok) {
		t.Error("viewing the page spent the link")
	}
}

// TestRecoverPageInvalidLink explains a dead link rather than erroring.
func TestRecoverPageInvalidLink(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	resp := do(t, ts, h.auth, "/recover/not-a-real-token")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `data-testid="recover-invalid"`) {
		t.Fatalf("status %d, want the invalid-link explanation:\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, "recover-passkey-btn") {
		t.Error("a dead link still offers to create a passkey")
	}
}

// beginRecovery posts to /api/recover/begin and returns the response.
func beginRecovery(t *testing.T, ts *httptest.Server, h hostEnv, tok string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"token": tok})
	return postBody(t, ts, h.auth, "/api/recover/begin", "application/json", string(body))
}

// TestRecoverBeginOffersRegistration: begin returns creation options for the
// existing account, without excluding its passkeys, and doesn't spend the link.
// Excluding them would make a device that still holds the broken passkey refuse
// to create a new one.
func TestRecoverBeginOffersRegistration(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	u := passkeyOnlyUser(t, st, ctx, "beginner")
	tok := mintRecovery(t, st, ctx, u.ID)

	resp := beginRecovery(t, ts, h, tok)
	raw := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("begin = %d: %s", resp.StatusCode, raw)
	}
	var opts struct {
		PublicKey struct {
			Challenge string `json:"challenge"`
			User      struct {
				Name string `json:"name"`
			} `json:"user"`
			Exclude []json.RawMessage `json:"excludeCredentials"`
		} `json:"publicKey"`
	}
	if err := json.Unmarshal([]byte(raw), &opts); err != nil {
		t.Fatalf("decode options: %v\n%s", err, raw)
	}
	if opts.PublicKey.Challenge == "" || opts.PublicKey.User.Name != "beginner" {
		t.Errorf("options = %+v; want a challenge for @beginner", opts.PublicKey)
	}
	if len(opts.PublicKey.Exclude) != 0 {
		t.Errorf("excludeCredentials lists %d passkeys; the broken one would block re-registering", len(opts.PublicKey.Exclude))
	}
	if !recoveryStillWorks(t, st, ctx, tok) {
		t.Error("begin spent the link; a cancelled prompt would leave it dead")
	}
}

// TestRecoverBeginRejectsDeadLink: an unknown or used link gets a plain message.
func TestRecoverBeginRejectsDeadLink(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	resp := beginRecovery(t, ts, h, "not-a-real-token")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "expired or has already been used") {
		t.Fatalf("begin = %d %s, want 400 with the dead-link message", resp.StatusCode, body)
	}
}

// TestRecoveryCeremonyIsolatedFromRegistration is the regression for sharing
// ceremony state: a recovery ceremony must not be finishable through
// /api/register/finish. That would add the passkey without spending the link or
// signing out other devices.
func TestRecoveryCeremonyIsolatedFromRegistration(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	u := passkeyOnlyUser(t, st, ctx, "isolated")
	tok := mintRecovery(t, st, ctx, u.ID)

	begin := beginRecovery(t, ts, h, tok)
	readBody(t, begin)
	sess := cookieByName(begin, "meshtender_session")
	if sess == nil {
		t.Fatal("begin set no session cookie to carry the ceremony")
	}

	resp := postBody(t, ts, h.auth, "/api/register/finish", "application/json", `{}`, sess)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "no registration in progress") {
		t.Fatalf("register/finish = %d %s; it must not see the recovery ceremony", resp.StatusCode, body)
	}
	if !recoveryStillWorks(t, st, ctx, tok) {
		t.Error("link spent by the wrong endpoint")
	}

	// And the other way round: a sign-up ceremony can't be finished as a recovery.
	signup := postBody(t, ts, h.auth, "/api/register/begin", "application/json", `{"username":"newperson"}`)
	readBody(t, signup)
	resp = postBody(t, ts, h.auth, "/api/recover/finish", "application/json", `{}`, cookieByName(signup, "meshtender_session"))
	body = readBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "no recovery in progress") {
		t.Fatalf("recover/finish after a sign-up begin = %d %s", resp.StatusCode, body)
	}
}
