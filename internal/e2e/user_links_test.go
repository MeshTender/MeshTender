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
	sel := `#user-links-rows .link-row select[name="link_platform"]`
	urlInput := `#user-links-rows .link-row input[name="link_url"]`
	submit := `#user-links-form button[type="submit"]`

	var inlineErr, afterBlockURL string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(accountURL),
		chromedp.WaitVisible(`#user-links-form`, chromedp.ByQuery),
		chromedp.Click(`#add-user-link`, chromedp.ByQuery),
		chromedp.WaitVisible(sel, chromedp.ByQuery),
		chromedp.SetValue(sel, "email", chromedp.ByQuery),
		chromedp.SetValue(urlInput, "notanemail", chromedp.ByQuery),
		chromedp.Click(submit, chromedp.ByQuery),
		// The blocked submit reveals an inline error on the row rather than reloading.
		chromedp.WaitVisible(`#user-links-rows .link-row .link-error`, chromedp.ByQuery),
		chromedp.Text(`#user-links-rows .link-row .link-error`, &inlineErr, chromedp.ByQuery),
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

	watch.assertClean(t)
}
