//go:build browser

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EApplyConfigOverUSB drives the Configure tab's USB cable path against
// the fake repeater: connecting checks the device is this repeater and locks the
// "Send with" choice, Run all sends the organization's profile through its serial
// CLI, and a reply that needs a reboot offers one. A different repeater is
// refused before anything is sent.
func TestE2EApplyConfigOverUSB(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "usb-apply")
	rep := srv.newRepeater(t, owner.ID, "Hilltop")
	org, err := srv.store.CreateOrg(srv.ctx, "USB Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	profile := []store.ProfileInput{{Name: "Base", Steps: []store.ConfigStep{
		{CommandLine: "set radio 910.525,62.5,7,5"},
		{CommandLine: "set tx 20"},
	}}}
	if err := srv.store.ReplaceOrgConfig(srv.ctx, org.ID, profile, nil); err != nil {
		t.Fatal(err)
	}
	url := srv.appURL + "/repeaters/" + rep.PublicID + "/configure?pane=configuration"
	const runAllReady = `!document.querySelector('[data-testid="config-run-all"]').disabled`
	// connectWithin bounds connecting to the fake repeater, which is slow on
	// purpose: like a board that resets when its port opens, it ignores input for
	// its first 1.2s, so the first "ver" can go unanswered and cost serial-cli's
	// 3s reply wait plus its 1s pause before asking again (~5s in all).
	const connectWithin = 20 * time.Second

	t.Run("the right repeater", func(t *testing.T) {
		bctx, cancel, watch := startBrowser(t)
		defer cancel()
		fake := installFakeRepeater(t, bctx, rep.PublicKeyHex)
		var locked bool
		if err := chromedp.Run(bctx,
			setSessionCookie(cookie),
			step("open the configuration pane",
				chromedp.Navigate(url),
				chromedp.WaitVisible(`[data-testid="config-commands"] button[data-run]`, chromedp.ByQuery)),
			step("choose the USB cable",
				chromedp.Click(`label[for="transport-usb"]`, chromedp.ByQuery)),
			stepWithin("connect and check it's this repeater", connectWithin,
				chromedp.Click(`[data-testid="connect"]`, chromedp.ByQuery),
				chromedp.Poll(runAllReady, nil)),
			step("see the KISS modem choice locked",
				chromedp.Evaluate(`document.querySelector('[data-testid="transport-air"]').disabled`, &locked)),
			step("run all the profile's commands",
				chromedp.Click(`[data-testid="config-run-all"]`, chromedp.ByQuery),
				chromedp.Poll(`document.querySelectorAll('.cfg-status.text-success').length === 2`, nil)),
			step("reboot when offered",
				chromedp.WaitVisible(`[data-testid="reboot-prompt"]`, chromedp.ByQuery),
				chromedp.Click(`[data-testid="reboot"]`, chromedp.ByQuery),
				chromedp.Poll(`document.querySelector('[data-testid="reboot-prompt"]').hidden`, nil)),
		); err != nil {
			t.Fatalf("browser run: %v (the fake repeater ran %q; page: %s)", err, fake.lines(), configureState(bctx))
		}
		if !locked {
			t.Error("the KISS modem choice stayed available while the USB connection was live")
		}
		want := "ver|get public.key|set radio 910.525,62.5,7,5|set tx 20|reboot"
		if got := strings.Join(fake.lines(), "|"); got != want {
			t.Errorf("the repeater ran %q, want %q", got, want)
		}
		watch.assertClean(t)
	})

	// The point of one tab: a connection made once serves the command line and
	// the organization configuration alike, across pane switches, until you
	// disconnect it.
	t.Run("one connection for the console and the configuration", func(t *testing.T) {
		bctx, cancel, watch := startBrowser(t)
		defer cancel()
		fake := installFakeRepeater(t, bctx, rep.PublicKeyHex)
		var status, label string
		if err := chromedp.Run(bctx,
			setSessionCookie(cookie),
			step("open the Configure tab",
				chromedp.Navigate(srv.appURL+"/repeaters/"+rep.PublicID+"/configure"),
				chromedp.WaitVisible(`[data-testid="connect"]`, chromedp.ByQuery)),
			step("choose the USB cable",
				chromedp.Click(`label[for="transport-usb"]`, chromedp.ByQuery)),
			stepWithin("connect and check it's this repeater", connectWithin,
				chromedp.Click(`[data-testid="connect"]`, chromedp.ByQuery),
				chromedp.Poll(`!document.getElementById("cmdsend").disabled`, nil)),
			step("send a command from the console",
				chromedp.SendKeys(`#cmdinput`, "get name\r", chromedp.ByQuery),
				chromedp.Poll(`Array.from(document.querySelectorAll("#log .ev-sent")).some(function (e) { return /get name/.test(e.textContent); })`, nil)),
			step("switch to the configuration on the same connection",
				chromedp.Click(`[data-testid="pane-configuration"]`, chromedp.ByQuery),
				chromedp.Poll(runAllReady, nil)),
			step("run all the profile's commands",
				chromedp.Click(`[data-testid="config-run-all"]`, chromedp.ByQuery),
				chromedp.Poll(`document.querySelectorAll('.cfg-status.text-success').length === 2`, nil),
				chromedp.Text(`[data-testid="conn-status"]`, &status, chromedp.ByQuery)),
			step("disconnect",
				chromedp.Click(`[data-testid="connect"]`, chromedp.ByQuery),
				chromedp.Poll(`document.getElementById("cmdsend").disabled`, nil),
				chromedp.Text(`#connect-label`, &label, chromedp.ByQuery)),
		); err != nil {
			t.Fatalf("browser run: %v (the fake repeater ran %q; page: %s)", err, fake.lines(), configureState(bctx))
		}
		want := "ver|get public.key|get name|set radio 910.525,62.5,7,5|set tx 20"
		if got := strings.Join(fake.lines(), "|"); got != want {
			t.Errorf("the repeater ran %q, want %q (one connection, no second identity check)", got, want)
		}
		if status != "Connected over USB" {
			t.Errorf("status while connected = %q", status)
		}
		if label != "Connect repeater" {
			t.Errorf("after disconnecting the button says %q, want Connect repeater", label)
		}
		watch.assertClean(t)
	})

	t.Run("a different repeater", func(t *testing.T) {
		bctx, cancel, watch := startBrowser(t)
		defer cancel()
		fake := installFakeRepeater(t, bctx, strings.Repeat("ab", 32))
		var errText string
		var runAll bool
		if err := chromedp.Run(bctx,
			setSessionCookie(cookie),
			step("open the configuration pane",
				chromedp.Navigate(url),
				chromedp.WaitVisible(`[data-testid="config-commands"] button[data-run]`, chromedp.ByQuery)),
			step("choose the USB cable",
				chromedp.Click(`label[for="transport-usb"]`, chromedp.ByQuery)),
			stepWithin("connect and be refused as a different repeater", connectWithin,
				chromedp.Click(`[data-testid="connect"]`, chromedp.ByQuery),
				chromedp.WaitVisible(`#log .ev-error`, chromedp.ByQuery),
				chromedp.Text(`#log .ev-error`, &errText, chromedp.ByQuery)),
			step("see Run all still disabled",
				chromedp.Evaluate(runAllReady, &runAll)),
		); err != nil {
			t.Fatalf("browser run: %v (the fake repeater ran %q; page: %s)", err, fake.lines(), configureState(bctx))
		}
		if !strings.Contains(errText, "different repeater") {
			t.Errorf("log error = %q, want it to say it's a different repeater", errText)
		}
		if runAll {
			t.Error("Run all is enabled after connecting to the wrong repeater")
		}
		if got := strings.Join(fake.lines(), "|"); got != "ver|get public.key" {
			t.Errorf("the wrong repeater was sent %q; want only the identity check", got)
		}
		watch.assertClean(t)
	})
}

// configureState describes the Configure tab for a failure message: whether Run
// all is enabled, the connection, the open pane, what the panel says, and the
// log — what a timed-out wait would otherwise leave unexplained.
func configureState(ctx context.Context) string {
	var state string
	// Detached from ctx's deadline: this runs after a failure, which may be the
	// test's own deadline expiring, and an expired context would read nothing.
	// WithoutCancel keeps the tab (ctx's values) while dropping its deadline.
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = chromedp.Run(dctx, chromedp.Evaluate(`JSON.stringify({
		runAllDisabled: (document.querySelector('[data-testid="config-run-all"]') || {}).disabled,
		runButtons: document.querySelectorAll('[data-testid="config-commands"] button[data-run]').length,
		hint: (document.querySelector('[data-testid="config-hint"]') || {}).textContent,
		commands: (document.querySelector('[data-testid="config-commands"]') || {}).textContent,
		status: (document.querySelector('[data-testid="conn-status"]') || {}).textContent,
		transport: window.MeshConsole && window.MeshConsole.name,
		ready: !!(window.MeshConsole && window.MeshConsole.ready),
		pane: (document.querySelector('.tab-pane.active') || {}).id,
		log: Array.from(document.querySelectorAll('#log li')).map(function (l) { return l.textContent; })
	})`, &state))
	return state
}

// TestConfigureStateSurvivesAnExpiredDeadline: the diagnostic runs after a
// failure, which can be the test's own deadline running out. It must still read
// the page then, not come back empty.
func TestConfigureStateSurvivesAnExpiredDeadline(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2ediagnose")
	rep := srv.newRepeater(t, user.ID, "Diagnosed")

	bctx, cancel, _ := startBrowser(t)
	defer cancel()
	if err := chromedp.Run(bctx,
		setSessionCookie(cookie),
		step("open the configuration pane",
			chromedp.Navigate(srv.appURL+"/repeaters/"+rep.PublicID+"/configure?pane=configuration"),
			chromedp.WaitVisible(`.tab-pane.active`, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("open configure: %v", err)
	}
	expired, cancelExpired := context.WithDeadline(bctx, time.Now().Add(-time.Second))
	defer cancelExpired()
	if got := configureState(expired); !strings.Contains(got, `"pane":`) {
		t.Fatalf("configureState under an expired deadline = %q, want the page's state", got)
	}
}
