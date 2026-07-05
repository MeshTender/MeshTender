package core

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// Black-box coverage for the auth host's non-visual POST endpoints: password
// sign-in/sign-up and the account-management forms. WebAuthn API ceremonies
// (#20–25) need a virtual authenticator and are left to browser/manual checks.

// authSSO signs up a new user via the password form and returns its live SSO
// session cookie — the automatable way to mint an authenticated auth-host session.
func authSSO(t *testing.T, ts *httptest.Server, h hostEnv, username string) *http.Cookie {
	t.Helper()
	resp := post(t, ts, h.auth, "/signup/password", url.Values{"username": {username}, "password": {"supersecret"}})
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
	ok := post(t, ts, h.auth, "/signup/password", url.Values{"username": {"pwuser"}, "password": {"supersecret"}})
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
	good := post(t, ts, h.auth, "/login/password", url.Values{"username": {"pwuser"}, "password": {"supersecret"}})
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
		url.Values{"new_password": {"anothersecret"}, "current_password": {"supersecret"}}, sso)
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
