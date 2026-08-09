//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestFooterSourceLinkOnEverySurface: the footer's source link is how the running
// service satisfies AGPL section 13, so it has to be present and clickable on all
// three hosts — including the sign-in host, whose centered layout builds its own
// page chrome rather than reusing the standard one.
//
// A browser test rather than only a Go one because the obligation is about what a
// visitor can actually reach: a link present in the HTML but overlaid, empty, or
// dropped by a layout that forgot the footer would satisfy a string assertion and
// not a person.
func TestFooterSourceLinkOnEverySurface(t *testing.T) {
	srv := newE2EServer(t)

	const wantHref = "https://github.com/MeshTender/MeshTender"

	for _, tc := range []struct {
		name string
		url  string
	}{
		{"root", srv.rootURL + "/"},
		{"signin", srv.authURL + "/login"},
		{"signup", srv.authURL + "/signup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bctx, cancel, watch := startBrowser(t)
			defer cancel()

			var href, text string
			if err := chromedp.Run(bctx,
				network.Enable(),
				cdplog.Enable(),
				chromedp.Navigate(tc.url),
				// Waiting on visibility is the point: it fails if a layout renders the
				// footer off-page or collapsed rather than merely including the markup.
				chromedp.WaitVisible(`footer a[href*="github.com"]`, chromedp.ByQuery),
				chromedp.AttributeValue(`footer a[href*="github.com"]`, "href", &href, nil, chromedp.ByQuery),
				chromedp.Text(`footer a[href*="github.com"]`, &text, chromedp.ByQuery),
			); err != nil {
				t.Fatalf("%s: %v", tc.url, err)
			}
			watch.assertClean(t)

			if href != wantHref {
				t.Errorf("source link href = %q, want %q", href, wantHref)
			}
			if text != "Source" {
				t.Errorf("source link text = %q, want %q", text, "Source")
			}
		})
	}
}
