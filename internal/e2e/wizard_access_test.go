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

// TestE2EWizardManageAccessPage verifies the add-repeater wizard's final step
// links each organization's "Manage access" to its page in Settings ›
// Organizations, which renders and runs clean under the CSP. Guards the fix for
// the wizard's old dead-route opt-out button.
func TestE2EWizardManageAccessPage(t *testing.T) {
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
		step("open the wizard's last step",
			chromedp.Navigate(addedURL),
			chromedp.WaitVisible(`[data-testid="manage-access"]`, chromedp.ByQuery)),
		step("open the organization's access page",
			chromedp.Click(`[data-testid="manage-access"]`, chromedp.ByQuery),
			// htmx loads the org-limits fragment: the Shared switch + command grid.
			chromedp.WaitVisible(`[data-testid="org-access-form"] [data-check-scope]`, chromedp.ByQuery),
			chromedp.Evaluate(`!!document.querySelector('[data-testid="org-access-form"] input[name=include]')`, &hasSwitch)),
	); err != nil {
		t.Fatalf("browser run against %s: %v", addedURL, err)
	}
	if !hasSwitch {
		t.Fatal("organization access page missing the Shared switch")
	}
	watch.assertClean(t)
}
