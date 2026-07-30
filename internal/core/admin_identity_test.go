package core

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/store"
)

// identityAdmin spins up a server with a signed-in admin holding CapManageUsers.
func identityAdmin(t *testing.T, username string) (*store.Store, context.Context, *httptest.Server, hostEnv, []*http.Cookie) {
	t.Helper()
	st, ctx, ts, h := splitServer(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	u := seedSession(t, ts, st, ctx, jar, username)
	if err := st.SetCapabilities(ctx, u.ID, true, false); err != nil {
		t.Fatalf("grant capabilities: %v", err)
	}
	return st, ctx, ts, h, jar.Cookies(mustURL(t, ts.URL))
}

// TestIdentityBackupRequiresAdmin: the page and both actions must be invisible without
// the capability. A 404 (not a 403) matches how the rest of the admin area hides itself.
func TestIdentityBackupRequiresAdmin(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	u := seedSession(t, ts, st, ctx, jar, "identitynobody")
	// CreateUser bootstraps the FIRST account in a database to full capabilities, so
	// this has to be cleared explicitly — otherwise the fixture is an admin and the
	// test passes for the wrong reason.
	if err := st.SetCapabilities(ctx, u.ID, false, false); err != nil {
		t.Fatalf("clear capabilities: %v", err)
	}
	cookies := jar.Cookies(mustURL(t, ts.URL))

	resp := do(t, ts, h.app, "/admin/identity", cookies...)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /admin/identity as a non-admin = %d, want 404", resp.StatusCode)
	}
	for _, path := range []string{"/admin/identity/export", "/admin/identity/restore"} {
		resp := post(t, ts, h.app, path, url.Values{}, cookies...)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("POST %s as a non-admin = %d, want 404", path, resp.StatusCode)
		}
	}
}

// TestIdentityBackupNotShownOnPageLoad: the backup value appears only in response to an
// explicit POST, so merely opening the admin area doesn't put it on screen.
func TestIdentityBackupNotShownOnPageLoad(t *testing.T) {
	t.Parallel()
	_, _, ts, h, cookies := identityAdmin(t, "identityviewer")

	resp := do(t, ts, h.app, "/admin/identity", cookies...)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	page := string(body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /admin/identity = %d, want 200", resp.StatusCode)
	}
	// Match the rendered value's own element, not the version string — that appears
	// legitimately in the restore field's placeholder and in the help text.
	if strings.Contains(page, `data-testid="identity-backup"`) {
		t.Error("the page rendered the backup value on a plain GET")
	}
	// The public key is not secret and identifies the deployment, so it should show.
	if !strings.Contains(page, `data-testid="identity-pubkey"`) {
		t.Error("the page doesn't show which identity this server holds")
	}
}

// TestIdentityBackupExportRoundTrip drives the real flow: export via the UI, then paste
// it back. Re-importing the identity the server already has must be a safe no-op.
func TestIdentityBackupExportRoundTrip(t *testing.T) {
	t.Parallel()
	_, _, ts, h, cookies := identityAdmin(t, "identityroundtrip")

	resp := post(t, ts, h.app, "/admin/identity/export", url.Values{}, cookies...)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export = %d, want 200", resp.StatusCode)
	}
	envelope := extractBackup(t, string(body))

	// Pasting it straight back is the idempotent case.
	resp = post(t, ts, h.app, "/admin/identity/restore",
		url.Values{"backup": {envelope}}, cookies...)
	body, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read restore: %v", err)
	}
	// Match the success alert, not the phrase alone: "already using" also appears in the
	// restore card's static help text, which made an earlier version of this pass
	// vacuously even while export was failing.
	alert := firstAlert(string(body))
	if !strings.Contains(alert, "alert-success") {
		t.Fatalf("restoring the current identity didn't report success; alert was: %s", alert)
	}
	if !strings.Contains(alert, "already using") {
		t.Errorf("success alert doesn't say it was a no-op: %s", alert)
	}
}

// TestIdentityRestoreRejectsBadPaste: operator typos and foreign backups must fail
// inline with an explanation, never a 500 and never a partial write.
func TestIdentityRestoreRejectsBadPaste(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h, cookies := identityAdmin(t, "identitybadpaste")

	before, _, err := st.GetServerIdentity(ctx)
	if err != nil {
		t.Fatalf("get identity: %v", err)
	}

	for _, tc := range []struct{ name, backup, want string }{
		{"empty", "", "Paste the whole value"},
		{"garbage", "hello world", "Paste the whole value"},
		{"right shape, wrong master key",
			// A well-formed envelope whose ciphertext was sealed under a different key.
			"meshtender-identity-v1." + strings.Repeat("a", 64) + ".AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			"could not be opened"},
	} {
		resp := post(t, ts, h.app, "/admin/identity/restore",
			url.Values{"backup": {tc.backup}}, cookies...)
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatalf("%s: read body: %v", tc.name, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 with an inline error", tc.name, resp.StatusCode)
		}
		if !strings.Contains(string(body), tc.want) {
			t.Errorf("%s: expected an error containing %q; page said: %s",
				tc.name, tc.want, firstAlert(string(body)))
		}
	}

	// Nothing may have changed.
	after, _, err := st.GetServerIdentity(ctx)
	if err != nil {
		t.Fatalf("get identity: %v", err)
	}
	if after != before {
		t.Errorf("a rejected paste changed the stored identity: %s -> %s", before, after)
	}
}

// extractBackup pulls the envelope out of the rendered textarea.
func extractBackup(t *testing.T, page string) string {
	t.Helper()
	const marker = "meshtender-identity-v1."
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("no backup value in the response; page said: %s", firstAlert(page))
	}
	rest := page[i:]
	end := strings.IndexAny(rest, "<\n\" ")
	if end < 0 {
		t.Fatal("backup value not terminated")
	}
	return rest[:end]
}

// firstAlert returns the page's first alert text, so failures report what the UI actually
// said instead of dumping the whole document.
func firstAlert(page string) string {
	i := strings.Index(page, `<div class="alert`)
	if i < 0 {
		return "(no alert on page)"
	}
	rest := page[i:]
	if j := strings.Index(rest, "</div>"); j > 0 {
		return strings.Join(strings.Fields(rest[:j]), " ")
	}
	return "(unterminated alert)"
}
