package core

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"github.com/MeshTender/MeshTender/internal/auth"
	"github.com/MeshTender/MeshTender/internal/identity"
)

// TestRevokedLoginLogsOut is the cross-host logout guarantee in miniature:
// revoking the login row (as Logout does on any host) invalidates the session on
// its very next request, because ValidateSession destroys sessions whose backing
// login is gone. See docs/auth-cross-host.md.
func TestRevokedLoginLogsOut(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)

	masterKey := testMasterKey
	idSvc, err := identity.LoadOrCreate(ctx, st, masterKey)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	authSvc, err := auth.New(st, st.Pool(), testAuthConfig())
	if err != nil {
		t.Fatalf("auth service: %v", err)
	}
	srv, err := NewServer(st, authSvc, idSvc, testConfig())
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	jar, _ := cookiejar.New(nil)
	user := seedSession(t, ts, st, ctx, jar, "revuser")
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// The seeded session authenticates the protected page.
	resp, err := client.Get(ts.URL + "/repeaters")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("before revoke: /repeaters = %d, want 200", resp.StatusCode)
	}

	// Revoking the login (a logout elsewhere, or "log out everywhere") drops the
	// session to anonymous on the next request without touching its cookie.
	if err := st.RevokeAllUserLogins(ctx, user.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	resp2, err := client.Get(ts.URL + "/repeaters")
	if err != nil {
		t.Fatalf("get after revoke: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		t.Fatalf("after revoke: /repeaters = 200, want a redirect to sign in")
	}
}
