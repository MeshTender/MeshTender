//go:build browser

package e2e

import (
	"testing"

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
		// WaitVisible on the checkbox itself proves it rendered and is visible.
		err := chromedp.Run(bctx,
			network.Enable(),
			cdplog.Enable(),
			setSessionCookie(cookie),
			step("open the "+c.name+" step",
				chromedp.Navigate(srv.appURL+c.url),
				chromedp.WaitVisible(c.selector, chromedp.ByQuery)),
		)
		if err != nil {
			t.Errorf("%s step: public-page checkbox (%s) not visible: %v", c.name, c.selector, err)
		}
	}

	watch.assertClean(t)
}
