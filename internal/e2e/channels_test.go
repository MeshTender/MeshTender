//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EChannels: an admin adds a hashtag channel on Settings › Channels, and
// the Guide lists it with its derived key and a QR code that actually renders —
// all under the strict CSP.
func TestE2EChannels(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2echannels")
	org, err := srv.store.CreateOrg(srv.ctx, "Channel Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var list, key string
	var qrDrawn bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the new-channel page",
			chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/settings/channels/new"),
			chromedp.WaitVisible(`#channel_name`, chromedp.ByQuery)),
		step("add #wny",
			chromedp.SendKeys(`#channel_name`, "#wny", chromedp.ByQuery),
			reloadedBy(chromedp.Click(`[data-testid="channel-save"]`, chromedp.ByQuery)),
			chromedp.Text(`[data-testid="channel-list"]`, &list, chromedp.ByQuery)),
		step("find it in the Guide",
			chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/guide"),
			chromedp.WaitVisible(`[data-testid="guide-channel"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="guide-channel-key"]`, &key, chromedp.ByQuery),
			chromedp.Poll(`(function () { var i = document.querySelector('[data-testid="guide-channel"] img'); return !!i && i.complete && i.naturalWidth > 0; })()`, &qrDrawn)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if !strings.Contains(list, "#wny") {
		t.Errorf("channel list = %q, want #wny", list)
	}
	if key != "ad145f5860c081d04dadd28f7a0b3cde" { // the key the MeshCore app shows for #wny
		t.Errorf("Guide key = %q", key)
	}
	if !qrDrawn {
		t.Error("the channel's QR code didn't render")
	}
	watch.assertClean(t)
}
