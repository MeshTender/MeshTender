//go:build browser

package e2e

import (
	"testing"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/jleight/meshtender/internal/geo"
	"github.com/jleight/meshtender/internal/store"
)

// TestE2ERegionEditorControls renders the org region editor and checks the
// clarified controls: Allow flood and Primary are labeled switches, the map-area
// button reads "Edit area", and Primary is exclusive — turning one region's
// Primary switch on clears the others. Runs under the strict CSP.
func TestE2ERegionEditorControls(t *testing.T) {
	srv := newE2EServer(t)
	owner, cookie := srv.login(t, "region-admin")
	org, err := srv.store.CreateOrg(srv.ctx, "Region Org", owner.ID) // owner is an admin member
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	regions := []store.RegionInput{
		{Token: "us", DisplayName: "US", Layer: 1, Primary: false,
			GeofenceJSON: geo.Rectangle(30, -90, 48, -70)},
		{Token: "us-ny", DisplayName: "New York", Layer: 2, Primary: true,
			GeofenceJSON: geo.Rectangle(40, -80, 45, -73)},
	}
	if err := srv.store.ReplaceOrgConfig(srv.ctx, org.ID, nil, regions); err != nil {
		t.Fatalf("replace config: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	url := srv.appURL + "/orgs/" + org.Slug + "/config/regions"

	// Read one region's control state by its token (row order isn't guaranteed).
	stateJS := func(token string) string {
		return `(function () {
			var blocks = document.querySelectorAll('.region-block');
			for (var i = 0; i < blocks.length; i++) {
				var t = blocks[i].querySelector('input[name="region_token"]');
				if (t && t.value === ` + "`" + token + "`" + `) {
					return {
						floodIsSwitch: !!blocks[i].querySelector('.form-switch [data-flood]'),
						primaryIsSwitch: !!blocks[i].querySelector('.form-switch [data-primary]'),
						primaryChecked: blocks[i].querySelector('[data-primary]').checked,
						primaryHidden: blocks[i].querySelector('input[name="region_primary"]').value,
						editText: blocks[i].querySelector('.region-edit-btn').textContent.trim(),
					};
				}
			}
			return null;
		})()`
	}
	type ctrl struct {
		FloodIsSwitch   bool   `json:"floodIsSwitch"`
		PrimaryIsSwitch bool   `json:"primaryIsSwitch"`
		PrimaryChecked  bool   `json:"primaryChecked"`
		PrimaryHidden   string `json:"primaryHidden"`
		EditText        string `json:"editText"`
	}

	var us0, ny0, us1, ny1 ctrl
	if err := chromedp.Run(bctx,
		network.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`.region-block`, chromedp.ByQuery),
		chromedp.Evaluate(stateJS("us"), &us0),
		chromedp.Evaluate(stateJS("us-ny"), &ny0),
		// Turn on US's Primary switch; NY (currently primary) must clear.
		chromedp.Evaluate(`(function(){
			var b = Array.prototype.slice.call(document.querySelectorAll('.region-block'))
				.find(function(x){ return x.querySelector('input[name="region_token"]').value === 'us'; });
			b.querySelector('[data-primary]').click();
			return true;
		})()`, nil),
		chromedp.Evaluate(stateJS("us"), &us1),
		chromedp.Evaluate(stateJS("us-ny"), &ny1),
	); err != nil {
		t.Fatalf("browser run against %s: %v", url, err)
	}

	// Controls are switches, and the map-area button is relabeled.
	if !us0.FloodIsSwitch || !us0.PrimaryIsSwitch {
		t.Errorf("US controls not switches: %+v", us0)
	}
	if us0.EditText != "Edit area" {
		t.Errorf("edit button = %q, want %q", us0.EditText, "Edit area")
	}
	// Initial: NY is primary, US is not.
	if !ny0.PrimaryChecked || us0.PrimaryChecked {
		t.Errorf("initial primary wrong: us=%v ny=%v", us0.PrimaryChecked, ny0.PrimaryChecked)
	}
	// After toggling US on, exactly US is primary (switch + hidden input), NY cleared.
	if !us1.PrimaryChecked || us1.PrimaryHidden != "1" {
		t.Errorf("US not primary after toggle: %+v", us1)
	}
	if ny1.PrimaryChecked || ny1.PrimaryHidden != "" {
		t.Errorf("NY still primary after toggling US: %+v", ny1)
	}

	watch.assertClean(t)
}
