//go:build browser

package e2e

import (
	"crypto/rand"
	"fmt"
	"testing"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/jleight/meshtender/internal/store"
)

// TestE2EOrgPublicMap renders the public org Repeaters map in a real browser and
// verifies the full Batch 2+3 chain under the strict CSP: the page fetches its
// points from the cached JSON endpoint (connect-src 'self') and the vendored
// markercluster plugin clusters co-located markers. A visible cluster proves the
// fetch succeeded and the plugin loaded; assertClean proves no CSP violation
// (from the fetch or the new self-hosted assets).
func TestE2EOrgPublicMap(t *testing.T) {
	// Served anonymously by the root (marketing) surface.
	srv := newE2EServer(t)

	owner, err := srv.store.CreateUser(srv.ctx, "mapowner", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	org, err := srv.store.CreateOrg(srv.ctx, "Map Club", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}

	// Four repeaters at the *same* coordinates (they can never separate by zoom, so
	// they always cluster) plus two elsewhere — guarantees a .marker-cluster.
	locs := []struct{ lat, lon float64 }{
		{40.0, -75.0}, {40.0, -75.0}, {40.0, -75.0}, {40.0, -75.0},
		{41.0, -76.0}, {39.0, -74.0},
	}
	for i, loc := range locs {
		id, _ := meshcore.GenerateLocalIdentity(rand.Reader)
		rep, err := srv.store.CreateRepeater(srv.ctx, &store.Repeater{
			OwnerID: owner.ID, Name: fmt.Sprintf("Node %d", i), PublicKeyHex: id.String(),
			RadioFreqHz: 869525000, RadioBwHz: 250000, RadioSF: 11, RadioCR: 5,
			ShowOnPublicOrg: true,
		})
		if err != nil {
			t.Fatalf("create repeater %d: %v", i, err)
		}
		if err := srv.store.SetRepeaterLocation(srv.ctx, rep.ID, loc.lat, loc.lon); err != nil {
			t.Fatalf("set location %d: %v", i, err)
		}
	}

	bctx, cancel, watch := startBrowser(t)
	defer cancel()

	mapURL := srv.rootURL + "/orgs/" + org.Slug + "/repeaters"
	var clusterCount, markerCount int
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(mapURL),
		// The map only paints markers after the fetch to /repeaters.json resolves.
		chromedp.WaitVisible(`.leaflet-container`, chromedp.ByQuery),
		chromedp.WaitVisible(`.marker-cluster`, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('.marker-cluster').length`, &clusterCount),
		chromedp.Evaluate(`document.querySelectorAll('.leaflet-interactive').length`, &markerCount),
	); err != nil {
		t.Fatalf("browser run against %s: %v", mapURL, err)
	}

	if clusterCount == 0 {
		t.Errorf("no marker cluster rendered; the fetched points did not cluster")
	}
	if markerCount == 0 {
		t.Errorf("no interactive map markers rendered")
	}
	// The whole point of the exercise: the fetch + vendored assets ran cleanly.
	watch.assertClean(t)
}
