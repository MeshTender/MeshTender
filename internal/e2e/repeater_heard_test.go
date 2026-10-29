//go:build browser

package e2e

import (
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2ERepeaterHeardDetails renders what a repeater's adverts say in its
// Overview details — an old flood advert default, and a flagged location
// that isn't registered, whose warning shows the advertised one on hover —
// then adopts that location from Settings › Details, all under the strict CSP.
func TestE2ERepeaterHeardDetails(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "heardowner")
	rep := srv.newRepeater(t, user.ID, "Hilltop")

	lat, lon := 42.9, -78.8
	at := time.Now().Add(-9 * time.Hour)
	for i := range 3 {
		a := store.RepeaterAdvert{PublicKeyHex: rep.PublicKeyHex, AdvertTS: uint32(at.Unix()), //nolint:gosec // test clock
			ReceivedAt: at, Flood: true, Name: "Hilltop", PathHashSize: 1, Packet: []byte{0x11}}
		if i == 2 {
			a.Lat, a.Lon = &lat, &lon
		}
		if _, err := srv.store.RecordRepeaterAdvert(srv.ctx, a); err != nil {
			t.Fatal(err)
		}
		at = at.Add(3 * time.Hour)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	page := srv.appURL + "/repeaters/" + rep.PublicID
	var vintage, tip, flash string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(page),
		chromedp.WaitVisible(`[data-testid="repeater-details"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="flood-vintage"]`, &vintage, chromedp.ByQuery),
		// Hovering the location warning shows the advertised location.
		chromedp.ScrollIntoView(`[data-testid="heard-location-differs"]`, chromedp.ByQuery),
		chromedp.Focus(`[data-testid="heard-location-differs"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.tooltip`, chromedp.ByQuery),
		chromedp.Text(`.tooltip`, &tip, chromedp.ByQuery),
		chromedp.Navigate(page+"/settings"),
		chromedp.Click(`[data-testid="adopt-location"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="flash-ok"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="flash-ok"]`, &flash, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", page, err)
	}
	if !strings.Contains(vintage, "Old default") {
		t.Errorf("vintage badge = %q, want an old default", vintage)
	}
	if !strings.Contains(tip, "42.9, -78.8") {
		t.Errorf("location tooltip = %q, want the advertised location", tip)
	}
	if !strings.Contains(flash, "advertised") {
		t.Errorf("flash = %q", flash)
	}
	var differs bool
	if err := chromedp.Run(bctx,
		chromedp.Navigate(page),
		chromedp.WaitVisible(`[data-testid="repeater-details"]`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('[data-testid="heard-location-differs"]')`, &differs),
	); err != nil {
		t.Fatal(err)
	}
	if differs {
		t.Error("location still flagged after adopting it")
	}
	watch.assertClean(t)
}
