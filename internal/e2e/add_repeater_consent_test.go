//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EAddRepeaterConsentLocationDisclosure verifies the add-repeater consent
// step discloses that MeshTender will read the repeater's location (get lat /
// get lon) through the user's modem. It shows for the add-existing (kiss) method
// but NOT the USB-setup (serial) method, which sets the location explicitly. The
// page must also render under the strict CSP.
func TestE2EAddRepeaterConsentLocationDisclosure(t *testing.T) {
	srv := newE2EServer(t)
	_, cookie := srv.login(t, "adder")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const disclosure = `!!Array.prototype.find.call(document.querySelectorAll('.alert-title'), function (h) { return /read the repeater's location/i.test(h.textContent); })`

	cases := map[string]bool{"kiss": true, "serial": false} // method → disclosure expected
	for method, want := range cases {
		var shown bool
		if err := chromedp.Run(bctx,
			network.Enable(),
			cdplog.Enable(),
			setSessionCookie(cookie),
			chromedp.Navigate(srv.appURL+"/repeaters/add?step=consent&method="+method),
			chromedp.WaitVisible(`#ack`, chromedp.ByQuery),
			chromedp.Evaluate(disclosure, &shown),
		); err != nil {
			t.Fatalf("browser run (method=%s): %v", method, err)
		}
		if shown != want {
			t.Errorf("consent step (method=%s): location disclosure shown=%v, want %v", method, shown, want)
		}
	}

	watch.assertClean(t)
}
