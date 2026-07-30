package core

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/store"
)

// postFetchSite issues a form POST carrying an explicit Sec-Fetch-Site header
// (omitted entirely when fetchSite is ""), so a test can imitate what a browser
// would report about the request's initiator.
func postFetchSite(t *testing.T, ts *httptest.Server, host, path, fetchSite string, form url.Values, cookies ...*http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("post %s%s: %v", host, path, err)
	}
	return resp
}

// TestCrossSiteWritesBlocked covers the second layer of CSRF defense (the first
// being the session cookie's SameSite=Lax):
//   - a state-changing POST reporting Sec-Fetch-Site: cross-site is refused 403,
//     and — the part that matters — its side effect does NOT happen
//   - same-origin, same-site, none, and a missing header are all allowed through,
//     so current browsers, sibling hosts, and pre-2020 / non-browser clients keep
//     working
//   - safe methods are never blocked, since cross-site GET navigation is normal
//
// Regression for audit finding S1.
func TestCrossSiteWritesBlocked(t *testing.T) {
	t.Parallel()

	// signedIn spins up a server with an authenticated app-host session and returns
	// what's needed to drive POSTs as that user. Each subtest gets its own so a
	// successful logout in one can't affect another.
	signedIn := func(t *testing.T) (*httptest.Server, hostEnv, *store.Store, []*http.Cookie) {
		t.Helper()
		st, ctx, ts, h := splitServer(t)
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookiejar: %v", err)
		}
		seedSession(t, ts, st, ctx, jar, "csrfuser-"+strings.ToLower(t.Name()[strings.LastIndex(t.Name(), "/")+1:]))
		return ts, h, st, jar.Cookies(mustURL(t, ts.URL))
	}

	// Logout is the cleanest probe: it's a real state change (revokes the login row)
	// whose effect is observable from a later request.
	t.Run("cross-site POST is blocked and has no effect", func(t *testing.T) {
		ts, h, _, cookies := signedIn(t)

		// Confirm the session works before the attempt.
		before := do(t, ts, h.app, "/repeaters", cookies...)
		before.Body.Close()
		if before.StatusCode != http.StatusOK {
			t.Fatalf("precondition: /repeaters = %d, want 200", before.StatusCode)
		}

		resp := postFetchSite(t, ts, h.app, "/logout", "cross-site", url.Values{}, cookies...)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-site POST /logout = %d, want 403", resp.StatusCode)
		}

		// The whole point: the logout must not have happened.
		after := do(t, ts, h.app, "/repeaters", cookies...)
		after.Body.Close()
		if after.StatusCode != http.StatusOK {
			t.Fatalf("session was destroyed by a blocked cross-site logout: /repeaters = %d, want 200",
				after.StatusCode)
		}
	})

	// Everything a legitimate client can report must still get through. A successful
	// logout answers 303 to the root host.
	for _, tc := range []struct{ name, fetchSite string }{
		{"same-origin", "same-origin"},
		{"same-site", "same-site"},
		{"none", "none"},
		{"missing header (old or non-browser client)", ""},
		{"unrecognized value", "future-value"},
	} {
		t.Run("allowed: "+tc.name, func(t *testing.T) {
			ts, h, _, cookies := signedIn(t)
			resp := postFetchSite(t, ts, h.app, "/logout", tc.fetchSite, url.Values{}, cookies...)
			resp.Body.Close()
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("POST /logout with Sec-Fetch-Site=%q = %d, want 303", tc.fetchSite, resp.StatusCode)
			}
		})
	}

	// Case shouldn't matter; browsers send lowercase but don't rely on it.
	t.Run("blocking is case-insensitive", func(t *testing.T) {
		ts, h, _, cookies := signedIn(t)
		resp := postFetchSite(t, ts, h.app, "/logout", "Cross-Site", url.Values{}, cookies...)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("POST /logout with Sec-Fetch-Site=Cross-Site = %d, want 403", resp.StatusCode)
		}
	})

	// The check must apply on every surface, not just the app host.
	t.Run("applies to the auth host", func(t *testing.T) {
		_, _, ts, h := splitServer(t)
		resp := postFetchSite(t, ts, h.auth, "/login/password", "cross-site",
			url.Values{"username": {"someone"}, "password": {"whatever"}})
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-site POST /login/password = %d, want 403", resp.StatusCode)
		}
	})

	// Cross-site GET navigation is how people arrive from a link — never blocked.
	t.Run("safe methods are not blocked cross-site", func(t *testing.T) {
		_, _, ts, h := splitServer(t)
		for _, page := range []struct{ host, path string }{
			{h.root, "/"},
			{h.root, "/orgs"},
			{h.auth, "/login"},
		} {
			req, _ := http.NewRequest(http.MethodGet, ts.URL+page.path, nil)
			req.Host = page.host
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			resp, err := noRedirect().Do(req)
			if err != nil {
				t.Fatalf("get %s: %v", page.path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("cross-site GET %s%s = %d, want 200", page.host, page.path, resp.StatusCode)
			}
		}
	})
}
