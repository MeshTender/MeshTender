//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
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
	var themed []string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the install page",
			chromedp.Navigate(srv.appURL+"/observers/new"),
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
		// The install dialog needs a serial port, which headless Chrome can't
		// pick; the no-port dialog is the same Material components and needs
		// none. Its color tokens must come from the page's theme, not the
		// light theme esp-web-tools fixes on its :host.
		step("open esp-web-tools' no-port dialog",
			chromedp.Evaluate(noPortDialogTheme, &themed, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
				return p.WithAwaitPromise(true)
			}),
			chromedp.WaitVisible(`ewt-no-port-picked-dialog`, chromedp.ByQuery)),
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
	if len(themed) != 2 || themed[0] == "" || themed[0] != themed[1] {
		t.Errorf("no-port dialog surface = %q, want the page's --tblr-bg-surface", themed)
	}
	if version != "v1.18.0.4313" {
		t.Errorf("version hint = %q", version)
	}
}

// noPortDialogTheme opens esp-web-tools' no-port dialog and returns its
// --md-sys-color-surface next to the page's --tblr-bg-surface.
const noPortDialogTheme = `(async () => {
	const src = document.querySelector('script[type="module"][src*="esp-web-tools"]').src;
	const m = await import(new URL("no-port-picked-DPUnbpty.js", src).href);
	m.openNoPortPickedDialog(() => {});
	await customElements.whenDefined("ewt-no-port-picked-dialog");
	const d = document.querySelector("ewt-no-port-picked-dialog");
	return [getComputedStyle(d).getPropertyValue("--md-sys-color-surface").trim(),
	        getComputedStyle(document.body).getPropertyValue("--tblr-bg-surface").trim()];
})()`

// TestE2EAddObserverByKey adds an observer through the form on Add an observer,
// lands on its page, and removes it through the data-confirm form, back on the
// Nodes list — all under the strict CSP.
func TestE2EAddObserverByKey(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eaddobserver")
	key := strings.Repeat("ef", 32)

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	acceptDialogs(bctx)

	var heading, notHeard string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open Add an observer",
			chromedp.Navigate(srv.appURL+"/observers/new"),
			chromedp.WaitVisible(`#observer-key`, chromedp.ByID)),
		step("add it by key",
			chromedp.SendKeys(`#observer-key`, key, chromedp.ByID),
			chromedp.Submit(`#observer-key`, chromedp.ByID),
			chromedp.WaitVisible(`[data-testid="observer-status"]`, chromedp.ByQuery),
			chromedp.Text(`h1`, &heading, chromedp.ByQuery),
			chromedp.Text(`[data-testid="observer-not-heard"]`, &notHeard, chromedp.ByQuery)),
		step("remove it",
			chromedp.Click(`[data-testid="observer-remove"]`, chromedp.ByQuery),
			waitForLocation(srv.appURL+"/nodes"),
			chromedp.WaitVisible(`body`, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("add observer: %v", err)
	}
	watch.assertClean(t)

	if heading != "Observer" || notHeard != "Not heard yet" {
		t.Errorf("observer page: heading %q, badge %q", heading, notHeard)
	}
	if left, _ := srv.store.ListObserversForUser(srv.ctx, user.ID); len(left) != 0 {
		t.Error("observer not removed")
	}
}
