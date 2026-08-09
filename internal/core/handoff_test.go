package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/auth"
	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/identity"
	"github.com/MeshTender/MeshTender/internal/store"
)

const (
	testAuthHost = "auth.localhost"
	testAppHost  = "app.localhost"
	testRootHost = "localhost"
	testWWWHost  = "www.localhost"
)

// hostEnv holds the host:port for each surface of a split-host test server.
type hostEnv struct{ auth, app, root, www string }

// splitServer stands up a server in full split-host mode (root/www/auth/app)
// against the test database, returning the store and the per-surface host:port.
// testPassword is the fixture password for sign-up/sign-in tests. It must satisfy
// auth.MinPasswordLen — asserted below — so raising the floor doesn't quietly break
// every credential test with a confusing "password too short" redirect.
const testPassword = "correct-horse-battery-staple"

// TestFixturePasswordMeetsFloor keeps the fixture honest if the floor moves again.
func TestFixturePasswordMeetsFloor(t *testing.T) {
	t.Parallel()
	if len(testPassword) < auth.MinPasswordLen {
		t.Fatalf("testPassword is %d chars, below the %d-char floor — credential tests "+
			"would fail with a misleading validation error", len(testPassword), auth.MinPasswordLen)
	}
}

func splitServer(t *testing.T) (*store.Store, context.Context, *httptest.Server, hostEnv) {
	t.Helper()
	st, ctx, ts, h, _ := splitServerMail(t)
	return st, ctx, ts, h
}

// splitServerMail is splitServer plus the captured outbound mail. Every test server
// gets a capturing sender (never a real provider, and never the log sender — which
// would print recovery links into test output), with mail reported as configured so
// the recovery UI is exercised rather than hidden.
func splitServerMail(t *testing.T) (*store.Store, context.Context, *httptest.Server, hostEnv, *fakeSender) {
	t.Helper()
	return splitServerWithMail(t, true)
}

// splitServerNoMail builds a server with no mail provider configured, for the tests
// that assert the email features hide themselves rather than offering recovery that
// can't be delivered.
func splitServerNoMail(t *testing.T) (*store.Store, context.Context, *httptest.Server, hostEnv) {
	t.Helper()
	st, ctx, ts, h, _ := splitServerWithMail(t, false)
	return st, ctx, ts, h
}

func splitServerWithMail(t *testing.T, mailEnabled bool) (*store.Store, context.Context, *httptest.Server, hostEnv, *fakeSender) {
	t.Helper()
	return splitServerWith(t, mailEnabled, testConfig())
}

// splitServerWith is the base builder, taking the runtime config so a test can
// exercise a setting the deployment supplies (e.g. MESHTENDER_IMAGE_DIGEST)
// rather than only the defaults in testConfig.
func splitServerWith(t *testing.T, mailEnabled bool, cfg *config.Config) (*store.Store, context.Context, *httptest.Server, hostEnv, *fakeSender) {
	t.Helper()
	st, ctx := coreStore(t)

	masterKey := testMasterKey
	idSvc, _ := identity.LoadOrCreate(ctx, st, masterKey)
	sender := &fakeSender{}
	authSvc, err := auth.New(st, st.Pool(), auth.Config{
		RPID: "localhost", RPDisplayName: "test",
		RPOrigins: []string{"http://auth.localhost", "http://app.localhost"},
		AppHost:   testAppHost, AuthHost: testAuthHost, RootHost: testRootHost,
		Mail: sender, MailEnabled: mailEnabled,
	})
	if err != nil {
		t.Fatalf("auth: %v", err)
	}
	srv, err := NewServer(st, authSvc, idSvc, cfg)
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	port := mustURL(t, ts.URL).Port()
	hp := func(h string) string { return h + ":" + port }
	return st, ctx, ts, hostEnv{auth: hp(testAuthHost), app: hp(testAppHost), root: hp(testRootHost), www: hp(testWWWHost)}, sender
}

// seedSession creates a user and establishes an authenticated app-host session
// by redeeming a handoff code at /session/callback, storing the session cookie
// in jar. Integration tests use this instead of the auth-host sign-in UI.
func seedSession(t *testing.T, ts *httptest.Server, st *store.Store, ctx context.Context, jar http.CookieJar, username string) *store.User {
	t.Helper()
	u, err := st.CreateUser(ctx, username, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	loginID, err := st.CreateLogin(ctx, u.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}
	code, err := st.CreateAuthCode(ctx, u.ID, loginID, "/")
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/session/callback?code="+code+"&state=s1", nil)
	req.AddCookie(&http.Cookie{Name: "mt_state", Value: "s1"})
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("session callback: %v", err)
	}
	resp.Body.Close()
	return u
}

// noRedirect is a client that surfaces redirects instead of following them.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// do issues a request to ts with an explicit Host header (so the dispatcher
// routes by hostname) and optional cookies.
func do(t *testing.T, ts *httptest.Server, host, path string, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("do %s%s: %v", host, path, err)
	}
	return resp
}

// post issues a form POST with an explicit Host header and optional cookies.
func post(t *testing.T, ts *httptest.Server, host, path string, form url.Values, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("post %s%s: %v", host, path, err)
	}
	return resp
}

func cookieByName(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestRequireUserBouncesToAuthHost: an unauthenticated app-host request to a
// protected page redirects to the auth host's /login, carrying next and a state
// nonce that matches the host-only state cookie just set.
func TestRequireUserBouncesToAuthHost(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	resp := do(t, ts, h.app, "/repeaters")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Host != h.auth || loc.Path != "/login" {
		t.Fatalf("Location = %q, want auth host /login", resp.Header.Get("Location"))
	}
	if got := loc.Query().Get("next"); got != "/repeaters" {
		t.Fatalf("next = %q, want /repeaters", got)
	}
	state := loc.Query().Get("state")
	c := cookieByName(resp, "mt_state")
	if state == "" || c == nil || c.Value != state {
		t.Fatalf("state cookie %v must match state param %q", c, state)
	}
}

// TestAppHostLoginRedirectsToAuth: the credential UI does not live on the app
// host; /login there bounces to the auth host.
func TestAppHostLoginRedirectsToAuth(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	resp := do(t, ts, h.app, "/login")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Host != h.auth {
		t.Fatalf("Location host = %q, want %q", loc.Host, h.auth)
	}
}

// TestAuthHostServesLogin: the auth host renders the sign-in page in place.
func TestAuthHostServesLogin(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	resp := do(t, ts, h.auth, "/login")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestHostRouting checks each surface is served on its own host: the root host
// serves public marketing + org discovery, www redirects to root, and the app
// host's / and /orgs require auth (bounce to the auth host).
func TestHostRouting(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	t.Run("root serves landing", func(t *testing.T) {
		resp := do(t, ts, h.root, "/")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("root / = %d, want 200", resp.StatusCode)
		}
	})
	t.Run("root serves public org directory", func(t *testing.T) {
		resp := do(t, ts, h.root, "/orgs")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("root /orgs = %d, want 200", resp.StatusCode)
		}
	})
	t.Run("www redirects to root", func(t *testing.T) {
		resp := do(t, ts, h.www, "/orgs")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMovedPermanently {
			t.Fatalf("www /orgs = %d, want 301", resp.StatusCode)
		}
		if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Host != h.root || loc.Path != "/orgs" {
			t.Fatalf("www redirect = %q, want root /orgs", resp.Header.Get("Location"))
		}
	})
	t.Run("app root bounces anonymous to auth", func(t *testing.T) {
		resp := do(t, ts, h.app, "/")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("app / = %d, want 303", resp.StatusCode)
		}
		if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Host != h.auth {
			t.Fatalf("app / redirect host = %q, want auth", loc.Host)
		}
	})
	t.Run("app /orgs requires auth", func(t *testing.T) {
		resp := do(t, ts, h.app, "/orgs")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("app /orgs anon = %d, want 303 to auth", resp.StatusCode)
		}
	})
}

// TestSingleLogout verifies the single-revoke logout model: one POST to a host's
// /logout revokes the shared login row, dropping every host bound to it. An
// auth-host sign-in is handed off to the app so both sessions share one login row;
// logging out on the APP host then invalidates the AUTH host's SSO session on its
// next request — with no redirect chain between them. See docs/auth-cross-host.md.
func TestSingleLogout(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	// Sign up on the auth host: creates login row L and the auth SSO session, then
	// hands off to the app with a single-use code carrying L.
	su := post(t, ts, h.auth, "/signup/password", url.Values{"username": {"sharuser"}, "password": {testPassword}})
	su.Body.Close()
	sso := cookieByName(su, "meshtender_session")
	if sso == nil {
		t.Fatal("expected an SSO session from signup")
	}
	loc, _ := url.Parse(su.Header.Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("signup did not hand off to the app (Location %q)", su.Header.Get("Location"))
	}
	// Redeem the code on the app host, binding its session to the SAME row L.
	cb := do(t, ts, h.app, "/session/callback?code="+code+"&state=s1", &http.Cookie{Name: "mt_state", Value: "s1"})
	cb.Body.Close()
	appSess := cookieByName(cb, "meshtender_session")
	if appSess == nil {
		t.Fatal("expected an app session from the callback")
	}

	// Both live: auth /login re-hands-off (303) rather than showing the form.
	live := do(t, ts, h.auth, "/login?next=%2F&state=x", sso)
	live.Body.Close()
	if live.StatusCode != http.StatusSeeOther {
		t.Fatalf("pre-logout auth /login = %d, want 303 handoff (SSO live)", live.StatusCode)
	}

	// Log out on the APP host: it revokes L and lands on the public root — no hop
	// through the auth host.
	out := post(t, ts, h.app, "/logout", url.Values{}, appSess)
	out.Body.Close()
	if loc, _ := url.Parse(out.Header.Get("Location")); out.StatusCode != http.StatusSeeOther || loc.Hostname() != testRootHost {
		t.Fatalf("app logout = %d %q, want 303 to the root host", out.StatusCode, out.Header.Get("Location"))
	}

	// The AUTH host's SSO session is now dead too: /login shows the form (200)
	// instead of re-handing-off, because ValidateSession dropped the revoked login.
	after := do(t, ts, h.auth, "/login", sso)
	after.Body.Close()
	if after.StatusCode != http.StatusOK {
		t.Fatalf("post-logout auth /login = %d, want 200 (SSO cleared via shared-login revoke)", after.StatusCode)
	}
}

// TestAuthLogoutClearsSSO covers the auth host's own POST /logout: a visitor who
// authenticated on the auth host (e.g. for account settings) with no app session
// signs out there directly, revoking and clearing the SSO session.
func TestAuthLogoutClearsSSO(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	su := post(t, ts, h.auth, "/signup/password", url.Values{"username": {"ssoonly"}, "password": {testPassword}})
	su.Body.Close()
	sso := cookieByName(su, "meshtender_session")
	if sso == nil {
		t.Fatal("expected an SSO session from signup")
	}

	out := post(t, ts, h.auth, "/logout", url.Values{}, sso)
	out.Body.Close()
	if loc, _ := url.Parse(out.Header.Get("Location")); out.StatusCode != http.StatusSeeOther || loc.Hostname() != testRootHost {
		t.Fatalf("auth logout = %d %q, want 303 to the root host", out.StatusCode, out.Header.Get("Location"))
	}

	after := do(t, ts, h.auth, "/login", sso)
	after.Body.Close()
	if after.StatusCode != http.StatusOK {
		t.Fatalf("post-logout auth /login = %d, want 200 (SSO cleared)", after.StatusCode)
	}
}

// TestLogoutRejectsGet locks in that /logout is POST-only on both the auth and app
// hosts: a forged cross-site GET (e.g. <img src=".../logout">) must not sign
// anyone out. This is the rule (state-changing actions are POST) the old
// side-effecting GET /logout violated.
func TestLogoutRejectsGet(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	su := post(t, ts, h.auth, "/signup/password", url.Values{"username": {"getlogout"}, "password": {testPassword}})
	su.Body.Close()
	sso := cookieByName(su, "meshtender_session")
	if sso == nil {
		t.Fatal("expected an SSO session from signup")
	}

	for _, host := range []string{h.auth, h.app} {
		g := do(t, ts, host, "/logout", sso)
		g.Body.Close()
		if g.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("GET %s/logout = %d, want 405 (POST-only)", host, g.StatusCode)
		}
	}

	// The GET left the session untouched: auth /login still re-hands-off (SSO live).
	after := do(t, ts, h.auth, "/login?next=%2F&state=x", sso)
	after.Body.Close()
	if after.StatusCode != http.StatusSeeOther {
		t.Fatalf("auth /login after GET /logout = %d, want 303 (SSO still live)", after.StatusCode)
	}
}

// TestAccountOnAuthHost verifies account/credential management lives on the auth
// host: the app host no longer serves /account, the auth host guards it with the
// SSO session, and an SSO-less visit returns LOCALLY to /account after login
// (rather than handing off to the app).
func TestAccountOnAuthHost(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	t.Run("app host no longer serves /account", func(t *testing.T) {
		resp := do(t, ts, h.app, "/account")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("app /account = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("auth /account anon bounces to local login", func(t *testing.T) {
		resp := do(t, ts, h.auth, "/account")
		defer resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusSeeOther || loc.Path != "/login" || loc.Host != "" {
			t.Fatalf("auth /account anon = %d %q, want 303 to local /login", resp.StatusCode, resp.Header.Get("Location"))
		}
		// Completing login carries the auth-local flag, so it returns to /account
		// on the auth host — NOT a handoff to the app callback.
		sess := cookieByName(resp, "meshtender_session")
		if sess == nil {
			t.Fatal("expected a session cookie carrying the auth-local flag")
		}
		fin := post(t, ts, h.auth, "/signup/password",
			url.Values{"username": {"acctlocal"}, "password": {testPassword}}, sess)
		fin.Body.Close()
		if got := fin.Header.Get("Location"); got != "/account" {
			t.Fatalf("post-login redirect = %q, want local /account (no handoff)", got)
		}
	})

	t.Run("auth /account with SSO session renders", func(t *testing.T) {
		su := post(t, ts, h.auth, "/signup/password",
			url.Values{"username": {"acctsso"}, "password": {testPassword}})
		su.Body.Close()
		sso := cookieByName(su, "meshtender_session")
		if sso == nil {
			t.Fatal("expected an SSO session from signup")
		}
		resp := do(t, ts, h.auth, "/account", sso)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("auth /account with SSO = %d, want 200", resp.StatusCode)
		}
	})
}

// TestSessionCallback exercises the handoff redemption: a valid code + matching
// state establishes an app-host session; tampered state or an unknown code is
// rejected back to sign-in without a session.
func TestSessionCallback(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	appHost := h.app
	u, err := st.CreateUser(ctx, "callbackuser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Run("happy path establishes a session", func(t *testing.T) {
		loginID, _ := st.CreateLogin(ctx, u.ID)
		code, _ := st.CreateAuthCode(ctx, u.ID, loginID, "/repeaters")
		resp := do(t, ts, appHost, "/session/callback?code="+code+"&state=s1",
			&http.Cookie{Name: "mt_state", Value: "s1"})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303", resp.StatusCode)
		}
		// The callback sets the app session, then bounces through the root host's
		// identity beacon (carrying a fresh code) before landing on the app page.
		sess := cookieByName(resp, "meshtender_session")
		if sess == nil || sess.Value == "" {
			t.Fatalf("expected an app session cookie to be set")
		}
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if loc.Host != h.root || loc.Path != "/session/beacon" || loc.Query().Get("code") == "" {
			t.Fatalf("Location = %q, want root /session/beacon?code=...", resp.Header.Get("Location"))
		}

		// Driving the beacon on the root host drops a root identity cookie and
		// forwards back to the app's requested page.
		beacon := do(t, ts, h.root, "/session/beacon?code="+loc.Query().Get("code"))
		defer beacon.Body.Close()
		if beacon.StatusCode != http.StatusSeeOther {
			t.Fatalf("beacon status = %d, want 303", beacon.StatusCode)
		}
		if rootSess := cookieByName(beacon, "meshtender_session"); rootSess == nil || rootSess.Value == "" {
			t.Fatalf("expected a root identity cookie from the beacon")
		}
		if bloc, _ := url.Parse(beacon.Header.Get("Location")); bloc.Host != h.app || bloc.Path != "/repeaters" {
			t.Fatalf("beacon Location = %q, want app /repeaters", beacon.Header.Get("Location"))
		}

		// The app session must actually authenticate: the protected page loads.
		follow := do(t, ts, appHost, "/repeaters", sess)
		defer follow.Body.Close()
		if follow.StatusCode != http.StatusOK {
			t.Fatalf("authenticated /repeaters = %d, want 200", follow.StatusCode)
		}
	})

	t.Run("state mismatch is rejected", func(t *testing.T) {
		code, _ := st.CreateAuthCode(ctx, u.ID, "", "/repeaters")
		resp := do(t, ts, appHost, "/session/callback?code="+code+"&state=s1",
			&http.Cookie{Name: "mt_state", Value: "different"})
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") == "/repeaters" {
			t.Fatalf("status=%d loc=%q, want bounce to login", resp.StatusCode, resp.Header.Get("Location"))
		}
		if c := cookieByName(resp, "meshtender_session"); c != nil && c.Value != "" {
			t.Fatalf("no session should be set on state mismatch")
		}
		// The code must NOT have been consumed, since state failed first.
		if _, _, _, ok, _ := st.ConsumeAuthCode(ctx, code); !ok {
			t.Fatalf("code should remain valid after a state-mismatch rejection")
		}
	})

	t.Run("unknown code is rejected", func(t *testing.T) {
		resp := do(t, ts, appHost, "/session/callback?code=bogus&state=s1",
			&http.Cookie{Name: "mt_state", Value: "s1"})
		defer resp.Body.Close()
		if c := cookieByName(resp, "meshtender_session"); c != nil && c.Value != "" {
			t.Fatalf("no session should be set for an unknown code")
		}
	})
}
