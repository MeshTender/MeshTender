package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/go-chi/chi/v5"
)

var fingerprintedRe = regexp.MustCompile(`^/static/ui\.[0-9a-f]{8}\.js$`)

func TestAssetURLFingerprints(t *testing.T) {
	t.Parallel()

	got := assets.URL("/static/ui.js")
	if !fingerprintedRe.MatchString(got) {
		t.Fatalf("asset URL not fingerprinted: got %q, want /static/ui.<8hex>.js", got)
	}
	if got == "/static/ui.js" {
		t.Fatal("asset URL unchanged; fingerprinting did not apply")
	}

	// Unknown assets pass through unchanged so a stray reference still resolves.
	if got := assets.URL("/static/does-not-exist.js"); got != "/static/does-not-exist.js" {
		t.Fatalf("unknown asset should pass through, got %q", got)
	}

	// Extensions with a dotted stem keep the hash before the final extension.
	if got := assets.URL("/static/tabler.min.css"); !regexp.MustCompile(`^/static/tabler\.min\.[0-9a-f]{8}\.css$`).MatchString(got) {
		t.Fatalf("dotted-stem asset mis-fingerprinted: %q", got)
	}
}

// staticRouter mirrors the mount in SharedRoutes so the test exercises the real
// StripPrefix + handler path.
func staticRouter() http.Handler {
	r := chi.NewRouter()
	r.Handle("/static/*", http.StripPrefix("/static/", http.HandlerFunc(assets.serveHTTP)))
	return r
}

func TestStaticFingerprintedImmutable(t *testing.T) {
	t.Parallel()
	srv := staticRouter()

	want, err := fs.ReadFile(staticFS, "static/ui.js")
	if err != nil {
		t.Fatalf("read embedded ui.js: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, assets.URL("/static/ui.js"), nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("fingerprinted asset: status %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("fingerprinted asset Cache-Control = %q, want immutable one-year", cc)
	}
	if rec.Body.String() != string(want) {
		t.Fatal("fingerprinted asset body does not match embedded file")
	}
}

func TestStaticStaleHash404(t *testing.T) {
	t.Parallel()
	srv := staticRouter()

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/ui.deadbeef.js", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("stale fingerprint: status %d, want 404", rec.Code)
	}
}

func TestStaticUnfingerprintedStillServed(t *testing.T) {
	t.Parallel()
	srv := staticRouter()

	// Old/direct links to the logical path keep working, just without the
	// immutable header (no fingerprint = can't promise immutability).
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/ui.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("un-fingerprinted asset: status %d, want 200", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Fatalf("un-fingerprinted asset should not be immutable, got Cache-Control %q", cc)
	}
}
