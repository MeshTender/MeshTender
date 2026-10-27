//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EOrgNavigation walks the org tabs as an admin under the strict CSP:
// Check-ins switches between Nets and Member activity, and Settings between its
// sections, each keeping its top-level tab active. Home links to the
// recommended configuration once there is some, which opens outside Settings
// for a plain member.
func TestE2EOrgNavigation(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eorgtabs")
	// CreateOrg makes the creator an admin member, so Settings shows.
	org, err := srv.store.CreateOrg(srv.ctx, "Tabs Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	activeTab := `document.querySelector('[data-testid="org-tabs"] .nav-link.active').dataset.testid`
	var afterActivity, afterObservers, publicHref string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug),
		chromedp.WaitVisible(`[data-testid="org-public-page"]`, chromedp.ByQuery),
		chromedp.AttributeValue(`[data-testid="org-public-page"]`, "href", &publicHref, nil, chromedp.ByQuery),
		chromedp.Click(`[data-testid="org-tab-checkins"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="org-checkins-nav"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="org-subnav-activity"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="org-subnav-activity"][aria-current="page"]`, chromedp.ByQuery),
		chromedp.Evaluate(activeTab, &afterActivity),
		chromedp.Click(`[data-testid="org-tab-settings"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="settings-profile"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="org-settings-mqtt"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="broker-card"]`, chromedp.ByQuery),
		chromedp.Evaluate(activeTab, &afterObservers),
	); err != nil {
		t.Fatalf("navigate org tabs: %v", err)
	}
	if afterActivity != "org-tab-checkins" || afterObservers != "org-tab-settings" {
		t.Errorf("active tab: activity %q, observers %q", afterActivity, afterObservers)
	}
	if !strings.HasSuffix(publicHref, "/orgs/"+org.Slug) || !strings.HasPrefix(publicHref, srv.rootURL) {
		t.Errorf("public page link = %q, want the root host's org page", publicHref)
	}

	// With some config, Home links a plain member to the recommended
	// configuration: Home stays the active tab and there's no Settings.
	if _, err := srv.store.CreateProfile(srv.ctx, org.ID, "Std", nil); err != nil {
		t.Fatal(err)
	}
	member, memberCookie := srv.login(t, "e2eorgtabsmember")
	if err := srv.store.AddOrgMember(srv.ctx, org.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	var memberTab string
	var settingsShown bool
	if err := chromedp.Run(bctx,
		setSessionCookie(memberCookie),
		chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug),
		chromedp.WaitVisible(`[data-testid="org-config-card"] [data-testid="org-config-link"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="org-config-card"] [data-testid="org-config-link"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.breadcrumb`, chromedp.ByQuery),
		chromedp.Evaluate(activeTab, &memberTab),
		chromedp.Evaluate(`!!document.querySelector('[data-testid="org-tab-settings"]')`, &settingsShown),
	); err != nil {
		t.Fatalf("member to recommended configuration: %v", err)
	}
	if memberTab != "org-tab-home" || settingsShown {
		t.Errorf("member on recommended config: active %q, Settings shown %v", memberTab, settingsShown)
	}
	watch.assertClean(t)
}
