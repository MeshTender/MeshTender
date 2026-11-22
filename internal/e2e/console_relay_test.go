//go:build browser

package e2e

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/observe"
	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EConsoleThroughObserver picks an observer from the Configure tab's
// "Send with" list. Connecting needs no serial port (the headless browser has
// none, so the KISS modem can't connect), and the server's answer reaches the
// log under the CSP. The org's broker is on loopback, which the relay's
// address guard refuses, so the answer is that refusal.
func TestE2EConsoleThroughObserver(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "owner")
	rep := srv.newRepeater(t, owner.ID, "Hilltop")
	observer, err := meshcore.GenerateLocalIdentity(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	org, err := srv.store.CreateOrg(srv.ctx, "Relay Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.CreateOrgObserver(srv.ctx, org.ID, "Roof", observer.String(), owner.ID); err != nil {
		t.Fatal(err)
	}
	status := &observe.Status{FirmwareVersion: "v1.18.0.4313-meshtender-6626f0c8"}
	if err := srv.store.ReportObserver(srv.ctx, org.ID, observer.String(), "BUF", time.Now(), status); err != nil {
		t.Fatal(err)
	}
	broker := store.OrgBroker{OrgID: org.ID, URL: "ws://127.0.0.1:1/mqtt", TopicPrefix: "meshcore", Enabled: true}
	if err := srv.store.UpsertOrgBroker(srv.ctx, broker, nil, false, owner.ID); err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var option, button, refusal string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the Configure tab",
			chromedp.Navigate(srv.appURL+"/repeaters/"+rep.PublicID+"/configure"),
			chromedp.WaitVisible(`[data-testid="transport"]`, chromedp.ByQuery)),
		step("choose the observer",
			chromedp.TextContent(`[data-testid="transport"] optgroup option`, &option, chromedp.ByQuery),
			chooseTransport("observer:"+observer.String()),
			chromedp.Text(`#connect-label`, &button, chromedp.ByQuery)),
		step("connect without a serial port",
			chromedp.WaitEnabled(`[data-testid="connect"]`, chromedp.ByQuery),
			chromedp.Click(`[data-testid="connect"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`#log li.ev-error`, chromedp.ByQuery),
			chromedp.Text(`#log li.ev-error`, &refusal, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if want := "Roof (Relay Org)"; strings.TrimSpace(option) != want {
		t.Errorf("observer option = %q, want %q", option, want)
	}
	if button != "Connect observer" {
		t.Errorf("button = %q, want Connect observer", button)
	}
	if !strings.HasPrefix(refusal, "Couldn't reach the observer") {
		t.Errorf("log error = %q, want the relay's dial refusal", refusal)
	}
	watch.assertClean(t)
}
