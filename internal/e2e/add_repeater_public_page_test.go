//go:build browser

package e2e

import (
	"context"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EAddRepeaterPublicPageOption verifies the "Publish a public page"
// checkbox is offered on both add-repeater detail steps — the add-existing (kiss)
// form field and the USB-setup (serial) toggle that serial-setup.js reads — so
// the option isn't edit-page-only. Both steps must render clean under the CSP.
func TestE2EAddRepeaterPublicPageOption(t *testing.T) {
	srv := newE2EServer(t)
	_, cookie := srv.login(t, "pager")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	cases := []struct{ name, url, selector string }{
		{"add-existing", "/repeaters/add?step=details&method=kiss", `input[name="expose_public_page"]`},
		{"usb-setup", "/repeaters/add?step=serial&method=serial", `#expose_public_page`},
	}
	for _, c := range cases {
		runCtx, cancelRun := context.WithTimeout(bctx, 30*time.Second)
		// WaitVisible on the checkbox itself proves it rendered and is visible.
		err := chromedp.Run(runCtx,
			network.Enable(),
			cdplog.Enable(),
			setSessionCookie(cookie),
			chromedp.Navigate(srv.appURL+c.url),
			chromedp.WaitVisible(c.selector, chromedp.ByQuery),
		)
		cancelRun()
		if err != nil {
			t.Errorf("%s step: public-page checkbox (%s) not visible: %v", c.name, c.selector, err)
		}
	}

	watch.assertClean(t)
}
