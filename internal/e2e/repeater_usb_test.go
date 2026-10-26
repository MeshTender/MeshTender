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

// TestE2EApplyConfigOverUSB drives the Configuration tab's USB cable path against
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
	url := srv.appURL + "/repeaters/" + rep.PublicID + "/config"
	const runAllReady = `!document.querySelector('[data-testid="config-run-all"]').disabled`

	t.Run("the right repeater", func(t *testing.T) {
		bctx, cancel, watch := startBrowser(t)
		defer cancel()
		fake := installFakeRepeater(t, bctx, rep.PublicKeyHex)
		ctx, cancelRun := context.WithTimeout(bctx, 45*time.Second)
		defer cancelRun()
		var locked bool
		if err := chromedp.Run(ctx,
			setSessionCookie(cookie),
			chromedp.Navigate(url),
			chromedp.WaitVisible(`[data-testid="config-commands"] button[data-run]`, chromedp.ByQuery),
			chromedp.Click(`[data-testid="config-connect"]`, chromedp.ByQuery),
			chromedp.Poll(runAllReady, nil),
			chromedp.Evaluate(`document.querySelector('[data-testid="transport-air"]').disabled`, &locked),
			chromedp.Click(`[data-testid="config-run-all"]`, chromedp.ByQuery),
			chromedp.Poll(`document.querySelectorAll('.cfg-status.text-success').length === 2`, nil),
			chromedp.WaitVisible(`[data-testid="reboot-prompt"]`, chromedp.ByQuery),
			chromedp.Click(`[data-testid="reboot"]`, chromedp.ByQuery),
			chromedp.Poll(`document.querySelector('[data-testid="reboot-prompt"]').hidden`, nil),
		); err != nil {
			t.Fatalf("browser run: %v (the fake repeater ran %q)", err, fake.lines())
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

	t.Run("a different repeater", func(t *testing.T) {
		bctx, cancel, watch := startBrowser(t)
		defer cancel()
		fake := installFakeRepeater(t, bctx, strings.Repeat("ab", 32))
		ctx, cancelRun := context.WithTimeout(bctx, 45*time.Second)
		defer cancelRun()
		var errText string
		var runAll bool
		if err := chromedp.Run(ctx,
			setSessionCookie(cookie),
			chromedp.Navigate(url),
			chromedp.WaitVisible(`[data-testid="config-commands"] button[data-run]`, chromedp.ByQuery),
			chromedp.Click(`[data-testid="config-connect"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`#log .ev-error`, chromedp.ByQuery),
			chromedp.Text(`#log .ev-error`, &errText, chromedp.ByQuery),
			chromedp.Evaluate(runAllReady, &runAll),
		); err != nil {
			t.Fatalf("browser run: %v (the fake repeater ran %q)", err, fake.lines())
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
