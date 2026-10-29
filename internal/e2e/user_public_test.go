//go:build browser

package e2e

import (
	"crypto/rand"
	"strings"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EUserPublicLinks renders a public profile in a real browser and checks
// the links list: email shows the address (not "Email"), text platforms show the
// handle on a single line, handle platforms show the "@handle", and a public
// companion (only a public one) collapses to a row that expands to a QR code + the
// key — all under the strict CSP.
func TestE2EUserPublicLinks(t *testing.T) {
	// The public profile is served by the root (marketing) surface.
	srv := newE2EServer(t)

	u, err := srv.store.CreateUser(srv.ctx, "profileuser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	id, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	meshKey := id.String()
	err = srv.store.ReplaceUserLinks(srv.ctx, u.ID, []store.UserLink{
		{Platform: store.EmailPlatform, URL: "person@example.com", IsPrimary: true},
		{Platform: store.SignalPlatform, URL: "riverbend.07"},
		{Platform: "discord", URL: "quietfern"},
		{Platform: "github", URL: "https://github.com/mossline"},
	})
	if err != nil {
		t.Fatalf("replace links: %v", err)
	}
	// A companion they show publicly, and one they don't.
	shown, err := srv.store.CreateCompanion(srv.ctx, u.ID, "Base Node", meshKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.store.SetCompanionPublic(srv.ctx, u.ID, shown.PublicID, true); err != nil {
		t.Fatal(err)
	}
	hiddenID, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	if _, err := srv.store.CreateCompanion(srv.ctx, u.ID, "Pocket Node", hiddenID.String()); err != nil {
		t.Fatal(err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	profileURL := srv.rootURL + "/u/profileuser"
	var listText, companionsText, meshCodeText string
	var qrShown, titlesShown bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		step("open the public profile",
			chromedp.Navigate(profileURL),
			chromedp.WaitVisible(`.list-group`, chromedp.ByQuery),
			chromedp.Text(`.list-group`, &listText, chromedp.ByQuery),
			// Each icon carries a hover title naming its platform.
			chromedp.Evaluate(`['GitHub','Signal','Discord','Email'].every(function(n){return !!document.querySelector('.list-group [title="'+n+'"]');})`, &titlesShown),
			chromedp.Text(`[data-testid="public-companions"]`, &companionsText, chromedp.ByQuery)),
		// Expand the companion row (Bootstrap collapse under the CSP) and read the key.
		// Target the row's own toggle — the navbar also uses data-bs-toggle=collapse.
		// It's a <button data-bs-target>, not an <a href="#...">: it opens a disclosure
		// rather than navigating, so it needs button semantics (audit A4).
		step("expand the companion",
			chromedp.Click(`[data-testid="public-companion"]`, chromedp.ByQuery),
			chromedp.WaitVisible(`.collapse.show .pk`, chromedp.ByQuery),
			chromedp.Text(`.collapse.show .pk`, &meshCodeText, chromedp.ByQuery),
			chromedp.WaitVisible(`.collapse.show img`, chromedp.ByQuery),
			chromedp.Evaluate(`!!document.querySelector('.collapse.show img[src^="data:image"]')`, &qrShown)),
	); err != nil {
		t.Fatalf("browser run against %s: %v", profileURL, err)
	}

	// Email shows the address, not the platform name.
	if !strings.Contains(listText, "person@example.com") {
		t.Errorf("email address not shown; list text:\n%s", listText)
	}
	if strings.Contains(listText, "Email") {
		t.Errorf("email row still shows the %q label; list text:\n%s", "Email", listText)
	}
	// Text handles appear (once each — the old duplicate mono line is gone).
	for _, want := range []string{"riverbend.07", "quietfern", "@mossline"} {
		if !strings.Contains(listText, want) {
			t.Errorf("missing %q in links list; text:\n%s", want, listText)
		}
	}
	if strings.Count(listText, "riverbend.07") != 1 {
		t.Errorf("signal handle should appear exactly once, got %d; text:\n%s", strings.Count(listText, "riverbend.07"), listText)
	}
	// Only the public companion is shown, and it expands to its key and a QR.
	if !strings.Contains(companionsText, "Base Node") || strings.Contains(companionsText, "Pocket Node") {
		t.Errorf("companions card = %q, want just the public one", companionsText)
	}
	if !strings.Contains(meshCodeText, meshKey) {
		t.Errorf("expanded MeshCore key = %q, want %q", meshCodeText, meshKey)
	}
	if !qrShown {
		t.Error("expanded MeshCore row has no QR image")
	}
	if !titlesShown {
		t.Error("link icons are missing hover titles naming their platform")
	}
	watch.assertClean(t)
}
