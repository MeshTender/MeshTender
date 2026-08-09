//go:build browser

package e2e

import (
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2ENoLeftGutterWhenScrollbarAppears: a page long enough to scroll must still
// start at the left edge of the window.
//
// Tabler ships `@media (min-width:992px) { :root { margin-left: calc(100vw - 100%) } }`
// — 100vw counts the scrollbar and 100% doesn't, so any scrollable page gets a left
// margin the width of the scrollbar, sliding the whole layout (navbar included) right
// and leaving an unpainted strip down the left edge. app.css overrides it and reserves
// the space with scrollbar-gutter instead.
//
// It has to be a browser test: it only reproduces at a viewport wide enough for the
// media query AND tall enough content to produce a real scrollbar, which is also why
// it looked page-specific — the short tabs of the same section were fine.
func TestE2ENoLeftGutterWhenScrollbarAppears(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "gutteruser")
	org, err := srv.store.CreateOrg(srv.ctx, "Gutter Org", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	// Enough rows that the list is taller than the window.
	for i := 0; i < 30; i++ {
		id, err := meshcore.GenerateLocalIdentity(rand.Reader)
		if err != nil {
			t.Fatalf("generate identity: %v", err)
		}
		if _, err := srv.store.CreateRepeater(srv.ctx, &store.Repeater{
			OwnerID: owner.ID, Name: fmt.Sprintf("Rep %02d", i), PublicKeyHex: id.String(),
			RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
		}); err != nil {
			t.Fatalf("create repeater: %v", err)
		}
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var scrollbar, navLeft int
	var htmlMargin string
	if err := chromedp.Run(bctx,
		// Above the 992px breakpoint the Tabler rule applies; below it, it doesn't.
		chromedp.EmulateViewport(1280, 700),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/repeaters"),
		chromedp.WaitVisible(`#rep-list`, chromedp.ByQuery),
		chromedp.Evaluate(`window.innerWidth - document.documentElement.clientWidth`, &scrollbar),
		chromedp.Evaluate(`getComputedStyle(document.documentElement).marginLeft`, &htmlMargin),
		chromedp.Evaluate(`Math.round(document.querySelector('header.navbar').getBoundingClientRect().left)`, &navLeft),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	watch.assertClean(t)

	// Guard the guard: with no scrollbar the margin is 0 anyway and this proves nothing.
	if scrollbar == 0 {
		t.Skip("no classic scrollbar in this browser, so the rule cannot misfire")
	}
	if htmlMargin != "0px" {
		t.Errorf("html margin-left = %s with a %dpx scrollbar; the page is pushed off the left edge",
			htmlMargin, scrollbar)
	}
	if navLeft != 0 {
		t.Errorf("navbar starts at x=%d, want 0 — there is a gutter down the left of the page", navLeft)
	}
}
