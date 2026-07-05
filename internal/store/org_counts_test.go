package store

import (
	"context"
	"strings"
	"testing"
)

// orgCountHarness bundles the store + a consistency assertion for the
// trigger-maintained directory counts (migration 0033). The triggers are
// correct iff the denormalized organizations.member_count/repeater_count always
// equal OrgCounts, which recomputes from the source tables.
type orgCountHarness struct {
	t   *testing.T
	st  *Store
	ctx context.Context
}

func (h *orgCountHarness) assert(orgID int64, label string, wantMembers, wantReps int) {
	h.t.Helper()
	var dm, dr int
	if err := h.st.pool.QueryRow(h.ctx,
		`SELECT member_count, repeater_count FROM organizations WHERE id=$1`, orgID).Scan(&dm, &dr); err != nil {
		h.t.Fatalf("%s: read denormalized counts: %v", label, err)
	}
	lm, lr, err := h.st.OrgCounts(h.ctx, orgID)
	if err != nil {
		h.t.Fatalf("%s: OrgCounts: %v", label, err)
	}
	if dm != lm || dr != lr {
		h.t.Fatalf("%s: denormalized (m=%d r=%d) != recomputed (m=%d r=%d)", label, dm, dr, lm, lr)
	}
	if dm != wantMembers || dr != wantReps {
		h.t.Fatalf("%s: counts m=%d r=%d, want m=%d r=%d", label, dm, dr, wantMembers, wantReps)
	}
}

func (h *orgCountHarness) user(name string) int64 {
	h.t.Helper()
	u, err := h.st.CreateUser(h.ctx, name, "")
	if err != nil {
		h.t.Fatalf("create user %s: %v", name, err)
	}
	return u.ID
}

func (h *orgCountHarness) repeater(owner int64, keyChar byte) int64 {
	h.t.Helper()
	r, err := h.st.CreateRepeater(h.ctx, &Repeater{
		OwnerID: owner, Name: "R", PublicKeyHex: strings.Repeat(string(keyChar), 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		h.t.Fatalf("create repeater: %v", err)
	}
	return r.ID
}

// TestOrgCountTriggers walks a repeater/membership/exclude lifecycle and checks
// the denormalized counts stay exact and in sync with the live computation after
// every mutation.
func TestOrgCountTriggers(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	h := &orgCountHarness{t, st, ctx}

	owner := h.user("owner")
	org, err := st.CreateOrg(ctx, "Org", owner) // creator becomes an admin member
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	h.assert(org.ID, "after create", 1, 0)

	// The owner's repeater participates automatically (no opt-out).
	repA := h.repeater(owner, 'a')
	h.assert(org.ID, "owner adds repeater", 1, 1)

	m2 := h.user("m2")
	if err := st.AddOrgMember(ctx, org.ID, m2, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	h.assert(org.ID, "second member joins", 2, 1)

	h.repeater(m2, 'b')
	repC := h.repeater(m2, 'c')
	h.assert(org.ID, "member adds two repeaters", 2, 3)

	if err := st.SetRepeaterOrgExcluded(ctx, org.ID, repC, true); err != nil {
		t.Fatalf("exclude: %v", err)
	}
	h.assert(org.ID, "exclude one repeater", 2, 2)

	if err := st.SetRepeaterOrgExcluded(ctx, org.ID, repC, false); err != nil {
		t.Fatalf("re-include: %v", err)
	}
	h.assert(org.ID, "re-include repeater", 2, 3)

	if err := st.DeleteRepeaterOwned(ctx, owner, repA); err != nil {
		t.Fatalf("delete repeater: %v", err)
	}
	h.assert(org.ID, "delete owner repeater", 2, 2)

	// Member leaves: member_count drops, and both of that member's repeaters leave
	// the count (owner no longer a member) → repeater_count 0.
	if err := st.RemoveOrgMember(ctx, org.ID, m2); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	h.assert(org.ID, "member leaves", 1, 0)
}

// TestOrgCountCascades covers the DB-level cascades that have no store method:
// deleting a user (cascades their org_members + repeaters) must keep counts
// consistent, and deleting an org (cascades members/excludes, firing recompute
// on the vanishing org) must not error.
func TestOrgCountCascades(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	h := &orgCountHarness{t, st, ctx}

	owner := h.user("owner")
	org, err := st.CreateOrg(ctx, "Org", owner)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	h.repeater(owner, 'a')

	member := h.user("member")
	if err := st.AddOrgMember(ctx, org.ID, member, "member"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	h.repeater(member, 'b')
	h.assert(org.ID, "two members, two repeaters", 2, 2)

	// Deleting the member cascades to org_members (ON DELETE CASCADE) and their
	// repeaters (owner_id ON DELETE CASCADE); both trigger recomputes.
	if _, err := st.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, member); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	h.assert(org.ID, "member user deleted", 1, 1)

	// Deleting the org cascades members/excludes; the recompute targets a row that
	// no longer exists and must be a harmless no-op.
	if _, err := st.pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org.ID); err != nil {
		t.Fatalf("delete org: %v", err)
	}
	var n int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM organizations WHERE id=$1`, org.ID).Scan(&n); err != nil {
		t.Fatalf("count org: %v", err)
	}
	if n != 0 {
		t.Fatalf("org still present after delete (n=%d)", n)
	}
}
