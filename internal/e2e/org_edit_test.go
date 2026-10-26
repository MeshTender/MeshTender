//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EOrgProfileSettingsSave: an admin edits the org's profile in place on
// Settings › Profile and saves it, landing back there with a confirmation, under
// the strict CSP.
func TestE2EOrgProfileSettingsSave(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eorgprofile")
	org, err := srv.store.CreateOrg(srv.ctx, "Profile Org", user.ID) // creator = admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var name string
	settingsURL := srv.appURL + "/orgs/" + org.Slug + "/settings"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(settingsURL),
		chromedp.WaitVisible(`#org_name`, chromedp.ByQuery),
		chromedp.Value(`#org_name`, &name, chromedp.ByQuery),
		chromedp.SetValue(`#org_region`, "Buffalo, NY", chromedp.ByQuery),
		chromedp.Click(`[data-testid="save-profile-settings"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="flash-ok"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", settingsURL, err)
	}
	if name != "Profile Org" {
		t.Fatalf("profile name field = %q, want %q", name, "Profile Org")
	}
	if got, err := srv.store.GetOrg(srv.ctx, org.ID); err != nil || got.Region != "Buffalo, NY" {
		t.Fatalf("region after save = %+v, %v", got, err)
	}
	watch.assertClean(t)
}

// TestE2EOrgProfileSettingsKeepsWorkOnError: a server validation error (renaming
// to a slug already taken — passes HTML5 checks, rejected server-side) re-renders
// Settings › Profile with the error shown and the entered work preserved.
func TestE2EOrgProfileSettingsKeepsWorkOnError(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eorgerr")
	// Two orgs owned by the same admin; renaming one onto the other's slug collides.
	if _, err := srv.store.CreateOrg(srv.ctx, "Alpha", user.ID); err != nil {
		t.Fatalf("create org alpha: %v", err)
	}
	beta, err := srv.store.CreateOrg(srv.ctx, "Beta", user.ID)
	if err != nil {
		t.Fatalf("create org beta: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var errText, nameVal, slugVal string
	settingsURL := srv.appURL + "/orgs/" + beta.Slug + "/settings"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(settingsURL),
		chromedp.WaitVisible(`#org_slug`, chromedp.ByQuery),
		chromedp.SetValue(`#org_name`, "Beta Renamed", chromedp.ByQuery),
		chromedp.SetValue(`#org_slug`, "alpha", chromedp.ByQuery),
		chromedp.Click(`[data-testid="save-profile-settings"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="settings-form-error"]`, chromedp.ByQuery),
		chromedp.Text(`[data-testid="settings-form-error"]`, &errText, chromedp.ByQuery),
		chromedp.Value(`#org_name`, &nameVal, chromedp.ByQuery),
		chromedp.Value(`#org_slug`, &slugVal, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", settingsURL, err)
	}
	if !strings.Contains(errText, "already taken") {
		t.Fatalf("error = %q", errText)
	}
	if nameVal != "Beta Renamed" || slugVal != "alpha" {
		t.Fatalf("entered values not preserved: name %q, slug %q", nameVal, slugVal)
	}
	watch.assertClean(t)
}

// TestE2EOrgLinksSettingsAddRow: the links editor on Settings › Profile works —
// "Add link" appends a row, and a saved link comes back on the page.
func TestE2EOrgLinksSettingsAddRow(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eorglinks")
	org, err := srv.store.CreateOrg(srv.ctx, "Links Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const countRows = `document.querySelectorAll('[data-testid="settings-links"] .link-row').length`
	var before, after int
	settingsURL := srv.appURL + "/orgs/" + org.Slug + "/settings"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(settingsURL),
		chromedp.WaitVisible(`[data-testid="add-link"]`, chromedp.ByQuery),
		chromedp.Evaluate(countRows, &before),
		chromedp.Click(`[data-testid="add-link"]`, chromedp.ByQuery),
		chromedp.Evaluate(countRows, &after),
		chromedp.SetValue(`[data-testid="settings-links"] .link-row:last-child .link-value`, "https://example.org", chromedp.ByQuery),
		chromedp.Click(`[data-testid="save-links"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`[data-testid="flash-ok"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", settingsURL, err)
	}
	if after != before+1 {
		t.Fatalf("Add link: rows %d -> %d, want +1", before, after)
	}
	if links, err := srv.store.ListOrgLinks(srv.ctx, org.ID); err != nil || len(links) != 1 {
		t.Fatalf("links after save = %+v, %v", links, err)
	}
	watch.assertClean(t)
}
