package core

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// assertNoStore checks every Cache-Control field-line on the response, not just
// the first. scs Adds its own `no-cache="Set-Cookie"` line whenever it writes a
// session cookie, so an authenticated response legitimately carries two — RFC 9111
// combines them into one comma-separated list, and no-store is the strictest
// directive, so it governs. Header.Get would only ever see the first value, which
// would let a contradicting directive slip through unnoticed.
func assertNoStore(t *testing.T, resp *http.Response, what string) {
	t.Helper()
	values := resp.Header.Values("Cache-Control")
	joined := strings.Join(values, ", ")
	var hasNoStore bool
	for _, v := range values {
		for _, directive := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(directive), "no-store") {
				hasNoStore = true
			}
		}
	}
	if !hasNoStore {
		t.Errorf("%s Cache-Control = %q, want it to include no-store", what, joined)
		return
	}
	// Nothing may license storing the response after no-store said not to.
	for _, bad := range []string{"public", "max-age", "immutable", "s-maxage"} {
		if strings.Contains(strings.ToLower(joined), bad) {
			t.Errorf("%s Cache-Control has no-store but also %q: %q", what, bad, joined)
		}
	}
}

// fingerprintedAsset scrapes the content-hashed URL for a logical asset stem
// (e.g. "ui" → "/static/ui.7ef750bd.js") out of a rendered page, so the test uses
// the same URL a browser would without reaching into web's unexported manifest.
func fingerprintedAsset(t *testing.T, ts *httptest.Server, host, page, stem string) string {
	t.Helper()
	resp := do(t, ts, host, page)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", page, err)
	}
	re := regexp.MustCompile(`/static/` + regexp.QuoteMeta(stem) + `\.[0-9a-f]+\.js`)
	match := re.Find(body)
	if match == nil {
		t.Fatalf("no fingerprinted /static/%s.<hash>.js reference on %s", stem, page)
	}
	return string(match)
}

// TestNoStoreOnSessionSurfaces locks the response-caching policy across the split
// hosts:
//   - every session-bearing route on the app and auth hosts sends
//     Cache-Control: no-store
//   - fingerprinted static assets keep their one-year immutable caching, on every
//     host, because they're registered ahead of the session middleware
//   - the public root (discovery) pages stay cacheable — no no-store
//
// Regression for the pre-release audit finding that authenticated HTML carried no
// Cache-Control at all, making it heuristically cacheable: the back button would
// re-render a signed-in dashboard after sign-out, handing the next person on a
// shared machine the previous user's data.
func TestNoStoreOnSessionSurfaces(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	// Unauthenticated app-host routes still run the session middleware, so they
	// carry the header too — enough to prove the wiring without seeding data.
	t.Run("app host session routes", func(t *testing.T) {
		for _, path := range []string{"/", "/repeaters", "/orgs", "/nonexistent-page"} {
			resp := do(t, ts, h.app, path)
			resp.Body.Close()
			assertNoStore(t, resp, "app "+path)
		}
	})

	t.Run("auth host session routes", func(t *testing.T) {
		for _, path := range []string{"/login", "/signup", "/account", "/nonexistent-page"} {
			resp := do(t, ts, h.auth, path)
			resp.Body.Close()
			assertNoStore(t, resp, "auth "+path)
		}
	})

	t.Run("authenticated page", func(t *testing.T) {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookiejar: %v", err)
		}
		st, ctx, ts2, h2 := splitServer(t)
		seedSession(t, ts2, st, ctx, jar, "nostoreuser")
		req, _ := http.NewRequest(http.MethodGet, ts2.URL+"/repeaters", nil)
		req.Host = h2.app
		for _, c := range jar.Cookies(mustURL(t, ts2.URL)) {
			req.AddCookie(c)
		}
		resp, err := noRedirect().Do(req)
		if err != nil {
			t.Fatalf("get /repeaters: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/repeaters = %d, want 200 (session not established?)", resp.StatusCode)
		}
		assertNoStore(t, resp, "signed-in /repeaters")
	})

	// The whole point of scoping no-store to the session groups: immutable assets
	// must survive it, including on the app/auth hosts.
	t.Run("fingerprinted assets stay immutable", func(t *testing.T) {
		// Discover the hashed URL the way a browser does — from the rendered page —
		// rather than reaching into web's unexported manifest.
		asset := fingerprintedAsset(t, ts, h.root, "/", "ui")

		for _, host := range []struct{ name, h string }{{"app", h.app}, {"auth", h.auth}, {"root", h.root}} {
			resp := do(t, ts, host.h, asset)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s %s = %d, want 200", host.name, asset, resp.StatusCode)
			}
			got := resp.Header.Get("Cache-Control")
			if !strings.Contains(got, "immutable") {
				t.Errorf("%s %s Cache-Control = %q, want immutable", host.name, asset, got)
			}
			if strings.Contains(got, "no-store") {
				t.Errorf("%s %s got no-store, which would defeat asset caching: %q", host.name, asset, got)
			}
		}
	})

	t.Run("public root pages stay cacheable", func(t *testing.T) {
		for _, path := range []string{"/", "/orgs", "/docs"} {
			resp := do(t, ts, h.root, path)
			resp.Body.Close()
			if got := resp.Header.Get("Cache-Control"); strings.Contains(got, "no-store") {
				t.Errorf("root %s should stay cacheable, got Cache-Control = %q", path, got)
			}
		}
	})
}
