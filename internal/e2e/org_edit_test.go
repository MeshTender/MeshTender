//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EOrgProfileModal: an admin opens the About card's Edit button and the
// profile fragment loads into the modal, prefilled, under the strict CSP.
func TestE2EOrgProfileModal(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eorgprofile")
	org, err := srv.store.CreateOrg(srv.ctx, "Profile Org", user.ID) // creator = admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var name string
	homeURL := srv.appURL + "/orgs/" + org.Slug
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(homeURL),
		chromedp.WaitVisible(`[data-testid="edit-profile"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="edit-profile"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#org-profile-modal-content input[name=name]`, chromedp.ByQuery),
		chromedp.Value(`#org-profile-modal-content input[name=name]`, &name, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", homeURL, err)
	}
	if name != "Profile Org" {
		t.Fatalf("profile modal name field = %q, want %q", name, "Profile Org")
	}
	watch.assertClean(t)
}

// TestE2EOrgProfileModalKeepsOpenOnError: a server validation error (renaming to a
// slug already taken — passes HTML5 checks, rejected server-side) re-renders the
// modal in place instead of navigating away, so the modal stays open with the
// error shown and the entered work preserved.
func TestE2EOrgProfileModalKeepsOpenOnError(t *testing.T) {
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

	var errShown, modalShown bool
	var nameVal string
	homeURL := srv.appURL + "/orgs/" + beta.Slug
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(homeURL),
		chromedp.WaitVisible(`[data-testid="edit-profile"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="edit-profile"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#org-profile-modal-content input[name=slug]`, chromedp.ByQuery),
		// Collide with Alpha's slug, then Save (footer button posts the modal form).
		chromedp.SetValue(`#org-profile-modal-content input[name=slug]`, "alpha", chromedp.ByQuery),
		chromedp.Click(`#org-profile-modal-content .modal-footer button[type=submit]`, chromedp.ByQuery),
		// htmx swaps the error fragment back into the still-open modal.
		chromedp.WaitVisible(`#org-profile-modal-content .alert-danger`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelector('#org-profile-modal').classList.contains('show')`, &modalShown),
		chromedp.Evaluate(`!!document.querySelector('#org-profile-modal-content .alert-danger')`, &errShown),
		chromedp.Value(`#org-profile-modal-content input[name=name]`, &nameVal, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", homeURL, err)
	}
	if !modalShown {
		t.Fatal("modal closed after a validation error; it should stay open")
	}
	if !errShown {
		t.Fatal("no error shown in the re-rendered modal")
	}
	if nameVal != "Beta" {
		t.Fatalf("entered name not preserved after error: got %q, want %q", nameVal, "Beta")
	}
	watch.assertClean(t)
}

// TestE2EOrgLinksModalAddRow: the links editor works inside the htmx-loaded modal —
// clicking "Add link" appends a row, proving link-editor.js re-initializes on the
// swapped-in form (not just at page load).
func TestE2EOrgLinksModalAddRow(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2eorglinks")
	org, err := srv.store.CreateOrg(srv.ctx, "Links Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const countRows = `document.querySelectorAll('#org-links-modal-content .link-row').length`
	var before, after int
	homeURL := srv.appURL + "/orgs/" + org.Slug
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(homeURL),
		chromedp.WaitVisible(`[data-testid="edit-links"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="edit-links"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`#org-links-modal-content [data-link-add]`, chromedp.ByQuery),
		chromedp.Evaluate(countRows, &before),
		chromedp.Click(`#org-links-modal-content [data-link-add]`, chromedp.ByQuery),
		chromedp.Evaluate(countRows, &after),
	); err != nil {
		t.Fatalf("browser run against %s: %v", homeURL, err)
	}
	if after != before+1 {
		t.Fatalf("Add link: rows %d -> %d, want +1 (link-editor.js didn't re-init in the modal)", before, after)
	}
	watch.assertClean(t)
}
