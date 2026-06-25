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
	if strings.Contains(anon, "Open in app") {
		t.Fatalf("anonymous root org page should not show the app jump")
	}

	// Drop a root identity cookie for the member via the beacon (as a fresh
	// sign-in on the app host would).
	loginID, _ := st.CreateLogin(ctx, u.ID)
	code, _ := st.CreateAuthCode(ctx, u.ID, loginID, "/")
	beacon := do(t, ts, h.root, "/session/beacon?code="+code)
	beacon.Body.Close()
	rootSess := cookieByName(beacon, "meshtender_session")
	if rootSess == nil || rootSess.Value == "" {
		t.Fatalf("beacon set no root identity cookie")
	}

	// The member now sees the app jump, not the sign-in CTA.
	member := readBody(t, do(t, ts, h.root, "/orgs/"+org.Slug, rootSess))
	if !strings.Contains(member, "Open in app") {
		t.Fatalf("member root org page missing the app jump CTA")
	}
	if strings.Contains(member, "Sign in to join") {
		t.Fatalf("member root org page should not show the sign-in CTA")
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
