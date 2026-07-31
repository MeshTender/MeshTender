package web

import (
	"bytes"
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

// TestCSPFormActionAllowsSiblingSurfaces is the regression test for a bug that broke
// password sign-in and sign-up in Chrome while every server-side test passed.
//
// Credential POSTs land on the auth host and answer 303 to the app host's handoff.
// Chrome enforces form-action across the redirect chain (the spec says it shouldn't,
// and Firefox doesn't), so `form-action 'self'` made the browser refuse to follow that
// redirect. The POST still arrived and the handler still succeeded — the server logged a
// clean 303 — so the only visible symptom was a button that did nothing, and nothing
// server-side looked wrong at all.
//
// The port matters as much as the host: a source expression without one only matches
// 443, so a dev deployment on :8080 needs the port present or it's blocked all over
// again.
func TestCSPFormActionAllowsSiblingSurfaces(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		PrimaryHost: "app.example.test",
		AuthHost:    "auth.example.test",
		RootHost:    "example.test",
		Secure:      true,
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.Host = "auth.example.test:8443"
	(&Env{Cfg: cfg}).securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"'self'",
		"https://app.example.test:8443",  // the handoff target — the one that was blocked
		"https://auth.example.test:8443", // where credential forms live
		"https://example.test:8443",      // the root beacon
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("form-action missing %q: %q", want, csp)
		}
	}
	// Still a closed list — a foreign origin must not be able to receive our forms.
	if strings.Contains(csp, "form-action *") || strings.Contains(csp, "form-action 'unsafe") {
		t.Errorf("form-action was widened to a wildcard: %q", csp)
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
	// The auth host's standalone pages have no page header, so their card title
	// legitimately carries the page's h1. They're single-purpose panels on the
	// authbase layout — sign in, sign up, and the two account-recovery steps.
	authPages := map[string]bool{
		"login.html": true, "signup.html": true,
		"forgot.html": true, "reset.html": true,
	}

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

// controlRe matches a form control's opening tag.
var controlRe = regexp.MustCompile(`(?is)<(input|select|textarea)\b([^>]*)>`)

// namedRe matches the attributes that give a control an accessible name directly.
var namedRe = regexp.MustCompile(`(?i)aria-label\s*=|aria-labelledby\s*=|\btitle\s*=`)

// skipTypes are input types with no user-visible value to name.
var skipTypes = map[string]bool{
	"hidden": true, "submit": true, "button": true, "image": true, "reset": true,
}

// TestTemplatesFormControlsAreLabeled enforces that every form control has an
// accessible name. Without one, a screen reader announces "edit text, blank" and the
// user has to guess from surrounding prose — assuming they find it at all.
//
// A control counts as named by any of:
//   - aria-label / aria-labelledby / title on the control
//   - a <label for="..."> anywhere in the template set (labels sometimes live in a
//     different file from their control, e.g. shared partials)
//   - being wrapped in its own <label> (Bootstrap's form-check pattern) — this is why
//     a naive for=/id scan is useless here: it reports 22 false positives on this
//     codebase.
//
// Note a placeholder is NOT a label: it vanishes on input and is not reliably
// announced. Neither is a nearby heading, nor a <label> that lacks for= and doesn't
// wrap its control — that last one looks correct in the markup and is the trap this
// test exists to catch (audit A3 found three of them in the region editor).
func TestTemplatesFormControlsAreLabeled(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)

	// Pass 1: every for= target across all templates.
	labelFor := map[string]bool{}
	forRe := regexp.MustCompile(`(?is)<label[^>]*\bfor="([^"]+)"`)
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".html") {
			return nil
		}
		files = append(files, path)
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range forRe.FindAllSubmatch(b, -1) {
			labelFor[string(m[1])] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}

	idRe := regexp.MustCompile(`(?i)\bid="([^"]+)"`)
	typeRe := regexp.MustCompile(`(?i)type="([^"]+)"`)
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		for _, loc := range controlRe.FindAllSubmatchIndex(b, -1) {
			tag := string(b[loc[2]:loc[3]])
			attrs := b[loc[4]:loc[5]]
			if tm := typeRe.FindSubmatch(attrs); tag == "input" && tm != nil &&
				skipTypes[strings.ToLower(string(tm[1]))] {
				continue
			}
			if namedRe.Match(attrs) {
				continue
			}
			if im := idRe.FindSubmatch(attrs); im != nil && labelFor[string(im[1])] {
				continue
			}
			// Wrapped by its own <label>? Count unclosed <label> tags before it.
			before := b[:loc[0]]
			if bytes.Count(before, []byte("<label")) > bytes.Count(before, []byte("</label>")) {
				continue
			}
			t.Errorf("%s: <%s> has no accessible name (add aria-label, or a <label for=> / "+
				"wrapping <label>): %q", rel, tag, snippet(b, loc[0]))
		}
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
