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

// TestLoginFinishDoesNotLeakWebAuthnError drives a passkey assertion to failure
// and asserts the client sees only a generic message — the go-webauthn internal
// detail must be logged server-side, not returned. Regression for the error-leak
// audit (auth/handlers.go LoginFinish).
func TestLoginFinishDoesNotLeakWebAuthnError(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)

	user, err := st.CreateUser(ctx, "wauser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	// A minimal stored credential so LoginBegin has something to challenge; its key
	// material is never exercised because the assertion body below fails to parse.
	blob, err := json.Marshal(webauthn.Credential{ID: []byte("cred-1")})
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	if err := st.AddCredential(ctx, user.ID, []byte("cred-1"), blob, "key"); err != nil {
		t.Fatalf("add credential: %v", err)
	}

	// One client with a cookie jar so the ceremony stashed at /begin is present at
	// /finish (both carry the same session cookie).
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	begin := authJSON(t, client, ts, h.auth, "/api/login/begin", `{"username":"wauser"}`)
	begin.Body.Close()
	if begin.StatusCode != http.StatusOK {
		t.Fatalf("login begin status = %d, want 200", begin.StatusCode)
	}

	// A malformed assertion body makes FinishLogin fail. The response must be the
	// generic message only.
	finish := authJSON(t, client, ts, h.auth, "/api/login/finish", `{}`)
	body, _ := io.ReadAll(finish.Body)
	finish.Body.Close()
	if finish.StatusCode != http.StatusUnauthorized {
		t.Fatalf("login finish status = %d, want 401 (body %s)", finish.StatusCode, body)
	}
	got := strings.TrimSpace(string(body))
	if got != `{"error":"login failed"}` {
		t.Fatalf("login finish body = %s, want generic {\"error\":\"login failed\"} with no leaked detail", got)
	}
}

// authJSON POSTs a JSON body to the given host/path using the provided client.
func authJSON(t *testing.T, client *http.Client, ts *httptest.Server, host, path, jsonBody string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(jsonBody))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("post %s%s: %v", host, path, err)
	}
	return resp
}
