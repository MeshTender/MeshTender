//go:build browser

package e2e

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// TestE2EOrgMembersFilter: the Members roster's filter bar (listfilter.js) hides
// rows by role and by search. It guards the trap the old page worked around with
// its own script: listfilter hides with [hidden], which a row's own .d-flex
// (display:flex !important, later in the cascade) would beat, leaving every row
// on screen.
func TestE2EOrgMembersFilter(t *testing.T) {
	srv := newE2EServer(t)
	admin, cookie := srv.login(t, "e2emembers-admin")
	org, err := srv.store.CreateOrg(srv.ctx, "Roster Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	member, err := srv.store.CreateUser(srv.ctx, "e2emembers-rita", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.AddOrgMember(srv.ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	const visible = `Array.from(document.querySelectorAll('[data-filter-item]')).filter(function (r) { return r.offsetParent !== null; }).length`
	var all, admins, searched int
	if err := chromedp.Run(bctx,
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/members"),
		chromedp.WaitVisible(`[data-testid="member-role-filter"]`, chromedp.ByQuery),
		chromedp.Evaluate(visible, &all),
		chromedp.SetValue(`[data-testid="member-role-filter"]`, "admin", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-testid="member-role-filter"]').dispatchEvent(new Event("change"))`, nil),
		chromedp.Evaluate(visible, &admins),
		chromedp.SetValue(`[data-testid="member-role-filter"]`, "", chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('[data-testid="member-role-filter"]').dispatchEvent(new Event("change"))`, nil),
		chromedp.SendKeys(`#member-search`, "rita", chromedp.ByQuery),
		chromedp.Evaluate(visible, &searched),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if all != 2 || admins != 1 || searched != 1 {
		t.Errorf("rows on screen: all %d, admins %d, search \"rita\" %d; want 2, 1, 1", all, admins, searched)
	}
	watch.assertClean(t)
}
