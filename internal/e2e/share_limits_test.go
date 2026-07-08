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

// TestE2EShareOrgLimitsModal drives the share page's per-org "Edit limits" modal:
// the button opens the shared modal (Bootstrap data-bs-toggle under the strict
// CSP) and htmx loads the command-grid fragment into it. It then exercises the
// per-section Select none control — which comes from the delegated ui.js handler,
// proving it works on htmx-injected content (a modal fragment can't run inline
// script under the CSP). Also asserts the page runs clean under the CSP.
func TestE2EShareOrgLimitsModal(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2elimit")

	// CreateOrg makes the creator an admin member; the owner's repeater then
	// participates in it automatically, so the org row (with Edit limits) renders.
	if _, err := srv.store.CreateOrg(srv.ctx, "Limits Org", user.ID); err != nil {
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

	shareURL := srv.appURL + "/repeaters/" + rep.PublicID + "/share"

	// Count checkboxes per section and how many are checked, scoped to the modal.
	const countChecked = `(function () {
		var scopes = document.querySelectorAll('#limits-modal-content [data-check-scope]');
		return Array.prototype.map.call(scopes, function (s) {
			var boxes = s.querySelectorAll('input[type=checkbox]');
			var checked = 0;
			Array.prototype.forEach.call(boxes, function (b) { if (b.checked) checked++; });
			return [boxes.length, checked];
		});
	})()`

	var initial, afterNone [][]int
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(shareURL),
		// Open the per-org limits modal; htmx loads the command grid into it.
		chromedp.WaitVisible(`[data-testid="manage-access"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="manage-access"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#limits-modal-content [data-check-scope]`, chromedp.ByQuery),
		chromedp.Evaluate(countChecked, &initial),
		// Default is permissive: everything checked. Uncheck only the first section.
		chromedp.Click(`#limits-modal-content [data-check-scope]:first-of-type [data-check-none]`, chromedp.ByQuery),
		chromedp.Evaluate(countChecked, &afterNone),
	); err != nil {
		t.Fatalf("browser run against %s: %v", shareURL, err)
	}

	if len(initial) < 2 {
		t.Fatalf("expected >=2 command sections in the modal, got %d", len(initial))
	}
	for i, s := range initial {
		if s[0] == 0 || s[1] != s[0] {
			t.Fatalf("section %d: expected all %d boxes checked initially, got %d", i, s[0], s[1])
		}
	}
	// Select none scoped to section 0: only section 0 clears.
	if afterNone[0][1] != 0 {
		t.Fatalf("section 0: Select none left %d boxes checked, want 0", afterNone[0][1])
	}
	for i := 1; i < len(afterNone); i++ {
		if afterNone[i][1] != afterNone[i][0] {
			t.Fatalf("section %d: unscoped Select none changed it (%d/%d)", i, afterNone[i][1], afterNone[i][0])
		}
	}
	watch.assertClean(t)
}
