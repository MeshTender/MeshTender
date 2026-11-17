//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// moveState reports each link row's value and whether its up/down buttons are
// disabled, in page order, as "value:up:down" tokens.
const moveState = `Array.prototype.map.call(document.querySelectorAll('[data-link-rows] .link-row'), function (r) {
	return r.querySelector('.link-value').value + ':' + r.querySelector('.move-link-up').disabled + ':' + r.querySelector('.move-link-down').disabled;
}).join(' ')`

// TestE2EOrgLinksReorder: an admin moves an org link up in Settings › Profile
// and the saved order follows, under the strict CSP. The first row can't move
// up, nor the last down (MESHTENDER-11).
func TestE2EOrgLinksReorder(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eorgorder")
	org, err := srv.store.CreateOrg(srv.ctx, "Order Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := srv.store.ReplaceOrgLinks(srv.ctx, org.ID, []store.OrgLink{
		{Platform: "website", URL: "https://first.example"},
		{Platform: "website", URL: "https://second.example"},
	}); err != nil {
		t.Fatalf("seed links: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var before, moved string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open Settings › Profile",
			chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/settings"),
			chromedp.WaitVisible(`[data-testid="settings-links"] .link-row`, chromedp.ByQuery),
			chromedp.Evaluate(moveState, &before)),
		step("move the second link up",
			chromedp.Click(`[data-testid="settings-links"] .link-row:nth-child(2) .move-link-up`, chromedp.ByQuery),
			chromedp.Evaluate(moveState, &moved)),
		step("save",
			reloadedBy(chromedp.Click(`[data-testid="save-links"]`, chromedp.ByQuery))),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if want := "https://first.example:true:false https://second.example:false:true"; before != want {
		t.Errorf("before = %q, want %q", before, want)
	}
	if want := "https://second.example:true:false https://first.example:false:true"; moved != want {
		t.Errorf("after moving = %q, want %q", moved, want)
	}
	links, err := srv.store.ListOrgLinks(srv.ctx, org.ID)
	if err != nil || len(links) != 2 || links[0].URL != "https://second.example" || links[1].URL != "https://first.example" {
		t.Errorf("saved order = %+v, %v; want second, first", links, err)
	}
	watch.assertClean(t)
}

// TestE2EUserLinksReorderKeepsPrimary: moving a profile link up on the account
// page saves the new order, and the primary contact stays with its link rather
// than with its old position.
func TestE2EUserLinksReorderKeepsPrimary(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2euserorder")
	if err := srv.store.ReplaceUserLinks(srv.ctx, user.ID, []store.UserLink{
		{Platform: "website", URL: "https://primary.example", IsPrimary: true},
		{Platform: "website", URL: "https://other.example"},
	}); err != nil {
		t.Fatalf("seed links: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the account page",
			chromedp.Navigate(srv.authURL+"/account"),
			chromedp.WaitVisible(`form[data-link-editor] .link-row`, chromedp.ByQuery)),
		step("move the second link up",
			chromedp.Click(`[data-link-rows] .link-row:nth-child(2) .move-link-up`, chromedp.ByQuery)),
		step("save",
			reloadedBy(chromedp.Click(`[data-testid="profile-save"]`, chromedp.ByQuery))),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	links, err := srv.store.ListUserLinks(srv.ctx, user.ID)
	if err != nil || len(links) != 2 {
		t.Fatalf("saved links = %+v, %v", links, err)
	}
	if links[0].URL != "https://other.example" || links[0].IsPrimary || links[1].URL != "https://primary.example" || !links[1].IsPrimary {
		t.Errorf("saved = %+v; want other then primary.example, still primary", links)
	}
	watch.assertClean(t)
}
