//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2ESmokeMaintenancePage is the harness "first light": it proves the whole
// browser-test plumbing works end to end — a headless Chrome in a container
// authenticates via an injected session cookie, reaches the app on the Docker
// host, renders a protected page, and runs it under the strict CSP with no
// violations. It asserts nothing about any specific bug.
func TestE2ESmokeMaintenancePage(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "smokeuser")
	rep := srv.newRepeater(t, user.ID, "Smoke Repeater")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	url := srv.appURL + "/repeaters/" + rep.PublicID + "/maintenance"
	var formHTML string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`form[action$="/maintenance"]`, chromedp.ByQuery),
		chromedp.OuterHTML(`form[action$="/maintenance"]`, &formHTML, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", url, err)
	}

	if !strings.Contains(formHTML, "Add entry") {
		t.Fatalf("maintenance page did not render the Add-entry form; got:\n%s", formHTML)
	}
	watch.assertClean(t)
}
