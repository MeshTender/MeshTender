package store

import (
	"strings"
	"testing"
)

// TestUserListsOrderByDisplayName: org member and repeater share lists must be
// ordered by the name actually shown (display name, falling back to username),
// not by username — so the on-screen order matches what the reader sees.
func TestUserListsOrderByDisplayName(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	// Usernames and display names sort in *opposite* orders, so a username sort
	// and a display-name sort are distinguishable.
	//   effective-name order: Alice, Carol, mmm-bob
	//   username order:       aaa-carol, mmm-bob, zzz-alice
	alice, err := st.CreateUser(ctx, "zzz-alice", "Alice")
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	bob, err := st.CreateUser(ctx, "mmm-bob", "") // no display name → sorts as "mmm-bob"
	if err != nil {
		t.Fatalf("create bob: %v", err)
	}
	carol, err := st.CreateUser(ctx, "aaa-carol", "Carol")
	if err != nil {
		t.Fatalf("create carol: %v", err)
	}
	wantOrder := []int64{alice.ID, carol.ID, bob.ID}

	// Org members (a separate creator is the admin; the three are plain members).
	creator, err := st.CreateUser(ctx, "creator", "")
	if err != nil {
		t.Fatalf("create creator: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Org", creator.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	for _, id := range []int64{alice.ID, bob.ID, carol.ID} {
		if err := st.AddOrgMember(ctx, org.ID, id, "member"); err != nil {
			t.Fatalf("add member: %v", err)
		}
	}
	members, err := st.ListOrgMembers(ctx, org.ID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	// Admins first (the creator), then members in display-name order.
	if len(members) == 0 || members[0].UserID != creator.ID {
		t.Fatalf("admin should sort first, got %+v", members)
	}
	var gotMembers []int64
	for _, m := range members[1:] {
		gotMembers = append(gotMembers, m.UserID)
	}
	if !equalIDs(gotMembers, wantOrder) {
		t.Fatalf("members not in display-name order: got %v, want %v", gotMembers, wantOrder)
	}

	// Repeater shares.
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: creator.ID, Name: "R", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	for _, id := range []int64{bob.ID, carol.ID, alice.ID} {
		if _, err := st.AddShare(ctx, rep.ID, id); err != nil {
			t.Fatalf("add share: %v", err)
		}
	}
	shares, err := st.ListShares(ctx, rep.ID)
	if err != nil {
		t.Fatalf("list shares: %v", err)
	}
	var gotShares []int64
	for _, s := range shares {
		gotShares = append(gotShares, s.UserID)
	}
	if !equalIDs(gotShares, wantOrder) {
		t.Fatalf("shares not in display-name order: got %v, want %v", gotShares, wantOrder)
	}
}

func equalIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
