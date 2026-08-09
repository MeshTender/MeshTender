package core

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestRootOrgPageIdentityAwareCTA is the end-to-end payoff: the public org page
// on the root (discovery) host renders a sign-in CTA for anonymous visitors, but
// once the identity beacon has dropped the root cookie, a member sees an "open in
// app" jump instead. See docs/auth-cross-host.md.
func TestRootOrgPageIdentityAwareCTA(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	u, err := st.CreateUser(ctx, "member", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Test Org", u.ID) // creator becomes an admin member
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Anonymous visitor on the root host: the sign-in CTA, never the app jump.
	anon := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug))
	if !strings.Contains(anon, "Sign in to join") {
		t.Fatalf("anonymous root org page missing the sign-in CTA")
	}
	if strings.Contains(anon, "Go to organization") {
		t.Fatalf("anonymous root org page should not show the app jump")
	}
	// The CTA must be consistent across every public tab, not just Home.
	for _, tab := range []string{"/repeaters", "/config"} {
		page := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug+tab))
		if !strings.Contains(page, "Sign in to join") || strings.Contains(page, "Go to organization") {
			t.Fatalf("anonymous root %s tab CTA inconsistent with Home", tab)
		}
	}

	// Drop a root identity cookie for the member via the beacon (as a fresh
	// sign-in on the app host would).
	loginID, err := st.CreateLogin(ctx, u.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}
	code, err := st.CreateAuthCode(ctx, u.ID, loginID, "/")
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	beacon := do(t, ts, h.root, "/session/beacon?code="+code)
	beacon.Body.Close()
	rootSess := cookieByName(beacon, "meshtender_session")
	if rootSess == nil || rootSess.Value == "" {
		t.Fatalf("beacon set no root identity cookie")
	}

	// The member now sees the app jump, not the sign-in CTA.
	member := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug, rootSess))
	if !strings.Contains(member, "Go to organization") {
		t.Fatalf("member root org page missing the app jump CTA")
	}
	if strings.Contains(member, "Sign in to join") {
		t.Fatalf("member root org page should not show the sign-in CTA")
	}
	// Same consistency for the member: "Go to organization" on every public tab.
	for _, tab := range []string{"/repeaters", "/config"} {
		page := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug+tab, rootSess))
		if !strings.Contains(page, "Go to organization") || strings.Contains(page, "Sign in to join") {
			t.Fatalf("member root %s tab CTA inconsistent with Home", tab)
		}
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
