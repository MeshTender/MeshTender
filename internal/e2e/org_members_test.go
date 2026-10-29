//go:build browser

package e2e

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
)

// TestE2EOrgMembersFilter: the Members roster's filter bar (listfilter.js) hides
// rows by role and by search, and its sort puts the most recently heard first. It guards the trap the old page worked around with
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
	// Rita's companion has been heard and the admin's hasn't, so "Most recently
	// heard" puts Rita (a member, listed after admins as served) first.
	c, err := srv.store.CreateCompanion(srv.ctx, member.ID, "Rita T1", strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.Pool().Exec(srv.ctx, `UPDATE companions SET last_heard_at = now() WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	const visible = `Array.from(document.querySelectorAll('[data-filter-item]')).filter(function (r) { return r.offsetParent !== null; }).length`
	const firstRow = `document.querySelector('[data-filter-item]').dataset.name`
	var all, admins, searched int
	var servedFirst, heardFirst string
	if err := chromedp.Run(bctx,
		setSessionCookie(cookie),
		step("open the members list",
			chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/members"),
			chromedp.WaitVisible(`[data-testid="member-role-filter"]`, chromedp.ByQuery),
			chromedp.Evaluate(visible, &all),
			chromedp.Evaluate(firstRow, &servedFirst)),
		step("sort by last heard",
			chromedp.SetValue(`[data-testid="member-sort"]`, "heard:desc", chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector('[data-testid="member-sort"]').dispatchEvent(new Event("change"))`, nil),
			chromedp.Evaluate(firstRow, &heardFirst)),
		step("filter to admins",
			chromedp.SetValue(`[data-testid="member-role-filter"]`, "admin", chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector('[data-testid="member-role-filter"]').dispatchEvent(new Event("change"))`, nil),
			chromedp.Evaluate(visible, &admins)),
		step("search by name",
			chromedp.SetValue(`[data-testid="member-role-filter"]`, "", chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector('[data-testid="member-role-filter"]').dispatchEvent(new Event("change"))`, nil),
			chromedp.SendKeys(`#member-search`, "rita", chromedp.ByQuery),
			chromedp.Evaluate(visible, &searched)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if all != 2 || admins != 1 || searched != 1 {
		t.Errorf("rows on screen: all %d, admins %d, search \"rita\" %d; want 2, 1, 1", all, admins, searched)
	}
	if !strings.Contains(servedFirst, "e2emembers-admin") || !strings.Contains(heardFirst, "e2emembers-rita") {
		t.Errorf("first row: as served %q, most recently heard %q; want the admin, then Rita", servedFirst, heardFirst)
	}
	watch.assertClean(t)
}
