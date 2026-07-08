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

// TestE2EWizardManageAccessModal verifies the add-repeater wizard's final step
// hosts the same "Manage access" modal as the share page: the button opens the
// shared modal (Bootstrap under the strict CSP) and htmx loads the org-limits
// fragment into it. Guards the fix for the wizard's old dead-route opt-out button.
func TestE2EWizardManageAccessModal(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ewizard")

	// CreateOrg makes the creator a member, so the new repeater participates and
	// the wizard lists it with a Manage access button.
	if _, err := srv.store.CreateOrg(srv.ctx, "Wizard Org", user.ID); err != nil {
		t.Fatalf("create org: %v", err)
	}
	rep, err := srv.store.CreateRepeater(srv.ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Rep", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	addedURL := srv.appURL + "/repeaters/" + rep.PublicID + "/added"
	var hasSwitch bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(addedURL),
		chromedp.WaitVisible(`[data-testid="manage-access"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="manage-access"]`, chromedp.ByQuery),
		// htmx loads the org-limits fragment: the Shared switch + command grid.
		chromedp.WaitVisible(`#limits-modal-content [data-check-scope]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('#limits-modal-content input[name=include]')`, &hasSwitch),
	); err != nil {
		t.Fatalf("browser run against %s: %v", addedURL, err)
	}
	if !hasSwitch {
		t.Fatal("manage-access modal missing the Shared switch")
	}
	watch.assertClean(t)
}
