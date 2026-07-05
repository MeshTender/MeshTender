package core

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// These black-box tests cover the non-visual "plumbing" endpoints from the
// endpoint inventory (docs/endpoint-inventory.md): health, static assets, and the
// pure host/redirect behaviors. They use the splitServer harness (real three-host
// server + real Postgres) and assert status/redirect Location rather than any
// rendered UI.

// #1 /healthz — returns "ok" on every surface.
func TestHealthzEndpoint(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	for _, host := range []string{h.app, h.auth, h.root} {
		resp := do(t, ts, host, "/healthz")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "ok" {
			t.Fatalf("%s/healthz = %d %q, want 200 \"ok\"", host, resp.StatusCode, body)
		}
	}
}

// TestHealthzReportsDBDown: /healthz is a readiness probe — when the database is
// unreachable it must fail (503) rather than report healthy. (A cookieless
// request touches no session DB rows, so the ping is what fails.)
func TestHealthzReportsDBDown(t *testing.T) {
	t.Parallel()
	st, _, ts, h := splitServer(t)

	// Healthy: 200.
	resp := do(t, ts, h.app, "/healthz")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthy /healthz = %d, want 200", resp.StatusCode)
	}

	// Take the database down; the readiness ping must now fail closed.
	st.Close()
	down := do(t, ts, h.app, "/healthz")
	down.Body.Close()
	if down.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("db-down /healthz = %d, want 503", down.StatusCode)
	}
}

// #2 /static/* — serves an embedded asset.
func TestStaticAssetEndpoint(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	resp := do(t, ts, h.app, "/static/ui.js")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/static/ui.js = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("/static/ui.js Content-Type = %q, want a javascript type", ct)
	}
	if len(body) == 0 {
		t.Fatal("/static/ui.js served an empty body")
	}
}

// #15 auth host `/` — bare visits 303 to the sign-in page.
func TestAuthRootRedirectsToLogin(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	resp := do(t, ts, h.auth, "/")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("auth / = %d, want 303", resp.StatusCode)
	}
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Path != "/login" {
		t.Fatalf("auth / → %q, want /login", resp.Header.Get("Location"))
	}
}

// #35 app host `/signup` — starts the signup handoff, bouncing to the auth host.
func TestAppSignupRedirectsToAuth(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)
	resp := do(t, ts, h.app, "/signup")
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("app /signup = %d, want 303", resp.StatusCode)
	}
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Host != h.auth || loc.Path != "/signup" {
		t.Fatalf("app /signup → %q, want auth host /signup", resp.Header.Get("Location"))
	}
}

// #97 custom org domain — a verified domain serves the org's public page at `/`
// and 302-redirects every other path to the app host.
func TestCustomDomainRedirect(t *testing.T) {
	t.Parallel()
	st, ctx, ts, _ := splitServer(t)

	owner, err := st.CreateUser(ctx, "domainowner", "")
	if err != nil {
		t.Fatal(err)
	}
	org, err := st.CreateOrg(ctx, "Domain Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	const customHost = "mesh.example.org"
	dom, err := st.CreateOrgDomain(ctx, org.ID, customHost)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.MarkOrgDomainVerified(ctx, org.ID, dom.ID); err != nil {
		t.Fatal(err)
	}

	// Non-root path on the custom host → 302 to the same path on the app host.
	resp := do(t, ts, customHost, "/repeaters/abc")
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("custom-domain /repeaters/abc = %d, want 302", resp.StatusCode)
	}
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Hostname() != testAppHost || loc.Path != "/repeaters/abc" {
		t.Fatalf("custom-domain redirect = %q, want app host /repeaters/abc", resp.Header.Get("Location"))
	}
	// (The custom-domain `/` org page — inventory #96 — is a rendered page left to
	// manual/browser verification.)
}
