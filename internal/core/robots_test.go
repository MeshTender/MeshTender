package core

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestRobotsAndNoIndex locks the crawl policy across the split hosts:
//   - the root (discovery) host stays crawlable; its robots.txt does NOT block /
//   - the app and auth hosts tell crawlers to stay out entirely (Disallow: /)
//   - the per-repeater and profile pages send X-Robots-Tag: noindex (dropped
//     from search) while the root discovery pages do not (still indexable)
//   - the whole app host sends noindex
//
// Regression for the pre-release audit finding that public pages (incl. exact
// repeater GPS and personal profiles) were fully search-indexable with no
// robots.txt anywhere.
func TestRobotsAndNoIndex(t *testing.T) {
	t.Parallel()
	_, _, ts, h := splitServer(t)

	body := func(resp *http.Response) string {
		t.Helper()
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return string(b)
	}

	t.Run("root robots.txt stays crawlable", func(t *testing.T) {
		resp := do(t, ts, h.root, "/robots.txt")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("root /robots.txt = %d, want 200", resp.StatusCode)
		}
		if txt := body(resp); strings.Contains(txt, "Disallow: /\n") {
			t.Fatalf("root robots.txt blocks everything: %q", txt)
		}
	})

	for _, host := range []struct{ name, h string }{{"app", h.app}, {"auth", h.auth}} {
		t.Run(host.name+" robots.txt disallows all", func(t *testing.T) {
			resp := do(t, ts, host.h, "/robots.txt")
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("%s /robots.txt = %d, want 200", host.name, resp.StatusCode)
			}
			if txt := body(resp); !strings.Contains(txt, "Disallow: /") {
				t.Fatalf("%s robots.txt should disallow all, got %q", host.name, txt)
			}
		})
	}

	// noindex is set by middleware before the handler, so a 404 target still
	// carries it — enough to prove the wiring without seeding data.
	for _, path := range []string{"/r/nonexistent", "/u/nonexistent"} {
		t.Run("noindex on "+path, func(t *testing.T) {
			resp := do(t, ts, h.root, path)
			resp.Body.Close()
			if got := resp.Header.Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
				t.Fatalf("%s X-Robots-Tag = %q, want noindex", path, got)
			}
		})
	}

	t.Run("root discovery pages stay indexable", func(t *testing.T) {
		for _, path := range []string{"/", "/orgs"} {
			resp := do(t, ts, h.root, path)
			resp.Body.Close()
			if got := resp.Header.Get("X-Robots-Tag"); got != "" {
				t.Fatalf("root %s should be indexable, but X-Robots-Tag = %q", path, got)
			}
		}
	})

	t.Run("app host sends noindex", func(t *testing.T) {
		resp := do(t, ts, h.app, "/healthz")
		resp.Body.Close()
		if got := resp.Header.Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
			t.Fatalf("app /healthz X-Robots-Tag = %q, want noindex", got)
		}
	})
}
