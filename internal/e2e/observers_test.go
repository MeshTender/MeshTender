//go:build browser

package e2e

import (
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/observe/observetest"
)

// TestE2EObserverApproval walks Settings › MQTT's observer list: an observer
// discovered from its status report is hidden by the default (Approved)
// filter, shows under Unapproved with its reported name and region, and is
// approved under a new name through its own page, landing back on the list it
// came from.
func TestE2EObserverApproval(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eobservers")
	org, err := srv.store.CreateOrg(srv.ctx, "Observer Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := srv.store.CreateOrgObserver(srv.ctx, org.ID, "Roof", observetest.NewIdentity(t).String(), user.ID); err != nil {
		t.Fatal(err)
	}
	// What the ingester records from a status report it hasn't seen before.
	key := observetest.NewIdentity(t).String()
	if err := srv.store.ReportObserver(srv.ctx, org.ID, key, "SYR", time.Now(), &observe.Status{Name: "Pickle"}); err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	mqtt := srv.appURL + "/orgs/" + org.Slug + "/settings/mqtt"
	var approvedList, unapprovedCount, unapprovedList, location, afterList, afterCount string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(mqtt),
		chromedp.WaitVisible(`[data-testid="observer-list"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="observer-list"]`, &approvedList, chromedp.ByQuery),
		chromedp.Text(`[data-testid="observer-count-unapproved"]`, &unapprovedCount, chromedp.ByQuery),
		chromedp.Click(`[data-testid="observer-filter-unapproved"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="observer-unapproved"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="observer-list"]`, &unapprovedList, chromedp.ByQuery),
		chromedp.Click(`[data-testid="observer-approve"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="observer-approve-form"]`, chromedp.ByQuery),
		chromedp.SetValue(`#observer-name`, "Pickle's roof", chromedp.ByQuery),
		chromedp.Click(`[data-testid="observer-approve-submit"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="observer-list-empty"]`, chromedp.ByQuery),
		chromedp.Location(&location),
		chromedp.Navigate(mqtt),
		chromedp.WaitVisible(`[data-testid="observer-list"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="observer-list"]`, &afterList, chromedp.ByQuery),
		chromedp.Text(`[data-testid="observer-count-approved"]`, &afterCount, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("approve observer: %v", err)
	}
	if !strings.Contains(approvedList, "Roof") || strings.Contains(approvedList, "Pickle") {
		t.Errorf("default (Approved) list: %q", approvedList)
	}
	if unapprovedCount != "1" {
		t.Errorf("unapproved count = %q, want 1", unapprovedCount)
	}
	if !strings.Contains(unapprovedList, "Pickle") || !strings.Contains(unapprovedList, "SYR") || strings.Contains(unapprovedList, "Roof") {
		t.Errorf("Unapproved list: %q", unapprovedList)
	}
	if !strings.Contains(location, "observers=unapproved") {
		t.Errorf("after approving, landed on %q; want the Unapproved list", location)
	}
	if !strings.Contains(afterList, "Pickle's roof") || afterCount != "2" {
		t.Errorf("approved list after approval: %q (count %q)", afterList, afterCount)
	}
	if ok, _ := srv.store.IsOrgObserver(srv.ctx, org.ID, key); !ok {
		t.Error("approved observer doesn't count")
	}
	watch.assertClean(t)
}
