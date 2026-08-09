package web

import (
	"strings"
	"testing"
)

// TestSharedLayoutDefinesBrandMark: every layout in base.html renders the brand
// mark via {{template "icon-logo"}}, which brand.html defines. Two separate lists
// have to agree for that to work — the go:embed directive and the ParseFS call in
// NewRenderer — and a file missing from either one leaves "icon-logo" undefined,
// which fails every page on the site. Nothing else asserts that the shared layout
// parses, so an omission surfaces only as an unrelated test failing obscurely.
func TestSharedLayoutDefinesBrandMark(t *testing.T) {
	t.Parallel()

	rn, err := NewRenderer(cspTestConfig(), sharedTemplatesFS)
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	page, ok := rn.pages["error.html"]
	if !ok {
		t.Fatal("no error.html page in the renderer; pick another shared page for this check")
	}
	if page.Lookup("icon-logo") == nil {
		t.Error(`the shared layout does not define "icon-logo", so every page that renders the ` +
			`brand mark fails. brand.html has to appear in BOTH the go:embed directive and the ` +
			`ParseFS call in NewRenderer.`)
	}

	// The mark itself, not merely a definition of that name: catches a stub or an
	// accidentally emptied file.
	var b strings.Builder
	if err := page.ExecuteTemplate(&b, "icon-logo", "test-class"); err != nil {
		t.Fatalf("executing icon-logo: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "<svg") || !strings.Contains(out, "<path") {
		t.Errorf("icon-logo rendered no SVG artwork: %q", out)
	}
	if !strings.Contains(out, "test-class") {
		t.Errorf("icon-logo ignored its class argument, which the call sites rely on for spacing: %q", out)
	}
}
