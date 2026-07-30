package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jleight/meshtender/internal/config"
)

func TestSecurityHeadersCSPNonce(t *testing.T) {
	t.Parallel()
	var seen []string
	h := (&Env{}).securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The nonce in the header must match the one templates read from context.
		seen = append(seen, NonceFromContext(r.Context()))
	}))

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/", nil))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/", nil))

	csp := rec1.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP missing base directives: %q", csp)
	}
	if !strings.Contains(csp, "script-src 'self' 'nonce-"+seen[0]+"'") {
		t.Fatalf("CSP script-src doesn't carry the context nonce %q: %q", seen[0], csp)
	}
	if rec1.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("missing X-Content-Type-Options: nosniff")
	}
	if rec1.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("missing X-Frame-Options: DENY")
	}
	if rec1.Header().Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Errorf("missing Cross-Origin-Opener-Policy: same-origin")
	}
	// Permissions-Policy must keep the features we actually use.
	pp := rec1.Header().Get("Permissions-Policy")
	if !strings.Contains(pp, "serial=(self)") || !strings.Contains(pp, "publickey-credentials-get=(self)") {
		t.Errorf("Permissions-Policy doesn't allow serial/webauthn: %q", pp)
	}
	if seen[0] == "" || seen[1] == "" || seen[0] == seen[1] {
		t.Errorf("nonce not fresh per request: %q, %q", seen[0], seen[1])
	}
}

func TestSecurityHeadersHSTSGatedOnTLS(t *testing.T) {
	t.Parallel()
	// No TLS (nil/insecure config): no HSTS.
	rec := httptest.NewRecorder()
	(&Env{}).securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Errorf("HSTS set without TLS: %q", rec.Header().Get("Strict-Transport-Security"))
	}
	// TLS on: HSTS present.
	rec = httptest.NewRecorder()
	(&Env{Cfg: &config.Config{Secure: true}}).
		securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.HasPrefix(rec.Header().Get("Strict-Transport-Security"), "max-age=") {
		t.Errorf("HSTS missing under TLS: %q", rec.Header().Get("Strict-Transport-Security"))
	}
}

// TestTemplatesHaveNoInlineJS enforces the CSP-compatible pattern across every
// template: no inline event handlers (on*=), and no un-nonce'd inline <script>.
// Nonces don't cover on*= handlers, so any new one would silently break under CSP.
func TestTemplatesHaveNoInlineJS(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	handler := regexp.MustCompile(`(?i)\son(click|change|submit|input|load|keyup|keydown|mouseover|focus|blur)\s*=`)
	bareScript := regexp.MustCompile(`<script>`)

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if loc := handler.FindIndex(b); loc != nil {
			t.Errorf("%s: inline on*= handler (CSP-forbidden): %q", rel, snippet(b, loc[0]))
		}
		if bareScript.Match(b) {
			t.Errorf("%s: un-nonce'd inline <script> — use <script nonce=\"{{.Nonce}}\">", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// TestTemplatesUseAssetHelper enforces that every static reference goes through
// the {{ asset }} helper (href="{{ asset "/static/x" }}"), never a bare
// href="/static/x". The helper rewrites the URL to a content-hashed,
// immutably-cached path; a bare reference silently loses that caching.
func TestTemplatesUseAssetHelper(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	bare := regexp.MustCompile(`(?i)(href|src)="/static/`)

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if loc := bare.FindIndex(b); loc != nil {
			t.Errorf("%s: bare static reference — use {{ asset \"/static/...\" }}: %q", rel, snippet(b, loc[0]))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// TestTemplatesHeadingConventions pins the heading levels the shared layouts assume,
// so a new page can't quietly reintroduce the outline problems from the audit (A1:
// no <h1> anywhere; A2: levels skipped or jumping backwards). The rendered-page
// counterpart is core.TestPageHeadingOutlines, which walks real responses; this one
// catches a bad heading the moment it's written, in whichever template it lands.
//
// The rules mirror the page structure: the page title is the h1, cards and modal
// dialogs are sections under it (h2), and alerts nest inside cards (h3).
func TestTemplatesHeadingConventions(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)

	banned := []struct {
		pattern *regexp.Regexp
		why     string
	}{
		{regexp.MustCompile(`(?i)<h[2-6][^>]*class="[^"]*\bpage-title\b`),
			`page-title must be <h1> — it names the page`},
		{regexp.MustCompile(`(?i)<h(?:[13-6])[^>]*class="[^"]*\bcard-title\b`),
			`card-title must be <h2> — a card is a section under the page's h1 ` +
				`(auth pages are the exception and are checked by the rendered test)`},
		{regexp.MustCompile(`(?i)<h(?:1|[3-6])[^>]*class="[^"]*\bmodal-title\b`),
			`modal-title must be <h2> — a dialog title sits under the page's h1`},
		{regexp.MustCompile(`(?i)<h(?:[12]|[4-6])[^>]*class="[^"]*\balert-title\b`),
			`alert-title must be <h3> — alerts nest inside cards`},
	}
	// login/signup have no page header, so their card title legitimately carries the
	// page's h1.
	authPages := map[string]bool{"login.html": true, "signup.html": true}

	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		for _, rule := range banned {
			if authPages[filepath.Base(path)] && strings.Contains(rule.why, "card-title") {
				continue
			}
			if loc := rule.pattern.FindIndex(b); loc != nil {
				t.Errorf("%s: %s — found %q", rel, rule.why, snippet(b, loc[0]))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

func snippet(b []byte, at int) string {
	end := at + 40
	if end > len(b) {
		end = len(b)
	}
	return string(b[at:end])
}

// moduleRoot walks up from the test's working directory to the dir holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above working directory")
		}
		dir = parent
	}
}
