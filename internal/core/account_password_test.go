package core

import (
	"net/http"
	"net/url"
	"testing"
)

// assertAccountError asserts a 303 back to /account carrying an error flash that
// contains want.
func assertAccountError(t *testing.T, resp *http.Response, want, label string) {
	t.Helper()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Path != "/account" {
		t.Fatalf("%s: %d %q, want 303 → /account", label, resp.StatusCode, resp.Header.Get("Location"))
	}
	if got := loc.Query().Get("error"); got != want {
		t.Fatalf("%s: error flash = %q, want %q", label, got, want)
	}
}

// The change-password form is current → new → confirm, and the confirmation is
// enforced by the server rather than only by the form: a mismatch must not slip
// through a direct post and leave someone with a password they mistyped twice
// over and can't reproduce.
func TestChangePasswordRequiresMatchingConfirmation(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	sso := authSSO(t, ts, h, "confirmpw")

	mismatch := post(t, ts, h.auth, "/account/password", url.Values{
		"current_password": {testPassword},
		"new_password":     {"a-brand-new-password"},
		"confirm_password": {"a-brand-new-passwrod"},
	}, sso)
	mismatch.Body.Close()
	assertAccountError(t, mismatch, "The new passwords don't match.", "mismatched confirmation")

	// The old password still works, i.e. nothing was written.
	login := post(t, ts, h.auth, "/login/password",
		url.Values{"username": {"confirmpw"}, "password": {testPassword}})
	login.Body.Close()
	if cookieByName(login, "meshtender_session") == nil {
		t.Fatal("a rejected change still altered the stored password")
	}

	// A missing confirmation is a mismatch too — no silent pass on an empty field.
	blank := post(t, ts, h.auth, "/account/password", url.Values{
		"current_password": {testPassword},
		"new_password":     {"a-brand-new-password"},
	}, sso)
	blank.Body.Close()
	assertAccountError(t, blank, "The new passwords don't match.", "omitted confirmation")

	// Matching values save.
	ok := post(t, ts, h.auth, "/account/password", url.Values{
		"current_password": {testPassword},
		"new_password":     {"a-brand-new-password"},
		"confirm_password": {"a-brand-new-password"},
	}, sso)
	ok.Body.Close()
	assertAccountOK(t, ok, "matching confirmation")

	u, err := st.GetUserByUsername(ctx, "confirmpw")
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if u.PasswordHash == nil {
		t.Fatal("password was cleared instead of changed")
	}
}

// Removing a password is the one account-page action that permanently narrows how
// you can get back in, so it takes a fresh passkey assertion — not just a live
// session. A direct post without one must be refused even though the account has
// a passkey and would otherwise satisfy the "keep a sign-in method" rule.
func TestRemovePasswordRequiresFreshPasskeyProof(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	sso := authSSO(t, ts, h, "removepw")

	u, err := st.GetUserByUsername(ctx, "removepw")
	if err != nil {
		t.Fatalf("load user: %v", err)
	}

	// Without a passkey the earlier rule fires first: there'd be no way to sign in.
	noKey := post(t, ts, h.auth, "/account/password", url.Values{"remove": {"1"}}, sso)
	noKey.Body.Close()
	assertAccountError(t, noKey,
		"Add a passkey before removing your password — it's your only way to sign in.",
		"remove with no passkey")

	if err := st.AddCredential(ctx, u.ID, []byte("cred-removepw"), []byte(`{}`), "laptop"); err != nil {
		t.Fatalf("add credential: %v", err)
	}

	// Now the account has a passkey, but this session has never asserted with it.
	// Completing the assertion needs a real authenticator, so the accepting path is
	// covered by the browser suite; here we pin that a session alone isn't enough.
	unverified := post(t, ts, h.auth, "/account/password", url.Values{"remove": {"1"}}, sso)
	unverified.Body.Close()
	assertAccountError(t, unverified,
		"Verify with your passkey to remove your password.",
		"remove without a fresh assertion")

	after, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if after.PasswordHash == nil {
		t.Fatal("the password was removed without a passkey assertion")
	}
}
