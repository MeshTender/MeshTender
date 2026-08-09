package core

import (
	"context"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// The "Fetch location" button transmits real "get lat"/"get lon" commands to
// hardware the clicker may not own. It used to do so with no permission check and
// no audit entry at all, which made two of the product's claims false at once:
// that a shared user can only run what they were granted, and that every command
// reaching a repeater is attributable. These tests hold both halves.

// commandIDByKey resolves a catalog command by key for a test grant.
func commandIDByKey(t *testing.T, st *store.Store, ctx context.Context, key string) int64 {
	t.Helper()
	catalog, err := st.ListCommands(ctx)
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	for _, c := range catalog {
		if c.Key == key {
			return c.ID
		}
	}
	t.Fatalf("no %q in the command catalog", key)
	return 0
}

// TestCanFetchLocationRequiresBothCommands: the console offers the button only to
// a user allowed to run both coordinate reads. One of the two is not enough — a
// half-fetch would store a coordinate pair the repeater never reported.
func TestCanFetchLocationRequiresBothCommands(t *testing.T) {
	t.Parallel()
	lat := &store.Command{ID: 1, Key: "get.lat", Template: "get lat"}
	lon := &store.Command{ID: 2, Key: "get.lon", Template: "get lon"}
	other := &store.Command{ID: 3, Key: "advert", Template: "advert"}

	cases := map[string]struct {
		allowed []*store.Command
		want    bool
	}{
		"both":      {[]*store.Command{lat, lon}, true},
		"both plus": {[]*store.Command{other, lat, lon}, true},
		"lat only":  {[]*store.Command{lat}, false},
		"lon only":  {[]*store.Command{lon}, false},
		"neither":   {[]*store.Command{other}, false},
		"none":      {nil, false},
	}
	for name, tc := range cases {
		if got := canFetchLocation(tc.allowed); got != tc.want {
			t.Errorf("%s: canFetchLocation = %v, want %v", name, got, tc.want)
		}
	}
}

// TestLocationCommandsResolveFromCatalog: the fetch names the commands it sends so
// it can log them, and reports failure rather than transmitting something it can't
// account for if the catalog lacks them.
func TestLocationCommandsResolveFromCatalog(t *testing.T) {
	t.Parallel()
	st, ctx := coreStore(t)
	catalog, err := st.ListCommands(ctx)
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	lat, lon, ok := locationCommands(catalog)
	if !ok {
		t.Fatal("the seeded catalog has no get.lat/get.lon pair")
	}
	if lat.Template != "get lat" || lon.Template != "get lon" {
		t.Errorf("resolved %q/%q, want \"get lat\"/\"get lon\"", lat.Template, lon.Template)
	}

	if _, _, ok := locationCommands([]*store.Command{lat}); ok {
		t.Error("locationCommands accepted a catalog with only the latitude command")
	}
	if _, _, ok := locationCommands(nil); ok {
		t.Error("locationCommands accepted an empty catalog")
	}
}

// TestConsoleHidesFetchLocationWithoutPermission: a shared user who wasn't granted
// the coordinate reads is not offered the button, and is told what it needs
// instead of being shown a control that would be refused.
func TestConsoleHidesFetchLocationWithoutPermission(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	owner, ownerSess := appLogin(t, ts, st, ctx, h.app, "locowner")
	rep := newOwnedRepeater(t, st, ctx, owner.ID, "Loc Rep")

	// The banner only renders for a confirmed, admin-access repeater with no
	// location stored yet — the state where fetching is the obvious next step.
	if err := st.SetRepeaterConfirmed(ctx, rep.ID, owner.ID, true, 3); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	sharee, err := st.CreateUser(ctx, "locsharee", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, rep.ID, sharee.ID); err != nil {
		t.Fatal(err)
	}
	// Granted something, but not the coordinate reads.
	if err := st.SetShareCommands(ctx, rep.ID, sharee.ID, []int64{commandIDByKey(t, st, ctx, "advert")}); err != nil {
		t.Fatal(err)
	}
	shareeSess := appSession(t, ts, st, ctx, h.app, sharee)

	body := readBody(t, do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/console", shareeSess))
	if strings.Contains(body, `data-testid="fetch-location"`) {
		t.Error("console offered Fetch location to a user not permitted to run get lat/get lon")
	}
	if !strings.Contains(body, `data-testid="fetch-location-denied"`) {
		t.Error("console neither offers the fetch nor explains why not")
	}

	// The owner may run anything on their own repeater, so they still get it.
	ownerBody := readBody(t, do(t, ts, h.app, "/repeaters/"+rep.PublicID+"/console", ownerSess))
	if !strings.Contains(ownerBody, `data-testid="fetch-location"`) {
		t.Error("console withheld Fetch location from the repeater's owner")
	}
}
