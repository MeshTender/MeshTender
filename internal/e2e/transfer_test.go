//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// TestE2ETransferOwnership drives the whole handover in a real browser: from the
// sharing page, through the htmx-loaded modal and its steward picker, past the
// window.confirm gate, to the repeater page confirming it took effect.
//
// The modal and the confirm gate are the parts Go tests can't reach — Bootstrap
// and htmx running under the strict CSP, plus ui.js's [data-confirm] — so a
// template that forgot a hook (or a CSP that blocked the script) would still
// pass every handler test while being unusable in a browser.
func TestE2ETransferOwnership(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "e2exferowner")
	rep := srv.newRepeater(t, owner.ID, "Handover Rep")

	steward, err := srv.store.CreateUser(srv.ctx, "e2esuccessor", "")
	if err != nil {
		t.Fatalf("create steward: %v", err)
	}
	if _, err := srv.store.AddShare(srv.ctx, rep.ID, steward.ID); err != nil {
		t.Fatalf("add share: %v", err)
	}
	if err := srv.store.SetShareSteward(srv.ctx, rep.ID, steward.ID, true); err != nil {
		t.Fatalf("set steward: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	acceptDialogs(bctx)

	shareURL := srv.appURL + "/repeaters/" + rep.PublicID + "/share"
	repeaterURL := srv.appURL + "/repeaters/" + rep.PublicID
	var flash string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		page.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(shareURL),
		// The handover has to be reachable from where stewards are managed: the
		// button opens the shared modal and htmx fills it with the picker.
		chromedp.Click(`[data-testid="transfer-ownership"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#transfer-modal-content [data-testid="steward-choice"]`, chromedp.ByQuery),
		chromedp.Click(`#transfer-modal-content [data-testid="confirm-transfer"]`, chromedp.ByQuery),
		waitForLocation(repeaterURL),
		chromedp.Text(`.alert-success`, &flash, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", shareURL, err)
	}

	if flash == "" {
		t.Fatal("no confirmation banner after the transfer")
	}
	// The visible outcome must match the stored one.
	got, err := srv.store.GetRepeaterOwned(srv.ctx, steward.ID, rep.ID)
	if err != nil || got.OwnerID != steward.ID {
		t.Fatalf("ownership did not move to the steward: %v", err)
	}
	if isSteward, _ := srv.store.IsSteward(srv.ctx, rep.ID, owner.ID); !isSteward {
		t.Fatal("outgoing owner was not left a steward")
	}
	watch.assertClean(t)
}
