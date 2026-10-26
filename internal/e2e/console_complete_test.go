//go:build browser

package e2e

import (
	"strings"
	"testing"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// TestE2EConsoleTabCompletion: the console's command input completes from the
// allowed commands with Tab, before any modem is connected: as far as every match
// agrees, then through the choices a placeholder lists ("set repeat <on|off>").
// It never traps focus (WCAG 2.1.2): with nothing to complete, or after Escape,
// Tab moves on. A chip fills the input and lists its arguments, and the chip
// filter narrows the list.
func TestE2EConsoleTabCompletion(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "console-owner")
	rep := srv.newRepeater(t, owner.ID, "Hilltop") // the owner may run every command

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const (
		in      = `#cmdinput`
		value   = `document.querySelector("#cmdinput").value`
		listed  = `Array.from(document.querySelectorAll("#cmdsuggest:not([hidden]) code")).map(function (c) { return c.textContent; })`
		focused = `document.activeElement ? document.activeElement.id : ""`
		visible = `document.querySelectorAll('[data-testid="allowed-commands"] .chip:not([hidden])').length`
	)
	var afterSet, afterShared, afterRepeat, afterOn, afterChip string
	var repeatChoices, chipArgs []string
	var focusEmpty, focusEscaped, afterEscaped string
	var sendDisabled bool
	var allChips, filtered int
	if err := chromedp.Run(bctx,
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/repeaters/"+rep.PublicID+"/console"),
		chromedp.WaitVisible(in, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("#cmdsend").disabled`, &sendDisabled),

		chromedp.SendKeys(in, "set rep", chromedp.ByQuery),
		chromedp.SendKeys(in, kb.Tab, chromedp.ByQuery),
		chromedp.Evaluate(value, &afterSet),
		chromedp.SendKeys(in, kb.Tab, chromedp.ByQuery), // as far as "on" and "off" agree
		chromedp.Evaluate(value, &afterShared),
		chromedp.SendKeys(in, kb.Tab, chromedp.ByQuery), // then through the choices
		chromedp.Evaluate(listed, &repeatChoices),
		chromedp.Evaluate(value, &afterRepeat),
		chromedp.SendKeys(in, kb.Escape, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector("#cmdinput").value = "set repeat o"`, nil),
		chromedp.SendKeys(in, "f", chromedp.ByQuery),
		chromedp.SendKeys(in, kb.Tab, chromedp.ByQuery),
		chromedp.Evaluate(value, &afterOn),

		chromedp.Evaluate(`document.querySelector("#cmdinput").value = ""`, nil),
		chromedp.Click(`.chip[data-template="set tx <0-22>"]`, chromedp.ByQuery),
		chromedp.Evaluate(value, &afterChip),
		chromedp.Evaluate(listed, &chipArgs),

		// Tab never traps focus: on an empty input it moves on, and after Escape
		// it moves on even with something left to complete.
		chromedp.Evaluate(`document.querySelector("#cmdinput").value = ""`, nil),
		chromedp.Focus(in, chromedp.ByQuery),
		chromedp.SendKeys(in, kb.Tab, chromedp.ByQuery),
		chromedp.Evaluate(focused, &focusEmpty),
		chromedp.Focus(in, chromedp.ByQuery),
		chromedp.SendKeys(in, "get fl", chromedp.ByQuery),
		chromedp.SendKeys(in, kb.Escape, chromedp.ByQuery),
		chromedp.SendKeys(in, kb.Tab, chromedp.ByQuery),
		chromedp.Evaluate(focused, &focusEscaped),
		chromedp.Evaluate(value, &afterEscaped),

		chromedp.Evaluate(visible, &allChips),
		chromedp.SendKeys(`[data-testid="command-filter"]`, "flood", chromedp.ByQuery),
		chromedp.Evaluate(visible, &filtered),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}

	if !sendDisabled {
		t.Error("Send is enabled with no modem connected")
	}
	if afterSet != "set repeat " {
		t.Errorf(`"set rep" + Tab = %q, want "set repeat "`, afterSet)
	}
	if afterShared != "set repeat o" {
		t.Errorf(`second Tab = %q, want "set repeat o"`, afterShared)
	}
	if strings.Join(repeatChoices, ",") != "set repeat on,set repeat off" || afterRepeat != "set repeat on" {
		t.Errorf("third Tab: list %q, input %q; want both choices listed and the first taken", repeatChoices, afterRepeat)
	}
	if afterOn != "set repeat off" {
		t.Errorf(`"set repeat of" + Tab = %q, want "set repeat off"`, afterOn)
	}
	if afterChip != "set tx " || len(chipArgs) != 1 || chipArgs[0] != "set tx <0-22>" {
		t.Errorf("chip: input %q, list %q; want the command filled in and its template listed", afterChip, chipArgs)
	}
	if focusEmpty == "cmdinput" {
		t.Error("Tab on an empty input kept focus in it")
	}
	if focusEscaped == "cmdinput" || afterEscaped != "get fl" {
		t.Errorf("Esc then Tab: focus on %q, input %q; want focus moved on and the text left alone", focusEscaped, afterEscaped)
	}
	if filtered == 0 || filtered >= allChips {
		t.Errorf("filter \"flood\" left %d of %d chips", filtered, allChips)
	}
	watch.assertClean(t)
}
