//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EUserLinksClientValidation drives the profile-links editor on the auth
// host in a real browser, under the strict CSP. It proves the client-side
// validation added alongside the server fix actually runs: an invalid row blocks
// the submit and shows an inline error (so the user never round-trips and loses
// their work), while a scheme-less domain is accepted and saved as https:// —
// matching the server's normalization rather than being wrongly rejected.
func TestE2EUserLinksClientValidation(t *testing.T) {
	// The account page lives on the auth surface, so put that on the
	// browser-reachable host for this test.
	srv := newE2EServer(t, authReachableHosts())
	user, cookie := srv.login(t, "linkse2e")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	accountURL := srv.browserURL + "/account"

	// Phase 1: an invalid email row must block the submit client-side. We assert
	// the inline error appears and that we never navigated (no ?ok, no server
	// success banner) — i.e. the JS called preventDefault.
	sel := `[data-link-rows] .link-row .link-platform`
	urlInput := `[data-link-rows] .link-row .link-value`
	submit := `form[data-link-editor] button[type="submit"]`

	var inlineErr, afterBlockURL string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(accountURL),
		chromedp.WaitVisible(`form[data-link-editor]`, chromedp.ByQuery),
		chromedp.Click(`[data-link-add]`, chromedp.ByQuery),
		chromedp.WaitVisible(sel, chromedp.ByQuery),
		chromedp.SetValue(sel, "email", chromedp.ByQuery),
		chromedp.SetValue(urlInput, "notanemail", chromedp.ByQuery),
		chromedp.Click(submit, chromedp.ByQuery),
		// The blocked submit reveals an inline error on the row rather than reloading.
		chromedp.WaitVisible(`[data-link-rows] .link-row .link-error`, chromedp.ByQuery),
		chromedp.Text(`[data-link-rows] .link-row .link-error`, &inlineErr, chromedp.ByQuery),
		chromedp.Location(&afterBlockURL),
	); err != nil {
		t.Fatalf("phase 1 (invalid row) against %s: %v", accountURL, err)
	}
	if !strings.Contains(inlineErr, "Enter a valid email address.") {
		t.Fatalf("inline error = %q, want the email validation message", inlineErr)
	}
	if strings.Contains(afterBlockURL, "ok=") {
		t.Fatalf("submit was not blocked: navigated to %q", afterBlockURL)
	}

	// Phase 2: fix the row to a bare-domain URL. The client must NOT block it, the
	// form submits, and the server stores it normalized to https://.
	if err := chromedp.Run(bctx,
		chromedp.SetValue(sel, "website", chromedp.ByQuery),
		chromedp.SetValue(urlInput, "example.com", chromedp.ByQuery),
		chromedp.Click(submit, chromedp.ByQuery),
		// A successful save redirects back with the success flash.
		chromedp.WaitVisible(`.alert-success`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("phase 2 (valid bare domain) against %s: %v", accountURL, err)
	}

	links, err := srv.store.ListUserLinks(srv.ctx, user.ID)
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	if len(links) != 1 || links[0].URL != "https://example.com" {
		t.Fatalf("stored links = %+v, want one https://example.com", links)
	}

	// Phase 3: add a GitHub row and type a bare "@handle" — the redesign accepts it
	// and the server canonicalises to the profile URL. Reload first to a clean page
	// so the success banner we wait on is this save's, not phase 2's leftover.
	if err := chromedp.Run(bctx,
		chromedp.Navigate(accountURL),
		chromedp.WaitVisible(`form[data-link-editor]`, chromedp.ByQuery),
		chromedp.Click(`[data-link-add]`, chromedp.ByQuery),
		// The new row is the last one; target its controls.
		chromedp.SetValue(`[data-link-rows] .link-row:last-child .link-platform`, "github", chromedp.ByQuery),
		chromedp.SetValue(`[data-link-rows] .link-row:last-child .link-value`, "@octocat", chromedp.ByQuery),
		chromedp.Click(submit, chromedp.ByQuery),
		chromedp.WaitVisible(`.alert-success`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("phase 3 (github handle) against %s: %v", accountURL, err)
	}
	links, err = srv.store.ListUserLinks(srv.ctx, user.ID)
	if err != nil {
		t.Fatalf("list links after github: %v", err)
	}
	var gotGitHub string
	for _, l := range links {
		if l.Platform == "github" {
			gotGitHub = l.URL
		}
	}
	if gotGitHub != "https://github.com/octocat" {
		t.Fatalf("stored github link = %q, want https://github.com/octocat (all: %+v)", gotGitHub, links)
	}

	watch.assertClean(t)
}
