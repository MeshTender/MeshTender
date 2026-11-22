package store

import (
	"fmt"
	"testing"
)

func TestListRepeaterRegionNames(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner := mustUser(t, st, ctx, "regionowner")
	a := mustOrg(t, st, ctx, "RegionsA", owner)
	b := mustOrg(t, st, ctx, "RegionsB", owner)
	rep, err := st.CreateRepeater(ctx, &Repeater{OwnerID: owner, Name: "Hilltop", PublicKeyHex: testKey(7)})
	if err != nil {
		t.Fatal(err)
	}
	add := func(org int64, z RegionInput) {
		t.Helper()
		z.Layer, z.AllowFlood = 1, true
		if _, err := st.CreateRegion(ctx, org, z); err != nil {
			t.Fatal(err)
		}
	}
	add(a, RegionInput{Token: "wny", DisplayName: "Western NY"})
	add(a, RegionInput{Token: "buf", DisplayName: "Buffalo"})
	// The same token in another org, recommended there as the default scope.
	add(b, RegionInput{Token: "wny", DisplayName: "WNY", DefaultScope: true})

	got, err := st.ListRepeaterRegionNames(ctx, rep.ID)
	if err != nil {
		t.Fatal(err)
	}
	// One row per token, by token; a token is a default scope if any org
	// recommends it. (Which org's label wins for a shared token is up to the
	// collation.)
	var tokens []string
	for _, r := range got {
		tokens = append(tokens, fmt.Sprintf("%s:%v", r.Token, r.DefaultScope))
	}
	if want := "[buf:false wny:true]"; fmt.Sprint(tokens) != want {
		t.Fatalf("regions = %v, want %s", tokens, want)
	}
}
