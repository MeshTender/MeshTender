package store

import "testing"

// TestListOrgProfileNamesForUser covers the single-query grouping that replaced
// the per-org N+1 in the serial-setup org selector: every org the user belongs to
// is returned once, ordered by org name, each carrying its profile names in
// display order; an org with no profiles still appears (LEFT JOIN) with none.
func TestListOrgProfileNamesForUser(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, err := st.CreateUser(ctx, "npowner", "")
	if err != nil {
		t.Fatal(err)
	}
	// Two orgs, deliberately created out of alphabetical order to prove ordering.
	beta, err := st.CreateOrg(ctx, "Beta", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := st.CreateOrg(ctx, "Alpha", owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A third org the user is NOT a member of must never appear.
	stranger, err := st.CreateUser(ctx, "stranger", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrg(ctx, "Hidden", stranger.ID); err != nil {
		t.Fatal(err)
	}

	// Alpha gets two profiles (created out of order to prove position ordering);
	// Beta gets none.
	if err := st.ReplaceOrgConfig(ctx, alpha.ID, []ProfileInput{
		{Name: "ESP32"},
		{Name: "nRF52"},
	}, nil); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListOrgProfileNamesForUser(ctx, owner.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d orgs, want 2: %+v", len(got), got)
	}
	// Ordered by lower(name): Alpha before Beta.
	if got[0].OrgID != alpha.ID || got[0].OrgName != "Alpha" {
		t.Fatalf("first org = %+v, want Alpha", got[0])
	}
	if len(got[0].Profiles) != 2 || got[0].Profiles[0] != "ESP32" || got[0].Profiles[1] != "nRF52" {
		t.Fatalf("Alpha profiles = %v, want [ESP32 nRF52]", got[0].Profiles)
	}
	if got[1].OrgID != beta.ID || got[1].OrgName != "Beta" {
		t.Fatalf("second org = %+v, want Beta", got[1])
	}
	if len(got[1].Profiles) != 0 {
		t.Fatalf("Beta profiles = %v, want none", got[1].Profiles)
	}
}
