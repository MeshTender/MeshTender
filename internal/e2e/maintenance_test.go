//go:build browser

package e2e

import (
	"strings"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EMaintenanceShowsCurrentAuthorName is the browser-level guarantee for
// the stale-author-name bug: the maintenance history must render the author's
// *current* display name, not the name captured when the entry was logged. It
// renames the author after logging the entry, then asserts the rendered name
// reflects the rename — which only holds with the ListMaintenance fix in place.
// Also asserts the page runs clean under the strict CSP.
func TestE2EMaintenanceShowsCurrentAuthorName(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2euser")
	rep := srv.newRepeater(t, user.ID, "Field Repeater")

	// Log an entry snapshotting the name as it was (the username, since the
	// seeded user has no display name yet), exactly as the handler does.
	if err := srv.store.AddMaintenanceEntry(srv.ctx, rep.ID, user.ID, "e2euser", "replaced battery", time.Now()); err != nil {
		t.Fatalf("add maintenance entry: %v", err)
	}
	// Now rename the author. A correct read path shows the new name on the old
	// entry; the pre-fix snapshot read would still show "e2euser".
	const renamed = "Renamed In Browser"
	if err := srv.store.SetDisplayName(srv.ctx, user.ID, renamed); err != nil {
		t.Fatalf("set display name: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	url := srv.appURL + "/repeaters/" + rep.PublicID + "/maintenance"
	var authorText string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(url),
		chromedp.WaitVisible(`.maint-author`, chromedp.ByQuery),
		chromedp.Text(`.maint-author`, &authorText, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("browser run against %s: %v", url, err)
	}

	if got := strings.TrimSpace(authorText); got != renamed {
		t.Fatalf("maintenance author rendered %q, want current display name %q (stale snapshot?)", got, renamed)
	}
	watch.assertClean(t)
}
