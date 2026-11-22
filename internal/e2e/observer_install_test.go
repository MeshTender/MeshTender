//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EObserverInstallPage loads the observer install page under the real
// CSP: esp-web-tools' install button (an ES module that loads more modules)
// registers without a violation, its activate slot holds our button, and
// choosing another role points the install button at that board's manifest.
// Flashing itself needs a board on a serial port, so it isn't covered here.
func TestE2EObserverInstallPage(t *testing.T) {
	srv := newE2EServer(t)
	_, cookie := srv.login(t, "e2einstaller")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var defined bool
	var manifest, repeaterManifest, version string
	var repeaterBoardsVisible bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the install page",
			chromedp.Navigate(srv.appURL+"/observers/install"),
			chromedp.WaitVisible(`[data-testid="firmware-install-button"]`, chromedp.ByQuery),
			chromedp.Poll(`customElements.get("esp-web-install-button") !== undefined`, &defined),
			chromedp.AttributeValue(`[data-testid="firmware-install-button"]`, "manifest", &manifest, nil, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="firmware-install-button"] button[slot="activate"]`, chromedp.ByQuery)),
		step("choose the repeater role",
			chromedp.SetValue(`#fw-role`, "repeater", chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector("#fw-role").dispatchEvent(new Event("change"))`, nil),
			chromedp.AttributeValue(`[data-testid="firmware-install-button"]`, "manifest", &repeaterManifest, nil, chromedp.ByQuery),
			chromedp.Evaluate(`!document.querySelector('[data-fw-role="repeater"]').hidden && document.querySelector('[data-fw-role="room_server"]').hidden`, &repeaterBoardsVisible),
			chromedp.Text(`[data-fw-version]`, &version, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("install page: %v", err)
	}
	watch.assertClean(t)

	if !defined {
		t.Error("esp-web-install-button was never defined")
	}
	if manifest != "/firmware/heltec_v4_room_server_observer_mqtt/manifest.json" {
		t.Errorf("initial manifest = %q, want the room server's", manifest)
	}
	if repeaterManifest != "/firmware/heltec_v3_repeater_observer_mqtt/manifest.json" {
		t.Errorf("manifest after choosing repeater = %q", repeaterManifest)
	}
	if !repeaterBoardsVisible {
		t.Error("choosing repeater didn't swap the board list")
	}
	if version != "v1.18.0.4313" {
		t.Errorf("version hint = %q", version)
	}
}
