//go:build browser

package e2e

import (
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// TestE2EOrgSummary: an admin writes the org's one-line summary in Settings ›
// Profile, watching the character count keep up as they type, and it heads the
// public page — all under the strict CSP.
func TestE2EOrgSummary(t *testing.T) {
	srv := newE2EServer(t)
	user, cookie := srv.login(t, "e2esummary")
	org, err := srv.store.CreateOrg(srv.ctx, "Summary Org", user.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	const summary = "Weekly check-in net; new members welcome."
	var before, typed, hero string
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		setSessionCookie(cookie),
		step("open Settings › Profile",
			chromedp.Navigate(srv.appURL+"/orgs/"+org.Slug+"/settings"),
			chromedp.WaitVisible(`#org_summary`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="org-summary-count"]`, &before, chromedp.ByQuery)),
		step("type a summary",
			chromedp.SendKeys(`#org_summary`, summary, chromedp.ByQuery),
			chromedp.Text(`[data-testid="org-summary-count"]`, &typed, chromedp.ByQuery)),
		step("save it",
			reloadedBy(chromedp.Click(`[data-testid="save-profile-settings"]`, chromedp.ByQuery))),
		step("see it head the public page",
			chromedp.Navigate(srv.rootURL+"/orgs/"+org.Slug),
			chromedp.WaitVisible(`[data-testid="public-summary"]`, chromedp.ByQuery),
			chromedp.Text(`[data-testid="public-summary"]`, &hero, chromedp.ByQuery)),
	); err != nil {
		t.Fatalf("browser run: %v", err)
	}
	if before != "0 / 160" {
		t.Errorf("count before typing = %q, want 0 / 160", before)
	}
	if want := "41 / 160"; typed != want {
		t.Errorf("count after typing = %q, want %q", typed, want)
	}
	if strings.TrimSpace(hero) != summary {
		t.Errorf("public page summary = %q, want %q", hero, summary)
	}
	watch.assertClean(t)
}
