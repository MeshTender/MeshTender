package web

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/config"
)

// TestRenderErrorDoesNotLeak: when a page fails mid-execution, the renderer must
// return a generic 500 with no internal error text (template/query details) and
// no partial body. Regression for the pre-release audit finding that the error
// path wrote err.Error() straight to the response (and double-wrote after a
// partial 200).
func TestRenderErrorDoesNotLeak(t *testing.T) {
	t.Parallel()
	// A "base" layout that writes some markup and then indexes past the end of a
	// slice — an execution-time error, the shape a real template/query failure
	// takes. The leading <p> proves the partial body is discarded too.
	tmpl := template.Must(template.New("base").Parse(`{{define "base"}}<p>SECRET{{index .Items 99}}</p>{{end}}`))
	rn := &Renderer{
		cfg:   &config.Config{PrimaryHost: "app.example", AuthHost: "auth.example", RootHost: "example"},
		pages: map[string]*template.Template{"boom.html": tmpl},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "https://app.example/", nil)
	rn.Render(rec, req, "boom.html", map[string]any{"Layout": "base", "Items": []string{}})

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	for _, leaked := range []string{"index out of range", "error calling", "SECRET"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("response leaked %q: %s", leaked, body)
		}
	}
	if !strings.Contains(body, "Something went wrong") {
		t.Fatalf("want generic error message, got %q", body)
	}
}
