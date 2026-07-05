package store

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// adminCount returns how many admins the org has (test helper; the store no
// longer exposes a count method).
func adminCount(t *testing.T, st *Store, ctx context.Context, orgID int64) int {
	t.Helper()
	var n int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM org_members WHERE org_id = $1 AND role = 'admin'`, orgID).Scan(&n); err != nil {
		t.Fatalf("count admins: %v", err)
	}
	return n
}

// TestLastAdminRace is the regression for the check-then-act race: with exactly
// two admins, demoting both concurrently must not leave the org with zero admins.
// The FOR UPDATE lock in guardLastAdminTx serializes the two demotions, so exactly
// one succeeds and the other gets ErrLastAdmin. Before the fix the two demotions
// could both read "2 admins" and both proceed, dropping the org to zero admins.
func TestLastAdminRace(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	a, err := st.CreateUser(ctx, "admin-a", "")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	org, err := st.CreateOrg(ctx, "Org", a.ID) // a is admin
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	b, err := st.CreateUser(ctx, "admin-b", "")
	if err != nil {
		t.Fatalf("create b: %v", err)
	}
	if err := st.AddOrgMember(ctx, org.ID, b.ID, "admin"); err != nil {
		t.Fatalf("add admin b: %v", err)
	}

	// Repeat to give the race window many chances to interleave; the invariant must
	// hold every round.
	for round := 0; round < 30; round++ {
		// Restore both to admin (promotion skips the guard, so it never fails).
		if err := st.SetOrgMemberRole(ctx, org.ID, a.ID, "admin"); err != nil {
			t.Fatalf("round %d: repromote a: %v", round, err)
		}
		if err := st.SetOrgMemberRole(ctx, org.ID, b.ID, "admin"); err != nil {
			t.Fatalf("round %d: repromote b: %v", round, err)
		}
		if got := adminCount(t, st, ctx, org.ID); got != 2 {
			t.Fatalf("round %d: setup left %d admins, want 2", round, got)
		}

		// Demote both admins at once.
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		targets := []int64{a.ID, b.ID}
		for i, uid := range targets {
			wg.Add(1)
			go func(i int, uid int64) {
				defer wg.Done()
				<-start
				errs[i] = st.SetOrgMemberRole(ctx, org.ID, uid, "member")
			}(i, uid)
		}
		close(start)
		wg.Wait()

		// The org must keep at least one admin.
		if got := adminCount(t, st, ctx, org.ID); got < 1 {
			t.Fatalf("round %d: org left with %d admins (race lost the last admin)", round, got)
		}

		// Exactly one demotion succeeds; the other is refused as the last admin.
		var ok, refused, other int
		for _, e := range errs {
			switch {
			case e == nil:
				ok++
			case errors.Is(e, ErrLastAdmin):
				refused++
			default:
				other++
			}
		}
		if other != 0 {
			t.Fatalf("round %d: unexpected error(s) from demotion: %v", round, errs)
		}
		if ok != 1 || refused != 1 {
			t.Fatalf("round %d: got %d ok / %d refused, want 1/1 (%v)", round, ok, refused, errs)
		}
	}
}
