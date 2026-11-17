package core

import (
	"strconv"
	"strings"
	"testing"

	"github.com/MeshTender/MeshTender/internal/store"
)

// TestRegionFlagsShown: the regions list badges the primary region and the
// default scope — on Settings › Regions and the public Repeater setup alike —
// and a region's edit form names the region that holds each flag now when it's
// another one, since turning the switch on quietly takes the flag from it
// (MESHTENDER-16).
func TestRegionFlagsShown(t *testing.T) {
	t.Parallel()
	st, ctx, ts, h := splitServer(t)
	admin, sess := appLogin(t, ts, st, ctx, h.app, "flagsadmin")
	org, err := st.CreateOrg(ctx, "Flags Org", admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "us", DisplayName: "United States", Layer: 1, Primary: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "wny", DisplayName: "Western New York", Layer: 2, DefaultScope: true}); err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateRegion(ctx, org.ID, store.RegionInput{Token: "buf", DisplayName: "Buffalo", Layer: 3})
	if err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"Settings › Regions":    getBody(t, ts, h.app, "/orgs/"+org.Slug+"/settings/regions", sess),
		"public Repeater setup": getBody(t, ts, h.root, "/orgs/"+org.Slug+"/guide/repeaters"),
	} {
		assertInOrder(t, name, body,
			"United States", `data-testid="region-badge-primary"`,
			"Western New York", `data-testid="region-badge-default-scope"`,
			"Buffalo")
		if strings.Count(body, `data-testid="region-badge-primary"`) != 1 || strings.Count(body, `data-testid="region-badge-default-scope"`) != 1 {
			t.Errorf("%s: want exactly one of each badge", name)
		}
	}

	edit := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions/"+strconv.FormatInt(other, 10)+"/edit", sess)
	for _, want := range []string{
		`data-testid="region-primary-holder">Currently: United States`,
		`data-testid="region-default-scope-holder">Currently: Western New York`,
	} {
		if !strings.Contains(edit, want) {
			t.Errorf("Buffalo's edit form lacks %q", want)
		}
	}
	us := getBody(t, ts, h.app, "/orgs/"+org.Slug+"/config/regions/new", sess)
	if !strings.Contains(us, "Currently: United States") {
		t.Error("the new-region form doesn't say which region is primary")
	}
}
