//go:build browser

package e2e

import (
	"testing"

	"github.com/chromedp/chromedp"
)

// TestAdminUsersActionsModal drives the reworked admin users page in a real
// browser: open a row's Actions dropdown, pick Permissions, and confirm the
// modal loads the form via htmx — exercising the Bootstrap dropdown + modal +
// htmx-fragment interaction and asserting the strict CSP isn't tripped (the one
// thing Go handler tests can't see).
func TestAdminUsersActionsModal(t *testing.T) {
	srv := newE2EServer(t)
	// login()'s first account bootstraps the manage-users capability, so it can
	// reach /admin/users.
	_, cookie := srv.login(t, "e2eadmin")

	ctx, cancel, watch := startBrowser(t)
	defer cancel()

	if err := chromedp.Run(ctx,
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/admin/users"),
		chromedp.WaitVisible(`[data-testid="user-row"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="user-actions"]`, chromedp.ByQuery), // open the dropdown
		chromedp.WaitVisible(`[data-testid="user-permissions"]`, chromedp.ByQuery),
		chromedp.Click(`[data-testid="user-permissions"]`, chromedp.ByQuery), // open the modal + htmx load
		// The permissions form arrives in the modal via htmx.
		chromedp.WaitVisible(`#user-modal-content input[name="manage_users"]`, chromedp.ByQuery),
	); err != nil {
		t.Fatalf("drive admin users modal: %v", err)
	}

	watch.assertClean(t)
}
