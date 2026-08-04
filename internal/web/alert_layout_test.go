package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// templateAction matches a Go template action. They're stripped before parsing so
// a conditional or a field interpolation inside an alert isn't mistaken for
// content of its own.
var templateAction = regexp.MustCompile(`\{\{[^}]*\}\}`)

// TestAlertBodiesAreSingleChild enforces that a Tabler .alert wraps its body in
// exactly one child element.
//
// Tabler styles .alert as `display:flex; flex-direction:row; gap:1rem`, which
// makes every child node a flex COLUMN. An alert written the obvious way —
// prose with an inline <code> or <strong> in the middle, or a heading above a
// list — therefore renders as several side-by-side columns with 1rem gutters
// instead of flowing or stacking. Nothing errors, no test fails, and the CSP is
// happy; it just looks wrong, which is why this is worth pinning.
//
// The fix is always the same: put the body in a single <div>.
//
//	<div class="alert alert-info" role="alert">
//	  <div>… prose with <code>inline</code> markup …</div>
//	</div>
//
// The one intentional multi-column case is Tabler's icon layout, where a leading
// icon really is meant to sit beside the body:
//
//	<div class="alert alert-warning" role="alert">
//	  <div>{{template "icon-alert" "alert-icon"}}</div>
//	  <div>… body …</div>
//	</div>
func TestAlertBodiesAreSingleChild(t *testing.T) {
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

		// Strip template actions first: `{{if …}}` would otherwise parse as a text
		// node and read as a second child.
		src := templateAction.ReplaceAll(b, nil)
		doc, err := html.Parse(strings.NewReader(string(src)))
		if err != nil {
			t.Errorf("%s: parse: %v", rel, err)
			return nil
		}
		for _, alert := range findAlerts(doc) {
			if n := meaningfulChildren(alert); n > 1 {
				t.Errorf("%s: .alert has %d children, want 1 — .alert is display:flex, so each child "+
					"becomes a side-by-side column. Wrap the body in a single <div>.", rel, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// findAlerts returns every element carrying the bare "alert" class. Modifier
// classes (alert-title, alert-icon, alert-heading) are not flex containers and
// are deliberately excluded.
func findAlerts(n *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for _, a := range n.Attr {
				if a.Key != "class" {
					continue
				}
				for _, c := range strings.Fields(a.Val) {
					if c == "alert" {
						out = append(out, n)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

// meaningfulChildren counts the child nodes that become flex body columns:
// elements and non-whitespace text. Comments and whitespace don't.
//
// An icon slot doesn't either. Tabler's icon layout puts the icon in its own
// leading child, which is meant to be a separate column; since this file strips
// template actions before parsing, that child ({{template "icon-…"}} and nothing
// else) is left empty, and an empty element is how we recognize it.
func meaningfulChildren(n *html.Node) int {
	count := 0
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.ElementNode:
			if !isEmptyElement(c) {
				count++
			}
		case html.TextNode:
			if strings.TrimSpace(c.Data) != "" {
				count++
			}
		}
	}
	return count
}

// isEmptyElement reports whether an element has no element children and no
// non-whitespace text.
func isEmptyElement(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode {
			return false
		}
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) != "" {
			return false
		}
	}
	return true
}
