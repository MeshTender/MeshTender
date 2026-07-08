//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/jleight/meshtender/internal/store"
)

// TestE2ECreateInviteModal drives the share page's "Create single-use link"
// button: it opens the shared modal (Bootstrap data-bs-toggle under the strict
// CSP) and htmx loads the description field + command grid into it. Asserts the
// fragment renders and the page runs clean under the CSP.
func TestE2ECreateInviteModal(t *testing.T) {
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

	shareURL := srv.appURL + "/repeaters/" + rep.PublicID + "/share"

	var hasDescription, boxes bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(shareURL),
		chromedp.WaitVisible(`[data-testid="new-invite"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="new-invite"]`, chromedp.ByQuery),
		// htmx loads the fragment: wait for the command grid, then check the fields.
		chromedp.WaitVisible(`#invite-modal-content [data-check-scope]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('#invite-modal-content input[name=description]')`, &hasDescription),
		chromedp.Evaluate(`document.querySelectorAll('#invite-modal-content input[name=cmd]').length > 0`, &boxes),
	); err != nil {
		t.Fatalf("browser run against %s: %v", shareURL, err)
	}

	if !hasDescription {
		t.Fatal("invite modal missing the description field")
	}
	if !boxes {
		t.Fatal("invite modal missing command checkboxes")
	}
	watch.assertClean(t)
}
