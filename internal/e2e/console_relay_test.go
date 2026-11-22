//go:build browser

package e2e

import (
	"crypto/rand"
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"
)

// TestE2EConsoleViaObserver opens the Configure tab with ?via=<observer>. The
// over-the-air option becomes "Observer", connecting needs no serial port (the
// headless browser has none, so without ?via= the button stays disabled), and
// the server's answer reaches the log under the CSP. The user's orgs haven't
// approved this observer, so the answer is a refusal.
func TestE2EConsoleViaObserver(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "owner")
	rep := srv.newRepeater(t, owner.ID, "Hilltop")
	observer, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var label, refusal string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the console through an observer",
			chromedp.Navigate(srv.appURL+"/repeaters/"+rep.PublicID+"/configure?via="+observer.String()),
			chromedp.WaitVisible(`[data-testid="connection"]`, chromedp.ByQuery)),
		step("choose the observer",
			chromedp.Click(`label[for="transport-air"]`, chromedp.ByQuery),
			chromedp.Text(`label[for="transport-air"]`, &label, chromedp.ByQuery)),
		step("connect without a serial port",
			chromedp.WaitEnabled(`[data-testid="connect"]`, chromedp.ByQuery),
			chromedp.Click(`[data-testid="connect"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`#log li.ev-error`, chromedp.ByQuery),
			chromedp.Text(`#log li.ev-error`, &refusal, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if strings.TrimSpace(label) != "Observer" {
		t.Errorf("over-the-air option = %q, want Observer", label)
	}
	if !strings.Contains(refusal, "organization you administer") {
		t.Errorf("log error = %q, want the relay refusal", refusal)
	}
	watch.assertClean(t)
}
