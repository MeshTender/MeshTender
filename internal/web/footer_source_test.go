package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestEveryLayoutFooterOffersSource: AGPL section 13 obliges a network deployment
// to offer its source to the people using it, and the footer link is how this one
// does. That makes the link a licensing obligation rather than a nicety — it has
// to appear on every surface, so a page rendered on the auth or app host is as
// covered as the public root.
//
// footerbody is shared by every layout, but the URL is injected by the renderer,
// so a layout could render the footer with .SourceURL unset and silently emit an
// empty href.
func TestEveryLayoutFooterOffersSource(t *testing.T) {
	t.Parallel()

	// The layouts base.html defines. Each renders footerbody; each has to end up
	// with a working link.
	for _, layout := range []string{"base", "rootbase", "authbase", "landingbase"} {
		t.Run(layout, func(t *testing.T) {
			t.Parallel()
			e := testEnv(t)
			if e.Renderer.pages["error.html"].Lookup(layout) == nil {
				t.Fatalf("base.html no longer defines a %q layout; update this list", layout)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "https://app.example/nope", nil)
			e.Renderer.render(rec, req, http.StatusNotFound, "error.html", map[string]any{
				"Layout": layout,
				"Status": http.StatusNotFound, "Title": "Page not found", "Message": "gone",
			})

			body := rec.Body.String()
			want := `href="` + SourceURL + `"`
			if !strings.Contains(body, want) {
				t.Errorf("%s footer has no source link (%s)", layout, want)
			}
			if !strings.Contains(body, ">Source<") {
				t.Errorf("%s footer links the source without labeling it, so a reader cannot tell "+
					"what it offers", layout)
			}
		})
	}
}
