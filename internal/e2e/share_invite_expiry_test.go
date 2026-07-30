//go:build browser

package e2e

import (
	"os"
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/jleight/meshtender/internal/store"
)

// TestE2EInviteExpiryBadges renders the share page with one live and one expired
// share link and checks what an owner actually sees: the live link keeps its copy
// control, the expired one is badged and loses it, and the page runs clean under the
// CSP.
//
// Covers the presentation half of audit finding S3 (share links used to never
// expire); the store-level rules are pinned in internal/store.
func TestE2EInviteExpiryBadges(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2einviteexpiry")

	rep, err := srv.store.CreateRepeater(srv.ctx, &store.Repeater{
		OwnerID: user.ID, Name: "Rep", PublicKeyHex: strings.Repeat("c", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}

	if _, err := srv.store.CreateInvite(srv.ctx, rep.ID, "still good", nil); err != nil {
		t.Fatalf("create live invite: %v", err)
	}
	staleToken, err := srv.store.CreateInvite(srv.ctx, rep.ID, "long forgotten", nil)
	if err != nil {
		t.Fatalf("create stale invite: %v", err)
	}
	if _, err := srv.store.Pool().Exec(srv.ctx,
		`UPDATE repeater_invites SET expires_at = now() - interval '1 minute' WHERE token = $1`,
		staleToken); err != nil {
		t.Fatalf("expire invite: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	var rows, expiredBadges, copyButtons int
	var showsStaleToken bool
	var shot []byte
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		chromedp.Navigate(srv.appURL+"/repeaters/"+rep.PublicID+"/share"),
		chromedp.WaitVisible(`[data-testid="invite-row"]`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('[data-testid="invite-row"]').length`, &rows),
		chromedp.Evaluate(`document.querySelectorAll('[data-testid="invite-expired"]').length`, &expiredBadges),
		chromedp.Evaluate(`document.querySelectorAll('[data-copy-target]').length`, &copyButtons),
		chromedp.Evaluate(`document.body.innerText.includes(`+jsString(staleToken)+`)`, &showsStaleToken),
		chromedp.FullScreenshot(&shot, 80),
	); err != nil {
		t.Fatalf("share page: %v", err)
	}
	watch.assertClean(t)

	if rows != 2 {
		t.Errorf("invite rows = %d, want 2 (both links listed so the owner can tidy up)", rows)
	}
	if expiredBadges != 1 {
		t.Errorf("expired badges = %d, want exactly 1", expiredBadges)
	}
	if copyButtons != 1 {
		t.Errorf("copy controls = %d, want 1 — only the live link should be copyable", copyButtons)
	}
	if showsStaleToken {
		t.Error("the expired link's token is still rendered on the page")
	}

	// Keep the render for eyeballing when E2E_SHOT_DIR is set; skipped in CI.
	if dir := os.Getenv("E2E_SHOT_DIR"); dir != "" && len(shot) > 0 {
		if err := os.WriteFile(dir+"/invite-expiry.png", shot, 0o644); err != nil {
			t.Logf("write screenshot: %v", err)
		}
	}
}

// jsString quotes s for embedding in a JS expression.
func jsString(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}
