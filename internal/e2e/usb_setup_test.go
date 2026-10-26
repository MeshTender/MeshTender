//go:build browser

package e2e

import (
	"context"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EUSBSetupRunsEachCommand is the regression for USB setup sending
// "command\n": the firmware runs a line only on "\r", so every command ran
// together and the name came out as "MT Testset prv.key …". Against a fake
// repeater that follows the firmware's serial loop, setup must:
//   - wait out the board's reset on open before sending anything;
//   - run each command as its own line;
//   - write MeshCore's expanded private key, so the repeater derives the public
//     key MeshTender records (checked with meshcore-go);
//   - leave the firmware time to save MeshTender's access before rebooting.
func TestE2EUSBSetupRunsEachCommand(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "usb-setup")

	bctx, cancel, watch := startBrowser(t)
	defer cancel()
	acceptDialogs(bctx)
	fake := installFakeRepeater(t, bctx, strings.Repeat("ab", 32))

	runCtx, cancelRun := context.WithTimeout(bctx, 60*time.Second)
	defer cancelRun()
	var failure string
	if err := chromedp.Run(runCtx,
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/repeaters/add?step=serial&method=serial"),
		chromedp.WaitVisible(`#generate`, chromedp.ByQuery),
		// The random key is generated asynchronously on load.
		chromedp.Poll(`/^[0-9a-f]{64}$/.test(document.getElementById("pubkey").textContent)`, nil),
		chromedp.SendKeys(`#name`, "MT Test", chromedp.ByQuery),
		chromedp.Click(`#generate`, chromedp.ByQuery),
		chromedp.WaitVisible(`#run`, chromedp.ByQuery),
		chromedp.Click(`#run`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run: %v (the fake repeater ran %q)", err, fake.lines())
	}

	// Saving navigates the page away, so wait on the record rather than the page.
	var reps []*store.Repeater
	for deadline := time.Now().Add(40 * time.Second); len(reps) == 0; {
		if time.Now().After(deadline) {
			_ = chromedp.Run(bctx, chromedp.Evaluate(`Array.from(document.querySelectorAll("#log .ev-error")).map(function (e) { return e.textContent; }).join("; ")`, &failure))
			t.Fatalf("no repeater saved; errors %q; the fake repeater ran %q", failure, fake.lines())
		}
		time.Sleep(200 * time.Millisecond)
		var err error
		if reps, err = srv.store.ListRepeatersForUser(srv.ctx, user.ID); err != nil {
			t.Fatal(err)
		}
	}
	got := fake.lines()
	want := []*regexp.Regexp{
		regexp.MustCompile(`^ver$`),
		regexp.MustCompile(`^set name MT Test$`),
		regexp.MustCompile(`^set prv\.key [0-9a-f]{128}$`),
		regexp.MustCompile(`^set radio [0-9.]+,[0-9.]+,\d+,\d+$`),
		regexp.MustCompile(`^setperm [0-9a-f]{64} 3$`),
		regexp.MustCompile(`^reboot$`),
	}
	if len(got) != len(want) {
		t.Fatalf("the repeater ran %d lines, want %d: %q", len(got), len(want), got)
	}
	for i, re := range want {
		if !re.MatchString(got[i]) {
			t.Errorf("line %d = %q, want %s", i, got[i], re)
		}
	}

	if len(reps) != 1 {
		t.Fatalf("repeaters after setup = %d, want 1", len(reps))
	}
	prv, err := hex.DecodeString(strings.TrimPrefix(got[2], "set prv.key "))
	if err != nil {
		t.Fatal(err)
	}
	id, err := meshcore.NewLocalIdentityFromExpandedKey(prv)
	if err != nil {
		t.Fatal(err)
	}
	if derived := strings.ToLower(id.String()); derived != reps[0].PublicKeyHex {
		t.Errorf("the key written derives public key %s, but MeshTender recorded %s", derived, reps[0].PublicKeyHex)
	}

	perm, _ := fake.at("setperm ")
	reboot, _ := fake.at("reboot")
	if gap := reboot.Sub(perm); gap < 5*time.Second {
		t.Errorf("rebooted %v after setperm; the firmware needs 5s to save its access list", gap)
	}
	watch.assertClean(t)
}
