//go:build browser

package e2e

import (
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EConsoleSidebarOrgAdmin is the browser regression for the empty-sidebar
// bug: an org admin who reaches another member's participating repeater via the
// shared org must see the org-ceiling commands in the "Allowed commands" sidebar,
// not an empty list. Before the fix the sidebar (core.allowedCommands) omitted
// the org-participation path, so it rendered "No commands allowed here yet." even
// though the server would accept those commands when typed manually.
func TestE2EConsoleSidebarOrgAdmin(t *testing.T) {
	srv := newE2EServer(t)

	// The repeater owner: an org member whose repeater participates by default.
	owner, err := srv.store.CreateUser(srv.ctx, "rep-owner", "")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	rep := srv.newRepeater(t, owner.ID, "Owner Rep")

	// CreateOrg makes the creator (owner) an admin member; the owner's repeater
	// participates automatically (not opted out).
	org, err := srv.store.CreateOrg(srv.ctx, "Mesh Org", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Put at least one command in the org member ceiling so any member/admin of the
	// org is authorized to run it — deterministic regardless of catalog defaults.
	cmds, err := srv.store.ListCommands(srv.ctx)
	if err != nil || len(cmds) == 0 {
		t.Fatalf("list commands: %v (n=%d)", err, len(cmds))
	}
	if err := srv.store.UpdateCommandFlags(srv.ctx, cmds[0].ID, false, false, true, false); err != nil {
		t.Fatalf("set ceiling: %v", err)
	}

	// The reporter: a second admin of the same org, browsing. Not the owner, not a
	// steward, with no per-command share — reaches the repeater only via the org.
	reporter, cookie := srv.login(t, "reporter")
	if err := srv.store.AddOrgMember(srv.ctx, org.ID, reporter.ID, "admin"); err != nil {
		t.Fatalf("add reporter to org: %v", err)
	}

	// Precondition: the reporter can reach the repeater via org access (otherwise
	// the console page 404s and the assertions below would hang on WaitVisible).
	if _, err := srv.store.GetRepeaterForUser(srv.ctx, reporter.ID, rep.ID); err != nil {
		t.Fatalf("reporter lacks org access to repeater: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	url := srv.appURL + "/repeaters/" + rep.PublicID + "/console"

	// The "Allowed commands" sidebar is always rendered (unlike #cmdinput, which is
	// hidden until a WebSerial modem connects). Command chips carry data-template
	// (the hook the console JS uses to fill the input); the empty state renders a
	// "No commands allowed here yet" paragraph instead.
	const countChips = `document.querySelectorAll('[data-testid="allowed-commands"] .chip[data-template]').length`
	const emptyShown = `!!Array.prototype.find.call(document.querySelectorAll('[data-testid="allowed-commands"] p'), function (p) { return /No commands allowed here yet/.test(p.textContent); })`

	var chips int
	var empty bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`[data-testid="allowed-commands"]`, chromedp.ByQuery),
		chromedp.Evaluate(countChips, &chips),
		chromedp.Evaluate(emptyShown, &empty),
	); err != nil {
		t.Fatalf("browser run against %s: %v", url, err)
	}

	if chips == 0 {
		t.Fatal("org admin console sidebar is empty (regression: org-participation path missing)")
	}
	if empty {
		t.Fatalf("console shows the empty-state message despite %d command chips", chips)
	}

	watch.assertClean(t)
}
