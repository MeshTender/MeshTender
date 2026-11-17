package store

import (
	"errors"
	"fmt"
	"testing"
)

// TestOrgChannels: an org's channels are hashtag (no stored key) or private (a
// sealed key), and the schema refuses anything else; names are unique per org;
// the list puts hashtag channels first; an update keeps a private key unless
// given a new one and drops it on a rename to a hashtag; deleting a scope region
// falls back to the default scope; and an org holds at most MaxOrgChannels.
func TestOrgChannels(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner := mustUser(t, st, ctx, "chanowner")
	org := mustOrg(t, st, ctx, "ChanOrg", owner)
	lake, err := st.CreateRegion(ctx, org, RegionInput{Token: "lake", DisplayName: "Lakeshore", Layer: 1})
	if err != nil {
		t.Fatal(err)
	}

	ops, err := st.CreateOrgChannel(ctx, org, OrgChannel{Name: "Ops", SecretSealed: []byte("sealed-key"), Unscoped: true})
	if err != nil {
		t.Fatal(err)
	}
	tag, err := st.CreateOrgChannel(ctx, org, OrgChannel{Name: "#lakeshore", ScopeRegionID: &lake, Note: "Everyday chat."})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrgChannel(ctx, org, OrgChannel{Name: "#lakeshore"}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("duplicate name = %v, want ErrDuplicate", err)
	}
	for _, bad := range []OrgChannel{
		{Name: "#tagged", SecretSealed: []byte("x")}, // a hashtag channel can't carry a key
		{Name: "Keyless"}, // a private channel needs one
		{Name: "#both", ScopeRegionID: &lake, Unscoped: true},
	} {
		if _, err := st.CreateOrgChannel(ctx, org, bad); err == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}

	list, err := st.ListOrgChannels(ctx, org)
	if err != nil || len(list) != 2 || list[0].Name != "#lakeshore" || !list[0].Hashtag() || list[1].Hashtag() {
		t.Fatalf("list = %+v, %v; want #lakeshore then Ops", list, err)
	}

	// Renaming without a new key keeps the private key; renaming to a hashtag drops it.
	if err := st.UpdateOrgChannel(ctx, org, ops, OrgChannel{Name: "Lakeshore Ops", Unscoped: true}); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.GetOrgChannel(ctx, org, ops); string(c.SecretSealed) != "sealed-key" || c.Name != "Lakeshore Ops" {
		t.Errorf("after rename: %+v; want the key kept", c)
	}
	if err := st.UpdateOrgChannel(ctx, org, ops, OrgChannel{Name: "#ops"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.GetOrgChannel(ctx, org, ops); c.SecretSealed != nil {
		t.Error("renaming to a hashtag kept the private key")
	}
	if err := st.UpdateOrgChannel(ctx, org, 0, OrgChannel{Name: "#x"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update a missing channel = %v, want ErrNotFound", err)
	}

	if err := st.DeleteRegion(ctx, org, lake); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.GetOrgChannel(ctx, org, tag); c.ScopeRegionID != nil || c.Unscoped {
		t.Errorf("after deleting its region: %+v; want the default scope", c)
	}

	if err := st.DeleteOrgChannel(ctx, org, ops); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetOrgChannel(ctx, org, ops); !errors.Is(err, ErrNotFound) {
		t.Errorf("get a deleted channel = %v", err)
	}
	for i := 1; i < MaxOrgChannels; i++ {
		if _, err := st.CreateOrgChannel(ctx, org, OrgChannel{Name: fmt.Sprintf("#c%d", i)}); err != nil {
			t.Fatalf("channel %d: %v", i, err)
		}
	}
	if _, err := st.CreateOrgChannel(ctx, org, OrgChannel{Name: "#over"}); !errors.Is(err, ErrChannelLimit) {
		t.Errorf("channel over the cap = %v, want ErrChannelLimit", err)
	}
}
