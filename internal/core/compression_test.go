package core

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestHTMLCompression locks the response-compression policy:
//   - server-rendered HTML is gzipped when the client accepts it, on every host
//   - it carries Vary: Accept-Encoding, so a shared cache can't hand a gzip body
//     to a client that didn't ask for one (the public root pages are cacheable)
//   - a client that doesn't accept gzip still gets valid, readable HTML
//   - pre-compressed static assets are NOT re-compressed — they keep the brotli
//     they were built with at startup
//
// Regression for the pre-release audit finding that HTML was served uncompressed
// (the org directory shipped ~31 KB of markup per request).
func TestHTMLCompression(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	get := func(t *testing.T, host, path, acceptEncoding string) *http.Response {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Host = host
		// Set explicitly (and never leave it empty): Go's transport adds gzip
		// automatically and transparently decompresses, which would hide the header
		// we're asserting on.
		req.Header.Set("Accept-Encoding", acceptEncoding)
		resp, err := noRedirect().Do(req)
		if err != nil {
			t.Fatalf("get %s%s: %v", host, path, err)
		}
		return resp
	}

	t.Run("html is gzipped on every host", func(t *testing.T) {
		for _, page := range []struct{ name, host, path string }{
			{"root landing", h.root, "/"},
			{"root directory", h.root, "/orgs"},
			{"root docs", h.root, "/docs"},
			{"auth login", h.auth, "/login"},
			{"app 404", h.app, "/nonexistent-page"},
		} {
			resp := get(t, page.host, page.path, "gzip")
			if enc := resp.Header.Get("Content-Encoding"); enc != "gzip" {
				resp.Body.Close()
				t.Errorf("%s Content-Encoding = %q, want gzip", page.name, enc)
				continue
			}
			if vary := resp.Header.Values("Vary"); !hasToken(vary, "Accept-Encoding") {
				t.Errorf("%s Vary = %q, want it to include Accept-Encoding", page.name, vary)
			}
			// The body must be real gzip that decodes to the page.
			zr, err := gzip.NewReader(resp.Body)
			if err != nil {
				resp.Body.Close()
				t.Errorf("%s: body is not valid gzip: %v", page.name, err)
				continue
			}
			body, err := io.ReadAll(zr)
			resp.Body.Close()
			if err != nil {
				t.Errorf("%s: gunzip: %v", page.name, err)
				continue
			}
			if !strings.Contains(string(body), "</html>") {
				t.Errorf("%s: decompressed body isn't a complete page (%d bytes)", page.name, len(body))
			}
		}
	})

	// A client that can't decompress must still get a usable page.
	t.Run("identity still served uncompressed", func(t *testing.T) {
		resp := get(t, h.root, "/orgs", "identity")
		defer resp.Body.Close()
		if enc := resp.Header.Get("Content-Encoding"); enc != "" {
			t.Fatalf("Content-Encoding = %q, want empty for an identity request", enc)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !strings.Contains(string(body), "</html>") {
			t.Fatalf("identity body isn't a complete page (%d bytes)", len(body))
		}
	})

	// Compression must actually pay for itself on a real page.
	t.Run("compression meaningfully shrinks the directory page", func(t *testing.T) {
		plain := get(t, h.root, "/orgs", "identity")
		rawBody, err := io.ReadAll(plain.Body)
		plain.Body.Close()
		if err != nil {
			t.Fatalf("read identity body: %v", err)
		}
		zipped := get(t, h.root, "/orgs", "gzip")
		gzBody, err := io.ReadAll(zipped.Body)
		zipped.Body.Close()
		if err != nil {
			t.Fatalf("read gzip body: %v", err)
		}
		if len(gzBody) >= len(rawBody) {
			t.Fatalf("gzip body (%d) not smaller than identity (%d)", len(gzBody), len(rawBody))
		}
		// Markup compresses far better than this; a floor of 50% catches a silent
		// regression (e.g. compression quietly disabled) without being brittle.
		ratio := float64(len(gzBody)) / float64(len(rawBody))
		if ratio > 0.5 {
			t.Errorf("gzip only got %.0f%% of original (%d → %d bytes); expected under 50%%",
				ratio*100, len(rawBody), len(gzBody))
		}
		t.Logf("/orgs: %d bytes → %d bytes gzipped (%.0f%%)", len(rawBody), len(gzBody), ratio*100)
	})

	// Static assets are pre-compressed at startup with brotli; the HTML compressor
	// must not touch them (double-encoding would corrupt the response).
	t.Run("pre-compressed assets are not re-compressed", func(t *testing.T) {
		asset := fingerprintedAsset(t, ts, h.root, "/", "ui")
		resp := get(t, h.root, asset, "br, gzip")
		defer resp.Body.Close()
		if enc := resp.Header.Get("Content-Encoding"); enc != "br" {
			t.Fatalf("%s Content-Encoding = %q, want br (the startup-built variant)", asset, enc)
		}
		if strings.Contains(resp.Header.Get("Content-Encoding"), ",") {
			t.Fatalf("%s was double-encoded: %q", asset, resp.Header.Get("Content-Encoding"))
		}
	})
}
