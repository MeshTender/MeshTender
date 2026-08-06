//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EOrgActionsConsistent verifies the shared org header: the Actions menu is
// present on a non-Home org tab (Members) and opens under the strict CSP
// (Bootstrap data-bs-toggle, no inline JS), exposing the management items.
func TestE2EOrgActionsConsistent(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eorgtabs")
	// CreateOrg makes the creator an admin member, so the Actions menu renders.
	org, err := srv.store.CreateOrg(srv.ctx, "Tabs Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	membersURL := srv.appURL + "/orgs/" + org.Slug + "/members"
	var items string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(membersURL),
		// The Actions button lives in the shared header, so it's here on the Members
		// tab (not just Home). Opening it exercises Bootstrap JS under the CSP.
		chromedp.WaitVisible(`[data-testid="org-actions"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="org-actions"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.dropdown-menu.show`, chromedp.ByQuery),
		chromedp.Text(`.dropdown-menu.show`, &items, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", membersURL, err)
	}
	for _, want := range []string{"View public page", "Leave organization"} {
		if !strings.Contains(items, want) {
			t.Errorf("Actions menu on the Members tab missing %q; got %q", want, items)
		}
	}
	// Configuration has its own tab, so it isn't duplicated in this menu.
	if strings.Contains(items, "Edit configuration") {
		t.Errorf("Actions menu still offers Edit configuration; got %q", items)
	}
	watch.assertClean(t)
}
