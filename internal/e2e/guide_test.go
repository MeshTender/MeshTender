//go:build browser

package e2e

import (
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EGuide: a visitor opens an org's Guide from its public page, reads how
// to get on the air — the preset to pick and the nets to check in to — and moves
// to Repeater setup from the Guide's rail, all under the strict CSP. The rail's
// active item stays inside the rail's card: Tabler's transparent list group pulls
// items out by a card body's padding, which outside a card spilled past the page.
func TestE2EGuide(t *testing.T) {
	srv := newE2EServer(t)
	user, _ := srv.login(t, "e2eguide")
	org, err := srv.store.CreateOrg(srv.ctx, "Guide Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	radio := config.RadioDefaults{FreqHz: 910_525_000, BwHz: 62_500, SF: 7, CR: 5}
	if err := srv.store.SetOrgRadio(srv.ctx, org.ID, &radio); err != nil {
		t.Fatalf("set radio: %v", err)
	}
	now := time.Now()
	start := now.Add(2 * time.Hour).UTC()
	if _, err := srv.store.CreateNet(srv.ctx, org.ID, store.NetInput{Name: "Weekly Check-in", Weekday: start.Weekday(),
		StartMinute: start.Hour()*60 + start.Minute(), DurationMinutes: 60, Timezone: "UTC", Active: true}, user.ID, now); err != nil {
		t.Fatalf("create net: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	// The active rail item's box against its card's.
	const inCard = `(function () {
		var item = document.querySelector('[data-testid="org-guide-nav"] .list-group-item.active').getBoundingClientRect();
		var card = document.querySelector('[data-testid="org-guide-rail"]').closest('.card').getBoundingClientRect();
		return item.left >= card.left && item.right <= card.right;
	})()`
	var radioText, checkin, section string
	var guideInCard, setupInCard bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		step("open the Guide from the public page",
			chromedp.Navigate(srv.rootURL+"/orgs/"+org.Slug),
			chromedp.Click(`[data-testid="org-tab-guide"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="guide-radio"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="guide-radio"]`, &radioText, chromedp.ByQuery),
			chromedp.Text(`[data-testid="guide-checkin"]`, &checkin, chromedp.ByQuery),
			chromedp.Evaluate(inCard, &guideInCard)),
		step("move to Repeater setup",
			chromedp.Click(`[data-testid="org-guide-repeaters"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="org-guide-repeaters"][aria-current="page"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="guide-section-title"]`, &section, chromedp.ByQuery),
			chromedp.Evaluate(inCard, &setupInCard)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if !strings.Contains(radioText, "USA/Canada") || !strings.Contains(radioText, "910.525 MHz") {
		t.Errorf("Set your radio = %q, want the USA/Canada preset and its frequency", radioText)
	}
	if !strings.Contains(checkin, "Weekly Check-in") {
		t.Errorf("Check in = %q, want the net", checkin)
	}
	if !strings.Contains(section, "Repeater setup") {
		t.Errorf("after the rail click, heading = %q, want Repeater setup", section)
	}
	if !guideInCard || !setupInCard {
		t.Errorf("active rail item spills out of the rail's card (Get on the air %v, Repeater setup %v)", guideInCard, setupInCard)
	}
	watch.assertClean(t)
}
