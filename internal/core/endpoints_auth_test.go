package core

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/jleight/meshtender/internal/store"
)

// Black-box coverage for the auth host's non-visual POST endpoints: password
// sign-in/sign-up and the account-management forms. WebAuthn API ceremonies
// (#20–25) need a virtual authenticator and are left to browser/manual checks.

// authSSO signs up a new user via the password form and returns its live SSO
// session cookie — the automatable way to mint an authenticated auth-host session.
func authSSO(t *testing.T, ts *httptest.Server, h hostEnv, username string) *http.Cookie {
	t.Helper()
	resp := post(t, ts, h.auth, "/signup/password", url.Values{"username": {username}, "password": {testPassword}})
	resp.Body.Close()
	c := cookieByName(resp, "meshtender_session")
	if c == nil {
		t.Fatalf("no SSO session cookie after signup for %q", username)
	}
	return c
}

// #18 POST /login/password and #19 POST /signup/password.
func TestPasswordSignupAndLogin(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	// #19 signup, invalid (short password) → back to the form with an error.
	bad := post(t, ts, h.auth, "/signup/password", url.Values{"username": {"pwuser"}, "password": {"short"}})
	bad.Body.Close()
	if loc, _ := url.Parse(bad.Header.Get("Location")); bad.StatusCode != http.StatusSeeOther || loc.Path != "/signup" || loc.Query().Get("error") == "" {
		t.Fatalf("bad signup = %d %q, want 303 /signup?error", bad.StatusCode, bad.Header.Get("Location"))
	}

	// #19 signup, valid → creates the account + an SSO session and hands off.
	ok := post(t, ts, h.auth, "/signup/password", url.Values{"username": {"pwuser"}, "password": {testPassword}})
	ok.Body.Close()
	if ok.StatusCode != http.StatusSeeOther || cookieByName(ok, "meshtender_session") == nil {
		t.Fatalf("valid signup = %d, session=%v; want 303 + session cookie", ok.StatusCode, cookieByName(ok, "meshtender_session"))
	}
	if _, err := st.GetUserByUsername(ctx, "pwuser"); err != nil {
		t.Fatalf("signup did not create the user: %v", err)
	}

	// #18 login, wrong password → back to the form with an error.
	wrong := post(t, ts, h.auth, "/login/password", url.Values{"username": {"pwuser"}, "password": {"wrongpass"}})
	wrong.Body.Close()
	if loc, _ := url.Parse(wrong.Header.Get("Location")); wrong.StatusCode != http.StatusSeeOther || loc.Path != "/login" || loc.Query().Get("error") == "" {
		t.Fatalf("wrong login = %d %q, want 303 /login?error", wrong.StatusCode, wrong.Header.Get("Location"))
	}

	// #18 login, correct credentials → session + handoff.
	good := post(t, ts, h.auth, "/login/password", url.Values{"username": {"pwuser"}, "password": {testPassword}})
	good.Body.Close()
	if good.StatusCode != http.StatusSeeOther || cookieByName(good, "meshtender_session") == nil {
		t.Fatalf("good login = %d, session=%v; want 303 + session cookie", good.StatusCode, cookieByName(good, "meshtender_session"))
	}
}

// TestWebAuthnBeginRateLimited: the user-initiated passkey "begin" ceremonies
// share the credential-attempt limiter. register/begin persists an account row,
// so an unthrottled loop would flood the users table and squat usernames; this
// is a regression guard for the pre-release audit finding that they were
// unlimited. A malformed body 400s in the handler, but the limiter middleware
// runs first — which is all this exercises — so after the burst we get 429.
func TestWebAuthnBeginRateLimited(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/api/register/begin", "/api/login/begin"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			_, _, ts, h := splitServer(t)

			var got429, gotAllowed bool
			for i := 0; i < 15; i++ {
				resp := post(t, ts, h.auth, path, url.Values{})
				resp.Body.Close()
				if resp.StatusCode == http.StatusTooManyRequests {
					got429 = true
				} else {
					gotAllowed = true
				}
			}
			if !gotAllowed {
				t.Fatalf("%s: every request throttled; burst should allow some through", path)
			}
			if !got429 {
				t.Fatalf("%s: never returned 429; the rate limiter is not wired", path)
			}
		})
	}
}

// TestPasskeyBeginDefersAccount: a logged-out register/begin issues a challenge
// but does NOT persist an account — the row is only written once a credential is
// verified at finish (which needs a real authenticator, covered by the e2e
// suite). Regression for the pre-release audit finding + README follow-up that an
// abandoned passkey signup left an orphan account squatting the username.
func TestPasskeyBeginDefersAccount(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	resp := jsonPost(t, ts.URL+"/api/register/begin", h.auth, `{"username":"ghost","displayName":"Ghost"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register/begin = %d, want 200 (challenge issued)", resp.StatusCode)
	}
	if _, err := st.GetUserByUsername(ctx, "ghost"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("begin persisted an account for an unfinished ceremony (err=%v)", err)
	}

	// A name already taken is still rejected up front.
	authSSO(t, ts, h, "taken")
	dup := jsonPost(t, ts.URL+"/api/register/begin", h.auth, `{"username":"taken"}`)
	dup.Body.Close()
	if dup.StatusCode != http.StatusConflict {
		t.Fatalf("register/begin for taken username = %d, want 409", dup.StatusCode)
	}
}

// TestPasswordNoMaxLengthAndLegacyMigration: a password well past bcrypt's
// 72-byte limit works end to end (pre-hashing removes the ceiling), and a stored
// legacy raw-bcrypt hash still authenticates and is transparently upgraded to the
// pre-hash scheme on login.
func TestPasswordNoMaxLengthAndLegacyMigration(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	// A 200-char password: sign up, then sign in with it.
	longPw := strings.Repeat("MeshTender-", 20) // 220 chars
	up := post(t, ts, h.auth, "/signup/password", url.Values{"username": {"longpw"}, "password": {longPw}})
	up.Body.Close()
	if up.StatusCode != http.StatusSeeOther || cookieByName(up, "meshtender_session") == nil {
		t.Fatalf("long-password signup = %d, want 303 + session", up.StatusCode)
	}
	in := post(t, ts, h.auth, "/login/password", url.Values{"username": {"longpw"}, "password": {longPw}})
	in.Body.Close()
	if in.StatusCode != http.StatusSeeOther || cookieByName(in, "meshtender_session") == nil {
		t.Fatalf("long-password login = %d, want 303 + session", in.StatusCode)
	}

	// Plant a legacy (raw-bcrypt) hash and confirm login works and upgrades it.
	legacyUser, err := st.CreateUser(ctx, "legacypw", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	raw, _ := bcrypt.GenerateFromPassword([]byte("old-password"), bcrypt.DefaultCost)
	if err := st.SetPassword(ctx, legacyUser.ID, string(raw)); err != nil {
		t.Fatalf("set legacy hash: %v", err)
	}
	legacyLogin := post(t, ts, h.auth, "/login/password", url.Values{"username": {"legacypw"}, "password": {"old-password"}})
	legacyLogin.Body.Close()
	if legacyLogin.StatusCode != http.StatusSeeOther || cookieByName(legacyLogin, "meshtender_session") == nil {
		t.Fatalf("legacy login = %d, want 303 + session", legacyLogin.StatusCode)
	}
	after, err := st.GetUserByUsername(ctx, "legacypw")
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if after.PasswordHash == nil || *after.PasswordHash == string(raw) {
		t.Fatal("legacy hash was not upgraded on login")
	}
}

// TestLoginStampsLastLogin: a fresh account has no last-login stamp until it
// actually authenticates. Signing in populates last_login_at.
func TestLoginStampsLastLogin(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	// Created directly (never signed in) → no stamp.
	if _, err := st.CreateUser(ctx, "neverin", ""); err != nil {
		t.Fatalf("create user: %v", err)
	}
	fresh, err := st.GetUserByUsername(ctx, "neverin")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if fresh.LastLoginAt != nil {
		t.Fatalf("never-logged-in account LastLoginAt = %v, want nil", fresh.LastLoginAt)
	}

	// Signing up authenticates the user, which stamps last_login_at.
	authSSO(t, ts, h, "stamped")
	after, err := st.GetUserByUsername(ctx, "stamped")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.LastLoginAt == nil {
		t.Fatal("LastLoginAt is nil after signing in, want a timestamp")
	}
}

// TestAdminUsersShowsLastLogin renders the admin users page and confirms the
// last-login column shows — including "never" for an account that hasn't signed
// in (exercising the nil-timestamp branch, which must not panic the template).
func TestAdminUsersShowsLastLogin(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "adminview")
	if err := st.SetCapabilities(ctx, admin.ID, true, true); err != nil {
		t.Fatalf("set caps: %v", err)
	}
	if _, err := st.CreateUser(ctx, "ghostuser", ""); err != nil {
		t.Fatalf("create user: %v", err)
	}

	resp := do(t, ts, h.app, "/admin/users", sess)
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/users = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(body, "Last login") {
		t.Fatal("admin users page missing the Last login column")
	}
	if !strings.Contains(body, "never") {
		t.Fatal("expected 'never' for the account that hasn't logged in")
	}
}

// assertAccountOK asserts a 303 back to /account carrying a success ("ok") flash.
func assertAccountOK(t *testing.T, resp *http.Response, label string) {
	t.Helper()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Path != "/account" {
		t.Fatalf("%s: %d %q, want 303 → /account", label, resp.StatusCode, resp.Header.Get("Location"))
	}
	if loc.Query().Get("ok") == "" {
		t.Fatalf("%s: %q, want an ok flash", label, resp.Header.Get("Location"))
	}
}

// #29 profile, #100 profile-fields, #101 links, #30 password, #28 username.
func TestAccountProfilePosts(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	sso := authSSO(t, ts, h, "acctuser")

	profile := post(t, ts, h.auth, "/account/profile", url.Values{"display_name": {"Ada Lovelace"}}, sso)
	profile.Body.Close()
	assertAccountOK(t, profile, "profile")

	fields := post(t, ts, h.auth, "/account/profile-fields",
		url.Values{"bio": {"hi there"}, "location": {"NYC"}, "callsign": {"W1AW"}}, sso)
	fields.Body.Close()
	assertAccountOK(t, fields, "profile-fields")

	links := post(t, ts, h.auth, "/account/links",
		url.Values{"link_platform": {"website"}, "link_label": {"Site"}, "link_url": {"https://example.com"}}, sso)
	links.Body.Close()
	assertAccountOK(t, links, "links")

	pw := post(t, ts, h.auth, "/account/password",
		url.Values{"new_password": {"anothersecret"}, "current_password": {testPassword}}, sso)
	pw.Body.Close()
	assertAccountOK(t, pw, "password")

	// Username change last (it changes the identity). A fresh account has no prior
	// self-change, so the 30-day cooldown doesn't apply.
	uname := post(t, ts, h.auth, "/account/username", url.Values{"username": {"renamed-acct"}}, sso)
	uname.Body.Close()
	assertAccountOK(t, uname, "username")
	if _, err := st.GetUserByUsername(ctx, "renamed-acct"); err != nil {
		t.Fatalf("username was not changed: %v", err)
	}
}

// #31 passkey rename, #32 passkey delete. The user has no passkeys, but the
// endpoints must still resolve cleanly back to /account (they don't render).
func TestAccountPasskeyPosts(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	sso := authSSO(t, ts, h, "pkuser")

	rn := post(t, ts, h.auth, "/account/passkeys/rename",
		url.Values{"credential_id": {"1"}, "name": {"Phone"}}, sso)
	rn.Body.Close()
	if loc, _ := url.Parse(rn.Header.Get("Location")); rn.StatusCode != http.StatusSeeOther || loc.Path != "/account" {
		t.Fatalf("passkey rename = %d %q, want 303 → /account", rn.StatusCode, rn.Header.Get("Location"))
	}

	// The user still has a password, so removing a (nonexistent) passkey is allowed.
	del := post(t, ts, h.auth, "/account/passkeys/delete", url.Values{"credential_id": {"1"}}, sso)
	del.Body.Close()
	if loc, _ := url.Parse(del.Header.Get("Location")); del.StatusCode != http.StatusSeeOther || loc.Path != "/account" {
		t.Fatalf("passkey delete = %d %q, want 303 → /account", del.StatusCode, del.Header.Get("Location"))
	}
}
