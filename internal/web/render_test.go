package web

import (
	"io"
	"strings"
	"testing"
)

// TestBuildPagesComposeAndExecute verifies every content page is pre-built with
// the shared layouts and can execute against each root layout without a parse
// or template-resolution error. This guards the startup-time template
// composition (each page redefines content/title/header onto the base set).
func TestBuildPagesComposeAndExecute(t *testing.T) {
	pages, err := buildPages()
	if err != nil {
		t.Fatalf("buildPages: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("buildPages returned no pages")
	}
	// The shared partials are not pages and must not be rendered directly.
	for _, name := range []string{"base.html", "icons.html"} {
		if _, ok := pages[name]; ok {
			t.Errorf("%s should not be registered as a page", name)
		}
	}

	layouts := []string{"base", "authbase", "landingbase"}
	for name, tmpl := range pages {
		for _, layout := range layouts {
			if tmpl.Lookup(layout) == nil {
				t.Errorf("page %s missing layout %q", name, layout)
				continue
			}
			// Execute with empty data: this catches references to undefined
			// partials (e.g. {{template "missing"}}). Runtime errors driven by
			// missing data (e.g. len of an absent map key) are expected here and
			// don't indicate a composition problem.
			err := tmpl.ExecuteTemplate(io.Discard, layout, map[string]any{})
			if err != nil && strings.Contains(err.Error(), "no such template") {
				t.Errorf("execute %s with layout %q: %v", name, layout, err)
			}
		}
	}
}
