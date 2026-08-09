package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFilterSidebarsPrecedeTheirList is the same class of trap as the two checks
// in alert_layout_test.go: markup that looks right on the screen it was written on
// and wrong on a narrower one, with nothing to signal it.
//
// A list page puts its search and filter controls in a narrow column beside the
// list. Bootstrap columns stack in SOURCE order on a phone, so writing the list
// first buries the controls underneath it — on /orgs the search box sat below the
// whole roster, meaning the way to shorten a long list was only reachable by
// scrolling past the long list. The same order decides tab order, so a keyboard or
// screen-reader user traverses every row to reach the control that exists to spare
// them exactly that.
//
// The fix is the idiom the list pages already use: put the filter column FIRST and
// send it to the right at lg with order-lg-last.
//
//	<div class="row row-cards" data-filter-root>
//	  <div class="col-lg-3 order-lg-last"> … search + filters … </div>
//	  <div class="col-lg-9"> … the list … </div>
//	</div>
//
// This only looks at columns holding a filter control — a sidebar of maps, links,
// or admins is informational, and reading it after the content it describes is the
// right order on a phone. TestPageNavSidebarsPrecedeContent covers the other kind
// of column that has to come first.
func TestFilterSidebarsPrecedeTheirList(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)

	// A column div and its classes, in source order.
	colRe := regexp.MustCompile(`(?is)<div class="(col-[^"]*)"`)
	// What makes a column a filter sidebar rather than an informational one.
	filterRe := regexp.MustCompile(`(?is)data-filter-search|type="search"`)
	// Wide enough to be the content column (col-lg-5 … col-lg-12).
	contentRe := regexp.MustCompile(`^col-lg-([5-9]|1[0-2])(\s|$)`)

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
		src := string(b)

		cols := colRe.FindAllStringSubmatchIndex(src, -1)
		for i, m := range cols {
			classes := src[m[2]:m[3]]
			// The column's own markup runs until the next column starts.
			end := len(src)
			if i+1 < len(cols) {
				end = cols[i+1][0]
			}
			body := src[m[1]:end]
			if !filterRe.MatchString(body) {
				continue
			}
			// Does a content column appear BEFORE this one in source order?
			for _, prev := range cols[:i] {
				if contentRe.MatchString(src[prev[2]:prev[3]]) {
					t.Errorf("%s: a filter column (%q) comes after the content column %q in source order, "+
						"so on a phone the search and filters stack BELOW the list they filter (and land "+
						"after every row in tab order). Move the filter column first and add order-lg-last.",
						rel, classes, src[prev[2]:prev[3]])
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}

// TestPageNavSidebarsPrecedeContent is the filter-sidebar rule applied to the
// other kind of column that can't live at the bottom: an in-page table of
// contents.
//
// The distinction is function, not appearance. A sidebar of maps, links, or admins
// describes the content, so on a phone it reads perfectly well after it. A list of
// fragment links exists to let someone skip ahead — placed below the content, the
// only way to reach it is to scroll past everything it would have saved them. The
// docs page shipped that way.
//
// Detection is deliberately narrow: a column whose links are ALL same-page anchors
// (href="#…"), which is what a table of contents is and what an informational
// sidebar of profile or repeater links never is.
func TestPageNavSidebarsPrecedeContent(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)

	colRe := regexp.MustCompile(`(?is)<div class="(col-[^"]*)"`)
	anchorRe := regexp.MustCompile(`(?is)<a\s[^>]*href="([^"]*)"`)
	contentRe := regexp.MustCompile(`^col-lg-([5-9]|1[0-2])(\s|$)`)

	// Enough links to be a nav rather than an incidental in-page reference.
	const minNavLinks = 3

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
		src := string(b)

		cols := colRe.FindAllStringSubmatchIndex(src, -1)
		for i, m := range cols {
			classes := src[m[2]:m[3]]
			end := len(src)
			if i+1 < len(cols) {
				end = cols[i+1][0]
			}
			hrefs := anchorRe.FindAllStringSubmatch(src[m[1]:end], -1)
			if len(hrefs) < minNavLinks {
				continue
			}
			allFragments := true
			for _, h := range hrefs {
				if !strings.HasPrefix(h[1], "#") {
					allFragments = false
					break
				}
			}
			if !allFragments {
				continue
			}
			for _, prev := range cols[:i] {
				if contentRe.MatchString(src[prev[2]:prev[3]]) {
					t.Errorf("%s: an in-page nav column (%q) comes after the content column %q in source "+
						"order, so on a phone the table of contents sits below the very content it exists "+
						"to skip. Move it first and add order-lg-last.", rel, classes, src[prev[2]:prev[3]])
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk templates: %v", err)
	}
}
