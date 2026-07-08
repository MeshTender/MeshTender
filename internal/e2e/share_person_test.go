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

// TestE2EPersonAccessModal drives the People-with-access "Manage access" button:
// it opens the shared modal (Bootstrap under the strict CSP) and htmx loads the
// steward toggle + command grid. Confirms the steward switch and grid render and
// the page runs clean under the CSP.
func TestE2EPersonAccessModal(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "e2eperson")
	rep, err := srv.store.CreateRepeater(srv.ctx, &store.Repeater{
		OwnerID: owner.ID, Name: "Rep", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	// A person with accepted access shows up in the People-with-access list.
	sharee, err := srv.store.CreateUser(srv.ctx, "e2esharee", "")
	if err != nil {
		t.Fatalf("create sharee: %v", err)
	}
	if _, err := srv.store.AddShare(srv.ctx, rep.ID, sharee.ID); err != nil {
		t.Fatalf("add share: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	shareURL := srv.appURL + "/repeaters/" + rep.PublicID + "/share"
	var hasSteward bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(shareURL),
		chromedp.WaitVisible(`[data-testid="manage-person"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="manage-person"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#person-modal-content [data-check-scope]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('#person-modal-content input[name=steward]')`, &hasSteward),
	); err != nil {
		t.Fatalf("browser run against %s: %v", shareURL, err)
	}
	if !hasSteward {
		t.Fatal("person-access modal missing the steward switch")
	}
	watch.assertClean(t)
}
