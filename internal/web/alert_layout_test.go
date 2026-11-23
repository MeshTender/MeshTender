package web

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// templateAction matches a Go template action. They're stripped before parsing so
// a conditional or a field interpolation inside an alert isn't mistaken for
// markup of its own.
var templateAction = regexp.MustCompile(`\{\{[^}]*\}\}`)

// iconAction matches an icon partial invocation, `{{template "icon-…" "classes"}}`,
// capturing its classes. It's expanded to a stand-in <svg> before the other
// actions are stripped, so the icon's position in the tree survives parsing.
var iconAction = regexp.MustCompile(`\{\{template "icon-[a-z0-9-]+" "([^"]*)"\}\}`)

// TestAlertIconsAreDirectChildren enforces that a Tabler .alert-icon sits
// directly inside its .alert.
//
// Tabler (1.7+) lays the icon out with `.alert > .alert-icon` (absolutely
// positioned in the left padding) and `.alert:has(> .alert-icon)` (which widens
// that padding to make room). Both selectors are child combinators, so an icon
// wrapped in anything — the `<div>` slot the pre-1.7 flex layout wanted — matches
// neither: it drops into normal flow and renders above the title instead of
// beside it. Nothing errors; it just looks wrong.
//
//	<div class="alert alert-warning" role="alert">
//	  {{template "icon-alert" "alert-icon"}}
//	  <div>… body …</div>
//	</div>
func TestAlertIconsAreDirectChildren(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)

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

		src := iconAction.ReplaceAll(b, []byte(`<svg class="icon $1"></svg>`))
		src = templateAction.ReplaceAll(src, nil)
		doc, err := html.Parse(strings.NewReader(string(src)))
		if err != nil {
			t.Errorf("%s: parse: %v", rel, err)
			return nil
		}
		for _, icon := range findByClass(doc, "alert-icon") {
			if p := icon.Parent; p == nil || !hasClass(p, "alert") {
				t.Errorf("%s: .alert-icon is not a direct child of .alert — Tabler positions it "+
					"with `.alert > .alert-icon`, so a wrapped icon renders above the title. "+
					"Drop the wrapper element.", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// TestFullWidthFlexButtonsAreTextStart is the same class of trap as
// TestAlertIconsAreDirectChildren: a component whose default styling quietly
// contradicts the layout it's being used for.
//
// A <button> centers its text. That's invisible on an ordinary button, whose text
// is the whole content — but a full-width button used as a row (d-flex + w-100,
// e.g. the MeshCore key row in the public profile's Links list) inherits that
// centering into every child, so its label floats in the middle of the row while
// every neighboring row is left-aligned. Nothing errors; it just looks wrong next
// to the rows around it.
//
// The fix is always the same: add text-start.
func TestFullWidthFlexButtonsAreTextStart(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)

	// Buttons only: an <a> or <div> laid out the same way already inherits the
	// page's left alignment. w-100 scopes this to buttons acting as rows.
	buttonRe := regexp.MustCompile(`(?is)<button[^>]*class="([^"]*)"[^>]*>`)

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
		for _, m := range buttonRe.FindAllSubmatch(b, -1) {
			classes := map[string]bool{}
			for _, c := range strings.Fields(string(m[1])) {
				classes[c] = true
			}
			if classes["d-flex"] && classes["w-100"] && !classes["text-start"] {
				t.Errorf("%s: full-width flex <button> is missing text-start — a button "+
					"centers its text, so this row's label sits centered while the rows "+
					"around it are left-aligned. Found class=%q", rel, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// findByClass returns every element carrying class.
func findByClass(n *html.Node, class string) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && hasClass(n, class) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

// hasClass reports whether element n's class attribute includes class.
func hasClass(n *html.Node, class string) bool {
	for _, a := range n.Attr {
		if a.Key == "class" && slices.Contains(strings.Fields(a.Val), class) {
			return true
		}
	}
	return false
}

// TestNoPaddinglessLinkButtons enforces the .btn-link trap: Tabler's
// .btn-link clears the button's background at rest but not on hover, so it
// still gets .btn's hover fill. With its padding stripped (p-0), that fill
// hugs the text edge to edge. A link-looking control inside text is a
// .link-button (app.css) instead, which has no fill at all.
func TestNoPaddinglessLinkButtons(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	classRe := regexp.MustCompile(`(?is)<(?:button|a)\b[^>]*class="([^"]*)"`)
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
		for _, m := range classRe.FindAllSubmatch(b, -1) {
			classes := map[string]bool{}
			for _, c := range strings.Fields(string(m[1])) {
				classes[c] = true
			}
			if classes["btn-link"] && (classes["p-0"] || classes["px-0"]) {
				t.Errorf("%s: .btn-link without padding gets a hover fill that hugs its "+
					"text; use .link-button for a link-looking control inside text. "+
					"Found class=%q", rel, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// TestHiddenElementsHaveNoDisplayUtility enforces the [hidden] trap: Tabler's
// display utilities (.d-flex, .d-block, .d-md-grid, …) are display:…
// !important, and so is [hidden]{display:none}. Same specificity, and the
// utilities come later, so an element carrying both stays on screen while its
// hidden property says otherwise — nothing errors, and a test that reads the
// property passes. Put hidden on a wrapper without a display utility instead.
//
// It checks elements hidden in the markup, and the ones listfilter.js hides at
// runtime (filter rows and the no-matches note).
func TestHiddenElementsHaveNoDisplayUtility(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)
	tagRe := regexp.MustCompile(`(?is)<[a-z][a-z0-9-]*\b[^>]*>`)
	classRe := regexp.MustCompile(`(?is)\bclass="([^"]*)"`)
	hiddenRe := regexp.MustCompile(`(?is)\s(?:hidden|data-filter-item|data-filter-empty)(?:[\s=>/]|$)`)
	displayRe := regexp.MustCompile(`^d(?:-(?:sm|md|lg|xl|xxl))?-(?:inline|inline-block|block|grid|inline-grid|table|table-row|table-cell|flex|inline-flex)$`)

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
		// Template actions go first, so `{{if .X}} hidden{{end}}` reads as hidden.
		src := templateAction.ReplaceAllString(string(b), "")
		for _, tag := range tagRe.FindAllString(src, -1) {
			if !hiddenRe.MatchString(tag) {
				continue
			}
			m := classRe.FindStringSubmatch(tag)
			if m == nil {
				continue
			}
			for _, c := range strings.Fields(m[1]) {
				if displayRe.MatchString(c) {
					t.Errorf("%s: %s can be hidden but carries %s, whose display:… !important "+
						"beats [hidden] and keeps it on screen. Move hidden to a wrapper.", rel, tag, c)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}
