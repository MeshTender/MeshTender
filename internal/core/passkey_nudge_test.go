package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
)

// dashboardHTML fetches the signed-in dashboard for the given session cookies.
func dashboardHTML(t *testing.T, ts *httptest.Server, host string, cookies []*http.Cookie) string {
	t.Helper()
	resp := do(t, ts, host, "/", cookies...)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dashboard = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read dashboard: %v", err)
	}
	return string(body)
}

// TestDashboardNudgesPasswordUsersTowardPasskeys covers the follow-up that didn't
// exist before: someone who signs up with a password is never asked about passkeys
// again, so the getting-started checklist carries the ask.
//
// The step is listed for everyone (a passkey holder sees it already satisfied, which
// reads as progress rather than a scold) and is never blocking — it's a checklist
// item, so it disappears on its own once acted on.
func TestDashboardNudgesPasswordUsersTowardPasskeys(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	t.Run("account without a passkey sees it outstanding", func(t *testing.T) {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookiejar: %v", err)
		}
		seedSession(t, ts, st, ctx, jar, "nopasskey")
		page := dashboardHTML(t, ts, h.app, jar.Cookies(mustURL(t, ts.URL)))

		if !strings.Contains(page, "Add a passkey") {
			t.Fatal("dashboard doesn't offer the passkey step at all")
		}
		// The CTA only renders while a step is pending, so its presence is what proves
		// the step is outstanding rather than merely listed.
		if !strings.Contains(page, "Add passkey") {
			t.Error("passkey step has no call to action, so it reads as already done")
		}
	})

	t.Run("account with a passkey sees it satisfied", func(t *testing.T) {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookiejar: %v", err)
		}
		user := seedSession(t, ts, st, ctx, jar, "haspasskey")
		blob, err := json.Marshal(webauthn.Credential{ID: []byte("nudge-cred")})
		if err != nil {
			t.Fatalf("marshal credential: %v", err)
		}
		if err := st.AddCredential(ctx, user.ID, []byte("nudge-cred"), blob, "laptop"); err != nil {
			t.Fatalf("add credential: %v", err)
		}

		page := dashboardHTML(t, ts, h.app, jar.Cookies(mustURL(t, ts.URL)))
		if !strings.Contains(page, "Add a passkey") {
			t.Fatal("passkey step vanished for a passkey holder; it should show as done")
		}
		if strings.Contains(page, "Add passkey") {
			t.Error("passkey step still shows its CTA for someone who already has one")
		}
	})
}

// TestSignupKeepsPasskeyPrimary pins the signup form's choice architecture: the
// passkey path leads, and the password path stays available. Both halves matter — the
// goal is a nudge, not a wall, so this guards against a future change tipping it
// either way (burying passkeys, or removing the password option for people who have
// no platform authenticator).
func TestSignupKeepsPasskeyPrimary(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	resp := do(t, ts, h.auth, "/signup")
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read signup: %v", err)
	}
	page := string(body)

	passkeyAt := strings.Index(page, "Create account with a passkey")
	passwordAt := strings.Index(page, "Create account with password")
	if passkeyAt < 0 {
		t.Fatal("signup form lost its passkey option")
	}
	if passwordAt < 0 {
		t.Fatal("the password option was removed; it must stay available")
	}
	if passkeyAt > passwordAt {
		t.Error("the password button precedes the passkey button; passkeys should lead")
	}
}
