package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// transferFixture builds the common setup: an owner, a repeater, and a second
// user shared on it. steward controls whether that second user is flagged a
// steward (the transfer-eligible state).
func transferFixture(t *testing.T, st *Store, ctx context.Context, key string, steward bool) (owner, target *User, rep *Repeater) {
	t.Helper()
	owner, err := st.CreateUser(ctx, "owner", "")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	target, err = st.CreateUser(ctx, "target", "")
	if err != nil {
		t.Fatalf("create target: %v", err)
	}
	rep, err = st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner.ID, Name: "Ridge", PublicKeyHex: strings.Repeat(key, 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	if _, err := st.AddShare(ctx, rep.ID, target.ID); err != nil {
		t.Fatalf("add share: %v", err)
	}
	if steward {
		if err := st.SetShareSteward(ctx, rep.ID, target.ID, true); err != nil {
			t.Fatalf("set steward: %v", err)
		}
	}
	return owner, target, rep
}

// TestTransferRepeaterToSteward is the happy path, asserting the whole
// post-conditions set together: ownership moves, the new owner's share and its
// grants are gone, the outgoing owner is left a steward, pending share links are
// revoked, and the site's history (docs, maintenance) survives the handover —
// outliving the builder being the entire point of the feature.
func TestTransferRepeaterToSteward(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, target, rep := transferFixture(t, st, ctx, "a", true)

	// State that must survive: documentation and a maintenance entry.
	if err := st.UpdateRepeaterDocs(ctx, owner.ID, rep.ID, "public doc", "gate code 1234"); err != nil {
		t.Fatalf("update docs: %v", err)
	}
	if err := st.AddMaintenanceEntry(ctx, rep.ID, owner.ID, "owner", "swapped antenna", time.Now()); err != nil {
		t.Fatalf("add maintenance: %v", err)
	}
	// State that must NOT survive: the recipient's own grants, and a pending link.
	catalog, err := st.ListCommands(ctx)
	if err != nil || len(catalog) == 0 {
		t.Fatalf("list commands: %v (n=%d)", err, len(catalog))
	}
	if err := st.SetShareCommands(ctx, rep.ID, target.ID, []int64{catalog[0].ID}); err != nil {
		t.Fatalf("set share commands: %v", err)
	}
	if _, err := st.CreateInvite(ctx, rep.ID, "pending", nil); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, target.ID); err != nil {
		t.Fatalf("TransferRepeaterToSteward: %v", err)
	}

	// Ownership moved, and the node is reachable through the owner-only gate for
	// the new owner but not the old one.
	got, err := st.GetRepeaterOwned(ctx, target.ID, rep.ID)
	if err != nil {
		t.Fatalf("GetRepeaterOwned(new owner): %v", err)
	}
	if got.OwnerID != target.ID {
		t.Fatalf("owner_id = %d, want %d (the new owner)", got.OwnerID, target.ID)
	}
	if _, err := st.GetRepeaterOwned(ctx, owner.ID, rep.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRepeaterOwned(old owner) err = %v, want ErrNotFound", err)
	}
	// The public_id is stable: links and QR codes to the public page still resolve.
	if got.PublicID != rep.PublicID {
		t.Fatalf("public_id changed on transfer (%q → %q); links in the field would break", rep.PublicID, got.PublicID)
	}
	if got.DocPublic != "public doc" || got.DocInternal != "gate code 1234" {
		t.Fatalf("docs lost on transfer: %q / %q", got.DocPublic, got.DocInternal)
	}

	// The new owner no longer holds a share on their own node, nor its grants.
	if shared, _ := st.IsShared(ctx, rep.ID, target.ID); shared {
		t.Fatal("new owner still holds a share on their own repeater")
	}
	cmds, err := st.ListShareCommandIDs(ctx, rep.ID, target.ID)
	if err != nil {
		t.Fatalf("list share commands: %v", err)
	}
	if len(cmds) != 0 {
		t.Fatalf("new owner's share grants = %v, want none (share was dropped)", cmds)
	}

	// The outgoing owner keeps access, as a steward.
	steward, err := st.IsSteward(ctx, rep.ID, owner.ID)
	if err != nil {
		t.Fatalf("IsSteward(old owner): %v", err)
	}
	if !steward {
		t.Fatal("outgoing owner is not a steward; the handover should not be a cliff")
	}

	// Pending share links minted by the old owner are revoked.
	invites, err := st.ListInvites(ctx, rep.ID)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(invites) != 0 {
		t.Fatalf("ListInvites = %d, want 0 (links from the previous owner are revoked)", len(invites))
	}

	// History survives, and the handover itself is recorded in it.
	entries, err := st.ListMaintenance(ctx, rep.ID)
	if err != nil {
		t.Fatalf("list maintenance: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("maintenance entries = %d, want 2 (the original plus the transfer)", len(entries))
	}
	var logged bool
	for _, e := range entries {
		if strings.Contains(e.Note, "@owner") && strings.Contains(e.Note, "@target") {
			logged = true
		}
	}
	if !logged {
		t.Fatalf("no maintenance entry naming both parties; got %+v", entries)
	}
}

// TestTransferRepeaterRejectsNonSteward: a plain shared user is not a valid
// recipient. Only people the owner explicitly designated as co-maintainers are.
func TestTransferRepeaterRejectsNonSteward(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, target, rep := transferFixture(t, st, ctx, "b", false)

	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, target.ID); !errors.Is(err, ErrNotSteward) {
		t.Fatalf("transfer to non-steward err = %v, want ErrNotSteward", err)
	}
	if got, _ := st.GetRepeaterOwned(ctx, owner.ID, rep.ID); got == nil {
		t.Fatal("repeater left the original owner despite the rejected transfer")
	}

	// Nor is a stranger with no share at all.
	stranger, err := st.CreateUser(ctx, "stranger", "")
	if err != nil {
		t.Fatalf("create stranger: %v", err)
	}
	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, stranger.ID); !errors.Is(err, ErrNotSteward) {
		t.Fatalf("transfer to stranger err = %v, want ErrNotSteward", err)
	}

	// Nor the owner themselves.
	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, owner.ID); !errors.Is(err, ErrNotSteward) {
		t.Fatalf("self-transfer err = %v, want ErrNotSteward", err)
	}
}

// TestTransferRepeaterRejectsDemotedSteward is the stale-form regression: the
// steward flag is re-checked at write time, so a recipient demoted after the
// transfer page rendered does not receive the node.
func TestTransferRepeaterRejectsDemotedSteward(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, target, rep := transferFixture(t, st, ctx, "c", true)

	// The page rendered while they were a steward; they are demoted before submit.
	if err := st.SetShareSteward(ctx, rep.ID, target.ID, false); err != nil {
		t.Fatalf("demote steward: %v", err)
	}
	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, target.ID); !errors.Is(err, ErrNotSteward) {
		t.Fatalf("transfer to demoted steward err = %v, want ErrNotSteward", err)
	}
	if got, err := st.GetRepeaterOwned(ctx, owner.ID, rep.ID); err != nil || got.OwnerID != owner.ID {
		t.Fatalf("ownership moved despite the rejected transfer (%v)", err)
	}
}

// TestTransferRepeaterRequiresOwnership: only the current owner can transfer.
// A steward — who has full command access — must not be able to take the node,
// and a second transfer by the previous owner must fail once it has moved on.
func TestTransferRepeaterRequiresOwnership(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, target, rep := transferFixture(t, st, ctx, "d", true)

	// The steward can't help themselves to it.
	if err := st.TransferRepeaterToSteward(ctx, target.ID, rep.ID, target.ID); !errors.Is(err, ErrNotSteward) {
		t.Fatalf("steward self-serve transfer err = %v, want a rejection", err)
	}
	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, target.ID); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	// The old owner is now only a steward; they can't transfer it again (to
	// themselves or anyone else). This is also the concurrent-double-transfer
	// outcome: the loser re-reads an owner_id that is no longer theirs.
	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, owner.ID); !errors.Is(err, ErrNotSteward) {
		t.Fatalf("re-transfer by previous owner err = %v, want a rejection", err)
	}
	third, err := st.CreateUser(ctx, "third", "")
	if err != nil {
		t.Fatalf("create third: %v", err)
	}
	if _, err := st.AddShare(ctx, rep.ID, third.ID); err != nil {
		t.Fatalf("add share: %v", err)
	}
	if err := st.SetShareSteward(ctx, rep.ID, third.ID, true); err != nil {
		t.Fatalf("set steward: %v", err)
	}
	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, third.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("transfer by previous owner err = %v, want ErrNotFound", err)
	}
}

// TestTransferRepeaterDuplicateKey: the recipient already registered the same
// physical node under their own account. repeaters is UNIQUE (owner_id,
// public_key_hex), so the transfer has nowhere to land — it must surface as
// ErrDuplicate (a real message) and roll back entirely, not a 500 or a partial
// handover.
func TestTransferRepeaterDuplicateKey(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, target, rep := transferFixture(t, st, ctx, "e", true)

	if _, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: target.ID, Name: "Mine", PublicKeyHex: rep.PublicKeyHex,
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	}); err != nil {
		t.Fatalf("create recipient's own copy: %v", err)
	}

	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, target.ID); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("transfer with colliding key err = %v, want ErrDuplicate", err)
	}
	// Rolled back: ownership unchanged, and the recipient still holds their share
	// rather than having it deleted by a half-applied transfer.
	if got, err := st.GetRepeaterOwned(ctx, owner.ID, rep.ID); err != nil || got.OwnerID != owner.ID {
		t.Fatalf("ownership changed despite the failed transfer (%v)", err)
	}
	if shared, _ := st.IsShared(ctx, rep.ID, target.ID); !shared {
		t.Fatal("recipient's share was dropped by a transfer that failed")
	}
}

// TestTransferRepeaterSweepsStaleOrgExcludes: an exclude row is the OLD owner's
// opt-out. After the handover, one naming an org the new owner isn't in can never
// apply again (participation requires the owner be a member), so it is swept;
// one for an org they share still means something and is kept, so visibility
// doesn't silently flip back on.
func TestTransferRepeaterSweepsStaleOrgExcludes(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	owner, target, rep := transferFixture(t, st, ctx, "f", true)

	// oldOnly: only the outgoing owner is a member. shared: both are.
	oldOnly, err := st.CreateOrg(ctx, "Old Only", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	shared, err := st.CreateOrg(ctx, "Shared", owner.ID)
	if err != nil {
		t.Fatalf("create org: %v", err)
	}
	if err := st.AddOrgMember(ctx, shared.ID, target.ID, "member"); err != nil {
		t.Fatalf("add org member: %v", err)
	}
	for _, org := range []*Org{oldOnly, shared} {
		if err := st.SetRepeaterOrgExcluded(ctx, org.ID, rep.ID, true); err != nil {
			t.Fatalf("exclude from org %d: %v", org.ID, err)
		}
	}

	if err := st.TransferRepeaterToSteward(ctx, owner.ID, rep.ID, target.ID); err != nil {
		t.Fatalf("TransferRepeaterToSteward: %v", err)
	}

	stale, err := st.IsRepeaterOrgExcluded(ctx, oldOnly.ID, rep.ID)
	if err != nil {
		t.Fatalf("IsRepeaterOrgExcluded: %v", err)
	}
	if stale {
		t.Fatal("exclude for an org the new owner isn't in survived; it can never apply again")
	}
	kept, err := st.IsRepeaterOrgExcluded(ctx, shared.ID, rep.ID)
	if err != nil {
		t.Fatalf("IsRepeaterOrgExcluded: %v", err)
	}
	if !kept {
		t.Fatal("exclude for a shared org was swept; the node would silently become visible there")
	}
}
