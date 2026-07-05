//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EOrgActionsMenuLimitCommands verifies the org page's "Actions" dropdown
// opens under the strict CSP (Bootstrap's data-bs-toggle, no inline JS) and that
// its "Limit commands" item navigates to the org-scoped my-commands page — the
// new home for that org-wide, per-member setting after it moved off the
// per-repeater details page.
func TestE2EOrgActionsMenuLimitCommands(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2euser")

	// CreateOrg makes the creator an admin member, so the member view (with the
	// Actions dropdown) renders.
	org, err := srv.store.CreateOrg(srv.ctx, "Actions Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	orgURL := srv.appURL + "/orgs/" + org.Slug
	var landedURL string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(orgURL),
		// Open the dropdown (exercises Bootstrap JS under the CSP), then click the
		// Limit commands item once the menu is shown.
		chromedp.Click(`.dropdown-toggle`, chromedp.ByQuery),
		chromedp.WaitVisible(`.dropdown-menu.show a[href$="/my-commands"]`, chromedp.ByQuery),
		chromedp.Click(`.dropdown-menu.show a[href$="/my-commands"]`, chromedp.ByQuery),
		// The my-commands page renders the command sections behind its form.
		chromedp.WaitVisible(`#cmdform-perms`, chromedp.ByQuery),
		chromedp.Location(&landedURL),
	); err != nil {
		t.Fatalf("browser run against %s: %v", orgURL, err)
	}

	if !strings.HasSuffix(landedURL, "/orgs/"+org.Slug+"/my-commands") {
		t.Fatalf("Actions → Limit commands landed on %q, want the org my-commands page", landedURL)
	}
	watch.assertClean(t)
}
