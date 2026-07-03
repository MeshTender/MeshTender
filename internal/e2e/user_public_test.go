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

	"github.com/jleight/meshtender/internal/store"
)

// TestE2EUserPublicLinks renders a public profile in a real browser and checks
// the links list: email shows the address (not "Email"), text platforms show the
// handle on a single line, handle platforms show the "@handle", and a MeshCore
// key collapses to a row that expands to a QR code + the key — all under the
// strict CSP.
func TestE2EUserPublicLinks(t *testing.T) {
	// The public profile is served by the root (marketing) surface.
	srv := newE2EServer(t, rootReachableHosts())

	u, err := srv.store.CreateUser(srv.ctx, "profileuser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	id, _ := meshcore.GenerateLocalIdentity(rand.Reader)
	meshKey := id.String()
	err = srv.store.ReplaceUserLinks(srv.ctx, u.ID, []store.UserLink{
		{Platform: store.EmailPlatform, URL: "person@example.com", IsPrimary: true},
		{Platform: store.SignalPlatform, URL: "jleight.07"},
		{Platform: "discord", URL: "xerofait"},
		{Platform: "github", URL: "https://github.com/jleight"},
		{Platform: store.MeshCorePlatform, Label: "Base Node", URL: meshKey},
	})
	if err != nil {
		t.Fatalf("replace links: %v", err)
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	profileURL := srv.browserURL + "/u/profileuser"
	var listText, meshCodeText string
	var qrShown, titlesShown bool
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(profileURL),
		chromedp.WaitVisible(`.list-group`, chromedp.ByQuery),
		chromedp.Text(`.list-group`, &listText, chromedp.ByQuery),
		// Each icon carries a hover title naming its platform.
		chromedp.Evaluate(`['GitHub','Signal','Discord','Email','MeshCore'].every(function(n){return !!document.querySelector('.list-group [title="'+n+'"]');})`, &titlesShown),
		// Expand the MeshCore row (Bootstrap collapse under the CSP) and read the key.
		// Target the row's own toggle — the navbar also uses data-bs-toggle=collapse.
		chromedp.Click(`.list-group a[href^="#mesh-"]`, chromedp.ByQuery),
		chromedp.WaitVisible(`.collapse.show .pk`, chromedp.ByQuery),
		chromedp.Text(`.collapse.show .pk`, &meshCodeText, chromedp.ByQuery),
		chromedp.WaitVisible(`.collapse.show img`, chromedp.ByQuery),
		chromedp.Evaluate(`!!document.querySelector('.collapse.show img[src^="data:image"]')`, &qrShown),
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
	for _, want := range []string{"jleight.07", "xerofait", "@jleight", "Base Node"} {
		if !strings.Contains(listText, want) {
			t.Errorf("missing %q in links list; text:\n%s", want, listText)
		}
	}
	if strings.Count(listText, "jleight.07") != 1 {
		t.Errorf("signal handle should appear exactly once, got %d; text:\n%s", strings.Count(listText, "jleight.07"), listText)
	}
	// The expanded MeshCore row reveals the key and a QR image.
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
