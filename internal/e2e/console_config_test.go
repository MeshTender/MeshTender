//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/geo"
	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EConsoleConfigPanel drives the "Apply organization configuration" panel:
// the button expands it, the recommended commands (profile base settings + region
// commands for the repeater's location) render, and — with no modem connected —
// the Run controls are disabled with a prompt to connect. Also asserts the page
// (Bootstrap collapse + MapLibre) runs clean under the strict CSP.
func TestE2EConsoleConfigPanel(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "cfg-owner")
	rep := srv.newRepeater(t, owner.ID, "Owner Rep")

	// Give the repeater a location inside the region below so region commands
	// resolve (there's no modem in headless to fetch it live).
	if err := srv.store.SetRepeaterLocation(srv.ctx, rep.ID, 42.0, -78.0); err != nil {
		t.Fatalf("set location: %v", err)
	}

	org, err := srv.store.CreateOrg(srv.ctx, "Mesh Org", owner.ID) // owner is an admin member
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	profiles := []store.ProfileInput{{Name: "ESP32", Steps: []store.ConfigStep{
		{CommandLine: "set tx 22"},
		{Comment: "tune antenna later"},
	}}}
	regions := []store.RegionInput{{
		Token: "buf", DisplayName: "Buffalo", Layer: 0, AllowFlood: true,
		GeofenceJSON: geo.Rectangle(40, -80, 45, -75),
	}}
	if err := srv.store.ReplaceOrgConfig(srv.ctx, org.ID, profiles, regions); err != nil {
		t.Fatalf("replace config: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	url := srv.appURL + "/repeaters/" + rep.PublicID + "/console"

	// After the panel expands and config.json loads, count rendered command rows,
	// Run buttons, region badges, and whether the Run controls are disabled.
	const summarize = `(function () {
		var box = document.querySelector('[data-testid="config-commands"]');
		var rows = box ? box.querySelectorAll('.list-group-item') : [];
		var runBtns = box ? box.querySelectorAll('button[data-run]') : [];
		var anyRunEnabled = false;
		Array.prototype.forEach.call(runBtns, function (b) { if (!b.disabled) anyRunEnabled = true; });
		var hasSetTx = box ? /set tx 22/.test(box.textContent) : false;
		var hasRegionCmd = box ? /region save/.test(box.textContent) : false;
		return {
			rows: rows.length,
			hasRegionCmd: hasRegionCmd,
			runButtons: runBtns.length,
			anyRunEnabled: anyRunEnabled,
			hasSetTx: hasSetTx,
			runAllDisabled: !!document.querySelector('[data-testid="config-run-all"]').disabled,
			hasConnect: !!document.querySelector('[data-testid="config-connect"]'),
			hint: (document.querySelector('[data-testid="config-hint"]') || {}).textContent || "",
		};
	})()`

	var out struct {
		Rows           int    `json:"rows"`
		HasRegionCmd   bool   `json:"hasRegionCmd"`
		RunButtons     int    `json:"runButtons"`
		AnyRunEnabled  bool   `json:"anyRunEnabled"`
		HasSetTx       bool   `json:"hasSetTx"`
		RunAllDisabled bool   `json:"runAllDisabled"`
		HasConnect     bool   `json:"hasConnect"`
		Hint           string `json:"hint"`
	}
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`[data-testid="apply-config"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="apply-config"]`, chromedp.ByQuery),
		// The command rows render after config.json resolves.
		chromedp.WaitVisible(`[data-testid="config-commands"] .list-group-item`, chromedp.ByQuery),
		chromedp.Evaluate(summarize, &out),
	); err != nil {
		t.Fatalf("browser run against %s: %v", url, err)
	}

	if !out.HasSetTx {
		t.Errorf("profile command 'set tx 22' not shown in the panel")
	}
	if !out.HasRegionCmd {
		t.Errorf("no region commands shown despite a covered location")
	}
	if out.RunButtons == 0 {
		t.Errorf("owner should see Run buttons for permitted commands")
	}
	// No modem is connected in headless, so Run must be disabled with a prompt.
	if out.AnyRunEnabled {
		t.Errorf("Run buttons should be disabled with no modem connected")
	}
	if !out.RunAllDisabled {
		t.Errorf("Run all should be disabled with no modem connected")
	}
	if out.Hint == "" {
		t.Errorf("expected a hint prompting to connect the modem")
	}
	if !out.HasConnect {
		t.Errorf("config panel is missing a Connect button")
	}

	watch.assertClean(t)
}
