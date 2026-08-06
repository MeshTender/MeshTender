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

// TestE2EConfigProfileSelect: the profile list on the Configuration page selects a
// profile — clicking a row shows that profile's base settings on the left and marks
// the row active — under the strict CSP.
func TestE2EConfigProfileSelect(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ecfgselect")
	org, err := srv.store.CreateOrg(srv.ctx, "Config Select Org", user.ID) // creator = admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	for _, p := range []struct{ name, comment string }{{"ESP32", "esp base"}, {"nRF52", "nrf base"}} {
		if _, err := srv.store.CreateProfile(srv.ctx, org.ID, p.name, []store.ConfigStep{{Comment: p.comment}}); err != nil {
			t.Fatalf("create profile %s: %v", p.name, err)
		}
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var heading, steps string
	var rowActive bool
	cfgURL := srv.appURL + "/orgs/" + org.Slug + "/config"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(cfgURL),
		chromedp.WaitVisible(`[data-testid="config-profile-list"]`, chromedp.ByQuery),
		// Click the second row's name to select it.
		chromedp.Click(`[data-testid="config-profile-row"]:nth-of-type(2) a`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="config-preview"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="config-preview"] .card-title`, &heading, chromedp.ByQuery),
		chromedp.Text(`[data-testid="config-commands"]`, &steps, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-testid="config-profile-row"]:nth-of-type(2).active')`, &rowActive),
	); err != nil {
		t.Fatalf("browser run against %s: %v", cfgURL, err)
	}
	if !strings.Contains(heading, "nRF52") {
		t.Fatalf("selected profile heading = %q, want it to name nRF52", heading)
	}
	if !strings.Contains(steps, "nrf base") {
		t.Fatalf("selected profile steps = %q, want nRF52's base settings", steps)
	}
	if !rowActive {
		t.Fatal("the selected profile's row should be marked active")
	}
	watch.assertClean(t)
}

// TestE2EConfigProfileModalSave: an admin adds a profile from the Configuration page
// itself — the editor loads into the modal via htmx and saving navigates back to the
// config page with the new profile selected (no separate editor page).
func TestE2EConfigProfileModalSave(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ecfgadd")
	org, err := srv.store.CreateOrg(srv.ctx, "Config Add Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var heading, currentURL string
	cfgURL := srv.appURL + "/orgs/" + org.Slug + "/config"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(cfgURL),
		chromedp.WaitVisible(`[data-testid="config-profile-add"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="config-profile-add"]`, chromedp.ByQuery),
		// The editor arrives in the modal via htmx.
		chromedp.WaitVisible(`#config-profile-modal-content input[name=profile_name]`, chromedp.ByQuery),
		chromedp.SetValue(`#config-profile-modal-content input[name=profile_name]`, "Heltec", chromedp.ByQuery),
		chromedp.SetValue(`#config-profile-modal-content textarea[name=profile_steps]`, "# heltec base", chromedp.ByQuery),
		chromedp.Click(`[data-testid="save-profile"]`, chromedp.ByQuery),
		// HX-Redirect navigates back to the config page, now showing the new profile.
		chromedp.WaitVisible(`[data-testid="config-profile-list"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="config-preview"] .card-title`, &heading, chromedp.ByQuery),
		chromedp.Location(&currentURL),
	); err != nil {
		t.Fatalf("browser run against %s: %v", cfgURL, err)
	}
	if !strings.Contains(heading, "Heltec") {
		t.Fatalf("after saving, left panel heading = %q, want it to name Heltec", heading)
	}
	if !strings.Contains(currentURL, "/config?profile=Heltec") {
		t.Fatalf("after saving, URL = %q, want the config page with Heltec selected", currentURL)
	}
	watch.assertClean(t)
}
