//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2ECreateInvitePage follows Settings › People's "Create link" to its page:
// the label field and command grid render, and the page runs clean under the CSP.
func TestE2ECreateInvitePage(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2einvite")

	rep, err := srv.store.CreateRepeater(srv.ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Rep", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	shareURL := srv.appURL + "/repeaters/" + rep.PublicID + "/settings/people"

	var hasDescription, boxes bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open Settings › People",
			chromedp.Navigate(shareURL),
			chromedp.WaitVisible(`[data-testid="new-invite"]`, chromedp.ByQuery)),
		step("open the new-invite form",
			chromedp.Click(`[data-testid="new-invite"]`, chromedp.ByQuery),
			// htmx loads the fragment: wait for the command grid, then check the fields.
			chromedp.WaitVisible(`[data-testid="invite-form"] [data-check-scope]`, chromedp.ByQuery),
			chromedp.Evaluate(`!!document.querySelector('[data-testid="invite-form"] input[name=description]')`, &hasDescription),
			chromedp.Evaluate(`document.querySelectorAll('[data-testid="invite-form"] input[name=cmd]').length > 0`, &boxes)),
	); err != nil {
		t.Fatalf("browser run against %s: %v", shareURL, err)
	}

	if !hasDescription {
		t.Fatal("Create link page missing the label field")
	}
	if !boxes {
		t.Fatal("Create link page missing command checkboxes")
	}
	watch.assertClean(t)
}
