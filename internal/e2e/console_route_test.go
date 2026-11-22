//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EConsoleRoute covers the route in the connection Options: the org's
// default scope region is preselected in the region list, "Another region…"
// reveals a field to type one, only the chosen route's field shows, and the
// choice is remembered on the next visit.
func TestE2EConsoleRoute(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "owner")
	rep := srv.newRepeater(t, owner.ID, "Hilltop")
	org, err := srv.store.CreateOrg(srv.ctx, "Scoped Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	region := store.RegionInput{Token: "wny", DisplayName: "Western NY", Layer: 1, AllowFlood: true, DefaultScope: true}
	if _, err := srv.store.CreateRegion(srv.ctx, org.ID, region); err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	visible := func(sel string, out *bool) chromedp.Action {
		return chromedp.Evaluate(`(function () { var e = document.querySelector('`+sel+`'); return !!e && e.offsetParent !== null; })()`, out)
	}
	var scopeChecked, scopeShown, typedHidden, typedShown, pathShown, pathChecked, pathShownAgain, scopeShownAgain bool
	var scope string
	url := srv.appURL + "/repeaters/" + rep.PublicID + "/configure"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open the connection options for the KISS modem",
			chromedp.Navigate(url),
			chromedp.WaitVisible(`[data-testid="transport"]`, chromedp.ByQuery),
			chooseTransport("air"),
			chromedp.Click(`[data-testid="conn-options"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="route"]`, chromedp.ByQuery)),
		step("see the default scope preselected",
			chromedp.Evaluate(`document.querySelector('[data-testid="route-scope"]').checked`, &scopeChecked),
			chromedp.Value(`#scope-region`, &scope, chromedp.ByQuery),
			visible(`#scope-region`, &scopeShown),
			visible(`#path`, &pathShown),
			chromedp.Evaluate(`document.getElementById("scope").hidden`, &typedHidden)),
		step("choose another region",
			chromedp.SetValue(`#scope-region`, "", chromedp.ByQuery),
			chromedp.Evaluate(`document.getElementById("scope-region").dispatchEvent(new Event("change"))`, nil),
			visible(`#scope`, &typedShown)),
		step("choose direct through repeaters",
			chromedp.Click(`[data-testid="route-path"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`#path`, chromedp.ByQuery),
			visible(`#scope-region`, &scopeShownAgain)),
		step("come back and find it remembered",
			chromedp.Navigate(url),
			chromedp.WaitVisible(`[data-testid="transport"]`, chromedp.ByQuery),
			chromedp.Click(`[data-testid="conn-options"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`[data-testid="route"]`, chromedp.ByQuery),
			chromedp.Evaluate(`document.querySelector('[data-testid="route-path"]').checked`, &pathChecked),
			visible(`#path`, &pathShownAgain)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if !scopeChecked || scope != "wny" || !scopeShown || pathShown || !typedHidden {
		t.Errorf("default: scope checked %v, region %q, list shown %v; path shown %v; typed field hidden %v", scopeChecked, scope, scopeShown, pathShown, typedHidden)
	}
	if !typedShown {
		t.Error("choosing another region didn't show a field to type it")
	}
	if scopeShownAgain {
		t.Error("the region field still shows after choosing direct")
	}
	if !pathChecked || !pathShownAgain {
		t.Errorf("after reload: path checked %v, shown %v", pathChecked, pathShownAgain)
	}
	watch.assertClean(t)
}

// TestE2EConsoleRegionIsntRememberedHalfTyped is the regression test for a
// region remembered as it was typed: typing "n" into the "Another region…"
// field and moving on saved "n", which then filled the field on every later
// visit. A region is only remembered when a session starts with it.
func TestE2EConsoleRegionIsntRememberedHalfTyped(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "owner")
	rep := srv.newRepeater(t, owner.ID, "Hilltop")
	org, err := srv.store.CreateOrg(srv.ctx, "Scoped Org", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	region := store.RegionInput{Token: "wny", DisplayName: "Western NY", Layer: 1, AllowFlood: true, DefaultScope: true}
	if _, err := srv.store.CreateRegion(srv.ctx, org.ID, region); err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	chooseRegion := func(v string) chromedp.Action {
		return chromedp.Tasks{
			chromedp.SetValue(`#scope-region`, v, chromedp.ByQuery),
			chromedp.Evaluate(`document.getElementById("scope-region").dispatchEvent(new Event("change"))`, nil),
		}
	}
	openOptions := chromedp.Tasks{
		chromedp.WaitVisible(`[data-testid="transport"]`, chromedp.ByQuery),
		chooseTransport("air"),
		chromedp.Click(`[data-testid="conn-options"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#scope-region`, chromedp.ByQuery),
	}
	var listed, typed string
	url := srv.appURL + "/repeaters/" + rep.PublicID + "/configure"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("start typing another region, then leave",
			chromedp.Navigate(url),
			openOptions,
			chooseRegion(""),
			chromedp.SendKeys(`#scope`, "n", chromedp.ByQuery),
			chromedp.Evaluate(`document.getElementById("scope").dispatchEvent(new Event("change"))`, nil)),
		step("come back and choose another region",
			chromedp.Navigate(url),
			openOptions,
			chromedp.Value(`#scope-region`, &listed, chromedp.ByQuery),
			chooseRegion(""),
			chromedp.Value(`#scope`, &typed, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if listed != "wny" {
		t.Errorf("region list = %q, want the default wny", listed)
	}
	if typed != "" {
		t.Errorf("another-region field = %q, want it empty", typed)
	}
	watch.assertClean(t)
}
