package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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
	if seen[0] == "" || seen[1] == "" || seen[0] == seen[1] {
		t.Errorf("nonce not fresh per request: %q, %q", seen[0], seen[1])
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
