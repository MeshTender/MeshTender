package web

import (
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/go-chi/chi/v5"
)

var fingerprintedRe = regexp.MustCompile(`^/static/ui\.[0-9a-f]{8}\.js$`)

func TestAssetURLFingerprints(t *testing.T) {
	t.Parallel()

	got := assets().URL("/static/ui.js")
	if !fingerprintedRe.MatchString(got) {
		t.Fatalf("asset URL not fingerprinted: got %q, want /static/ui.<8hex>.js", got)
	}
	if got == "/static/ui.js" {
		t.Fatal("asset URL unchanged; fingerprinting did not apply")
	}

	// Unknown assets pass through unchanged so a stray reference still resolves.
	if got := assets().URL("/static/does-not-exist.js"); got != "/static/does-not-exist.js" {
		t.Fatalf("unknown asset should pass through, got %q", got)
	}

	// Extensions with a dotted stem keep the hash before the final extension.
	if got := assets().URL("/static/tabler.min.css"); !regexp.MustCompile(`^/static/tabler\.min\.[0-9a-f]{8}\.css$`).MatchString(got) {
		t.Fatalf("dotted-stem asset mis-fingerprinted: %q", got)
	}
}

// staticRouter mirrors the mount in SharedRoutes so the test exercises the real
// StripPrefix + handler path.
func staticRouter() http.Handler {
	r := chi.NewRouter()
	r.Handle("/static/*", http.StripPrefix("/static/", http.HandlerFunc(assets().serveHTTP)))
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
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, assets().URL("/static/ui.js"), nil))

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

func TestStaticServesBrotli(t *testing.T) {
	t.Parallel()
	srv := staticRouter()

	want, err := fs.ReadFile(staticFS, "static/tabler.min.css")
	if err != nil {
		t.Fatalf("read embedded asset: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, assets().URL("/static/tabler.min.css"), nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "br" {
		t.Fatalf("Content-Encoding = %q, want br (preferred over gzip)", enc)
	}
	if v := rec.Header().Get("Vary"); v != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want Accept-Encoding", v)
	}
	if rec.Body.Len() >= len(want) {
		t.Fatalf("brotli body %d not smaller than raw %d", rec.Body.Len(), len(want))
	}
	got, err := io.ReadAll(brotli.NewReader(rec.Body))
	if err != nil {
		t.Fatalf("brotli decode: %v", err)
	}
	if string(got) != string(want) {
		t.Fatal("decoded brotli body does not match embedded file")
	}
}

func TestStaticServesGzipWhenBrotliUnwanted(t *testing.T) {
	t.Parallel()
	srv := staticRouter()

	want, err := fs.ReadFile(staticFS, "static/ui.js")
	if err != nil {
		t.Fatalf("read embedded asset: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, assets().URL("/static/ui.js"), nil)
	req.Header.Set("Accept-Encoding", "gzip") // no br
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gzip decode: %v", err)
	}
	if string(got) != string(want) {
		t.Fatal("decoded gzip body does not match embedded file")
	}
}

func TestStaticIdentityStillVaries(t *testing.T) {
	t.Parallel()
	srv := staticRouter()

	want, err := fs.ReadFile(staticFS, "static/ui.js")
	if err != nil {
		t.Fatalf("read embedded asset: %v", err)
	}

	// No Accept-Encoding: serve raw, but still advertise that the resource varies
	// so shared caches don't hand a compressed copy to a client that can't decode.
	req := httptest.NewRequest(http.MethodGet, assets().URL("/static/ui.js"), nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("Content-Encoding = %q, want none", enc)
	}
	if v := rec.Header().Get("Vary"); v != "Accept-Encoding" {
		t.Fatalf("Vary = %q, want Accept-Encoding", v)
	}
	if rec.Body.String() != string(want) {
		t.Fatal("identity body does not match embedded file")
	}
}

func TestAcceptsEncoding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		header, coding string
		want           bool
	}{
		{"gzip, deflate, br", "br", true},
		{"gzip, deflate, br", "gzip", true},
		{"gzip", "br", false},
		{"", "gzip", false},
		{"br;q=0", "br", false},      // explicit refusal
		{"gzip;q=0.5", "gzip", true}, // low but nonzero
		{"identity", "gzip", false},
		{"GZIP", "gzip", true}, // case-insensitive token
	}
	for _, c := range cases {
		if got := acceptsEncoding(c.header, c.coding); got != c.want {
			t.Errorf("acceptsEncoding(%q, %q) = %v, want %v", c.header, c.coding, got, c.want)
		}
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

// BenchmarkBuildAssetManifest measures what `assets` costs to construct, which
// is why it is built lazily rather than at package init. Run it with -race to
// see the number that mattered: the race detector instruments this pure-CPU
// compression heavily, and as a package-level var it was charged to every test
// binary that imported this package, not just the ones serving assets.
func BenchmarkBuildAssetManifest(b *testing.B) {
	for b.Loop() {
		buildAssetManifest(staticFS, "static")
	}
}
