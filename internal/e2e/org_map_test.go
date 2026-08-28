//go:build browser

package e2e

import (
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	meshcore "github.com/meshcore-go/meshcore-go"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestE2EOrgPublicMap renders the public org Repeaters map in a real browser and
// verifies the full chain under the strict CSP: the page fetches its points from
// the cached JSON endpoint (connect-src 'self'), the CARTO vector style loads
// (connect-src the CARTO host, plus the same-origin MapLibre worker), and the
// source's own clustering collapses the co-located repeaters. A cluster proves the
// fetch succeeded and the map is live; assertClean proves no CSP violation.
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
	// they always cluster) plus two elsewhere — guarantees at least one cluster.
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

	var counts sourceCounts
	mapURL := srv.rootURL + "/orgs/" + org.Slug + "/repeaters"
	if err := chromedp.Run(bctx,
		network.Enable(),
		cdplog.Enable(),
		chromedp.Navigate(mapURL),
		// The layer only exists once the points fetch has resolved and the style has
		// loaded, so this one wait covers both.
		mapReady("map", "clusters"),
		// Clustering happens on the worker, so the counts settle a beat after the
		// layer appears.
		chromedp.Poll(`(function () {
			return window.MESHTENDER_MAPS["map"].map
				.querySourceFeatures("points").length > 0;
		})()`, nil, chromedp.WithPollingTimeout(30*time.Second)),
		mapEval("map", countIn("points"), &counts),
	); err != nil {
		t.Fatalf("browser run against %s: %v", mapURL, err)
	}
	if counts.Clusters < 1 {
		t.Errorf("no marker cluster rendered; the four co-located repeaters should collapse into one")
	}
	if counts.Features+counts.Clusters < 2 {
		t.Errorf("map rendered %d features and %d clusters, want the outliers drawn alongside the cluster",
			counts.Features, counts.Clusters)
	}
	watch.assertClean(t)
}
