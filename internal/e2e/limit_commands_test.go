//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2ELimitCommandsSelectAll exercises the per-section select-all / select-none
// controls on the "Limit commands" page. It asserts the toggles are scoped to
// their own card: unchecking one section's boxes must leave other sections
// untouched, and re-checking restores them. Also asserts the page runs clean
// under the strict CSP (the toggle is CSP-safe delegated JS, no inline handlers).
func TestE2ELimitCommandsSelectAll(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2euser")

	// The page needs an org the user belongs to; CreateOrg makes the creator an
	// admin member. Command groups come from the migrated catalog ceiling.
	org, err := srv.store.CreateOrg(srv.ctx, "Toggle Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	url := srv.appURL + "/orgs/" + org.Slug + "/my-commands"

	// Count of checkboxes in each section, and how many are checked. Sections are
	// [data-check-scope] cards; each has its own Select all / Select none buttons.
	const countChecked = `(function () {
		var scopes = document.querySelectorAll('[data-check-scope]');
		return Array.prototype.map.call(scopes, function (s) {
			var boxes = s.querySelectorAll('input[type=checkbox]');
			var checked = 0;
			Array.prototype.forEach.call(boxes, function (b) { if (b.checked) checked++; });
			return [boxes.length, checked];
		});
	})()`

	// Every command row carries an Access badge (Members or Admins), the tier
	// column the table was added for. Count how many the table renders.
	const countAccessBadges = `document.querySelectorAll('[data-check-scope] tbody tr td .badge.bg-azure-lt, [data-check-scope] tbody tr td .badge.bg-success-lt').length`

	var initial, afterNone, afterAll [][]int
	var accessBadges int
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`[data-check-scope]`, chromedp.ByQuery),
		chromedp.Evaluate(countChecked, &initial),
		chromedp.Evaluate(countAccessBadges, &accessBadges),
		// Uncheck only the first section.
		chromedp.Click(`[data-check-scope]:first-of-type [data-check-none]`, chromedp.ByQuery),
		chromedp.Evaluate(countChecked, &afterNone),
		// Re-check the first section.
		chromedp.Click(`[data-check-scope]:first-of-type [data-check-all]`, chromedp.ByQuery),
		chromedp.Evaluate(countChecked, &afterAll),
	); err != nil {
		t.Fatalf("browser run against %s: %v", url, err)
	}

	// One Access badge per command row: total should match the total checkbox count.
	totalBoxes := 0
	for _, s := range initial {
		totalBoxes += s[0]
	}
	if accessBadges != totalBoxes {
		t.Fatalf("Access column rendered %d tier badges, want one per command row (%d)", accessBadges, totalBoxes)
	}

	if len(initial) < 2 {
		t.Fatalf("expected at least 2 command sections, got %d", len(initial))
	}
	// Default is permissive: everything checked.
	for i, s := range initial {
		if s[0] == 0 || s[1] != s[0] {
			t.Fatalf("section %d: expected all %d boxes checked initially, got %d checked", i, s[0], s[1])
		}
	}
	// Select none scoped to section 0: only section 0 clears.
	if afterNone[0][1] != 0 {
		t.Fatalf("section 0: Select none left %d boxes checked, want 0", afterNone[0][1])
	}
	for i := 1; i < len(afterNone); i++ {
		if afterNone[i][1] != afterNone[i][0] {
			t.Fatalf("section %d: unscoped Select none changed it (%d/%d checked)", i, afterNone[i][1], afterNone[i][0])
		}
	}
	// Select all restores section 0 to fully checked.
	if afterAll[0][1] != afterAll[0][0] {
		t.Fatalf("section 0: Select all left %d/%d boxes checked, want all", afterAll[0][1], afterAll[0][0])
	}

	watch.assertClean(t)
}
