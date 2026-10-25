package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/MeshTender/MeshTender/internal/config"
)

// notFound stands in for the host dispatcher.
var notFound = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.NotFound(w, nil) })

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "https://app.example"+path, nil))
	return rec
}

func TestLiveReloadEndpointOnlyWhenEnabled(t *testing.T) {
	t.Parallel()
	// Production: the path doesn't exist.
	if rec := get(t, WithLiveReload(false, notFound), LiveReloadPath); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled: %d, want 404", rec.Code)
	}

	h := WithLiveReload(true, notFound)
	first := get(t, h, LiveReloadPath)
	if first.Code != http.StatusOK || first.Body.Len() == 0 {
		t.Fatalf("enabled: %d %q", first.Code, first.Body.String())
	}
	if cc := first.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q; a cached answer would hide the restart", cc)
	}
	// Stable for the life of the process…
	if again := get(t, h, LiveReloadPath); again.Body.String() != first.Body.String() {
		t.Errorf("boot ID changed without a restart: %q → %q", first.Body.String(), again.Body.String())
	}
	// …and different for the next one, which is what the script reloads on.
	if next := get(t, WithLiveReload(true, notFound), LiveReloadPath); next.Body.String() == first.Body.String() {
		t.Error("a new process reused the boot ID")
	}
	// Everything else still reaches the app.
	if rec := get(t, h, "/repeaters"); rec.Code != http.StatusNotFound {
		t.Errorf("other path: %d, want the dispatcher's 404", rec.Code)
	}
}

// TestEveryLayoutLoadsLiveReloadOnlyInDev: the script has to reach every page
// layout in dev (a page that misses it just goes stale after a rebuild), and
// none of them in production.
func TestEveryLayoutLoadsLiveReloadOnlyInDev(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		cfg := &config.Config{PrimaryHost: "app.example", AuthHost: "auth.example", RootHost: "example", LiveReload: enabled}
		rnd, err := NewRenderer(cfg, fstest.MapFS{})
		if err != nil {
			t.Fatal(err)
		}
		for _, layout := range []string{"base", "rootbase", "authbase", "landingbase"} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "https://app.example/nope", nil)
			rnd.render(rec, req, http.StatusNotFound, "error.html", map[string]any{
				"Layout": layout, "Status": http.StatusNotFound, "Title": "Page not found", "Message": "gone",
			})
			if got := strings.Contains(rec.Body.String(), "/static/livereload."); got != enabled {
				t.Errorf("%s with LiveReload=%v: script present = %v", layout, enabled, got)
			}
		}
	}
}
