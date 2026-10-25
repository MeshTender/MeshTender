//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// pickPreset selects a preset in the radio picker the way a user does: set the
// value and fire change (SetValue alone fires nothing).
func pickPreset(id string) chromedp.Action {
	return chromedp.Evaluate(`(() => { const s = document.getElementById("radio-preset");
		s.value = "`+id+`"; s.dispatchEvent(new Event("change", {bubbles: true})); })()`, nil)
}

// editRadioField types a new value into one radio field, firing input.
func editRadioField(id, value string) chromedp.Action {
	return chromedp.Evaluate(`(() => { const f = document.getElementById("`+id+`");
		f.value = "`+value+`"; f.dispatchEvent(new Event("input", {bubbles: true})); })()`, nil)
}

// radioState reads the picker and the four fields as "preset freq bw sf cr".
const radioState = `["radio-preset","radio_freq_mhz","radio_bw_khz","radio_sf","radio_cr"]
	.map(id => document.getElementById(id).value).join(" ")`

// TestE2EOrgRadioSettings: an admin sets the org's radio from the Configuration
// page. The editor loads into a modal via htmx, and the delegated preset script
// (radio-preset.js) still drives the swapped-in picker under the CSP: a preset
// fills the fields, editing one moves the picker to Custom. Saving shows the
// values on the page. The same shared picker drives the add-repeater form.
func TestE2EOrgRadioSettings(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eradio")
	org, err := srv.store.CreateOrg(srv.ctx, "Radio Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var afterPreset, afterEdit, card string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/config"),
		chromedp.WaitVisible(`[data-testid="config-radio-edit"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="config-radio-edit"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#config-radio-modal-content #radio-preset`, chromedp.ByQuery),
		pickPreset("ch"),
		chromedp.Evaluate(radioState, &afterPreset),
		editRadioField("radio_sf", "9"),
		chromedp.Evaluate(radioState, &afterEdit),
		chromedp.Click(`[data-testid="save-radio"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="config-radio-values"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="config-radio-values"]`, &card, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("set org radio: %v", err)
	}
	if afterPreset != "ch 869.618 62.5 8 8" {
		t.Errorf("after picking Switzerland: %q", afterPreset)
	}
	if afterEdit != "custom 869.618 62.5 9 8" {
		t.Errorf("after editing SF: %q", afterEdit)
	}
	if !strings.Contains(card, "869.618 MHz · BW62.5 · SF9 · CR8") {
		t.Errorf("radio card = %q", card)
	}

	var addForm string
	if err := chromedp.Run(bctx,
		chromedp.Navigate(srv.appURL+"/repeaters/add?step=details&method=kiss"),
		chromedp.WaitVisible(`#radio-preset`, chromedp.ByQuery),
		pickPreset("nz"),
		chromedp.Evaluate(radioState, &addForm),
	); err != nil {
		t.Fatalf("add-repeater preset: %v", err)
	}
	if addForm != "nz 917.375 250 11 5" {
		t.Errorf("add-repeater after picking New Zealand: %q", addForm)
	}
	watch.assertClean(t)
}
