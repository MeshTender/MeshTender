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

// TestE2EStewardSwitchCollapsesCommands: a steward already runs every command, so
// the per-command grid is moot while the switch is on and the modal collapses it.
// Browser-only behavior worth pinning — it's a delegated ui.js handler acting on an
// htmx-swapped fragment, which is exactly the combination that breaks silently
// (a fragment can't run inline script, and the strict CSP forbids one anyway).
func TestE2EStewardSwitchCollapsesCommands(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "e2esteward")
	rep, err := srv.store.CreateRepeater(srv.ctx, &store.Repeater{
		OwnerID: owner.ID, Name: "Rep", PublicKeyHex: strings.Repeat("b", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	sharee, err := srv.store.CreateUser(srv.ctx, "e2estewardee", "")
	if err != nil {
		t.Fatalf("create sharee: %v", err)
	}
	if _, err := srv.store.AddShare(srv.ctx, rep.ID, sharee.ID); err != nil {
		t.Fatalf("add share: %v", err)
	}
	// Already a steward, so the fragment arrives with the switch checked: the
	// collapse has to be applied on swap, not only on a change event.
	if err := srv.store.SetShareSteward(srv.ctx, rep.ID, sharee.ID, true); err != nil {
		t.Fatalf("set steward: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const visible = `(() => {
	  const el = document.querySelector('#person-commands');
	  return !!el && !el.hidden;
	})()`

	shareURL := srv.appURL + "/repeaters/" + rep.PublicID + "/share"
	var shownAsSteward, shownAfterOff, shownAfterOn bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(shareURL),
		chromedp.WaitVisible(`[data-testid="manage-person"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="manage-person"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#person-modal-content input[name=steward]`, chromedp.ByQuery),
		chromedp.Evaluate(visible, &shownAsSteward),
		// Turning steward off reveals the grants that apply instead.
		chromedp.Click(`#person-modal-content input[name=steward]`, chromedp.ByQuery),
		chromedp.Evaluate(visible, &shownAfterOff),
		chromedp.Click(`#person-modal-content input[name=steward]`, chromedp.ByQuery),
		chromedp.Evaluate(visible, &shownAfterOn),
	); err != nil {
		t.Fatalf("browser run against %s: %v", shareURL, err)
	}
	if shownAsSteward {
		t.Error("command grid is visible for an existing steward; the swapped-in fragment was not synced")
	}
	if !shownAfterOff {
		t.Error("turning steward off did not reveal the per-command grants")
	}
	if shownAfterOn {
		t.Error("turning steward back on did not collapse the per-command grants")
	}
	watch.assertClean(t)
}
