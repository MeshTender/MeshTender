package store

import (
	"errors"
	"strings"
	"testing"
)

// TestAcceptInvite covers the atomic redemption path: a successful accept consumes
// the link, creates the share, and seeds the default command set — all together —
// and the single-use guard rejects any later accept.
func TestAcceptInvite(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "owner", "")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner.ID, Name: "Rep", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	token, err := st.CreateInvite(ctx, rep.ID, "join")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	invitee, err := st.CreateUser(ctx, "invitee", "")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}

	added, err := st.AcceptInvite(ctx, token, invitee.ID)
	if err != nil {
		t.Fatalf("AcceptInvite: %v", err)
	}
	if !added {
		t.Fatalf("AcceptInvite added = false, want true (new share)")
	}

	// All three effects must be present together.
	shared, err := st.IsShared(ctx, rep.ID, invitee.ID)
	if err != nil || !shared {
		t.Fatalf("IsShared = %v, %v; want true, nil", shared, err)
	}
	cmds, err := st.ListShareCommandIDs(ctx, rep.ID, invitee.ID)
	if err != nil {
		t.Fatalf("ListShareCommandIDs: %v", err)
	}
	if len(cmds) == 0 {
		t.Fatalf("no default commands seeded for the new share")
	}
	invites, err := st.ListInvites(ctx, rep.ID)
	if err != nil || len(invites) != 1 {
		t.Fatalf("ListInvites = %d invites, %v; want 1", len(invites), err)
	}
	if invites[0].UsedAt == nil {
		t.Fatalf("invite not marked consumed after accept")
	}
	if invites[0].UsedByName == nil || *invites[0].UsedByName != "invitee" {
		t.Fatalf("invite UsedByName = %v, want invitee", invites[0].UsedByName)
	}

	// Single-use: a second accept (by anyone) must fail and grant nothing.
	other, err := st.CreateUser(ctx, "other", "")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	if _, err := st.AcceptInvite(ctx, token, other.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second AcceptInvite err = %v, want ErrNotFound", err)
	}
	if shared, _ := st.IsShared(ctx, rep.ID, other.ID); shared {
		t.Fatalf("second accepter got a share despite the spent link")
	}
}

// TestAcceptInviteRollsBack is the atomicity regression: if any step of the
// redemption fails, the whole thing must roll back — the link stays unspent so it
// can still be redeemed, rather than being consumed with no access granted (the
// reported bug). We trigger a failure by redeeming for a non-existent user, which
// violates the used_by/user_id foreign keys inside the transaction.
func TestAcceptInviteRollsBack(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	owner, err := st.CreateUser(ctx, "owner", "")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner.ID, Name: "Rep", PublicKeyHex: strings.Repeat("b", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	token, err := st.CreateInvite(ctx, rep.ID, "join")
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// A doomed accept: the user does not exist, so the transaction fails.
	if _, err := st.AcceptInvite(ctx, token, 999999); err == nil {
		t.Fatalf("AcceptInvite for non-existent user succeeded, want error")
	}

	// The link must NOT have been consumed by the failed attempt.
	invites, err := st.ListInvites(ctx, rep.ID)
	if err != nil || len(invites) != 1 {
		t.Fatalf("ListInvites = %d, %v; want 1", len(invites), err)
	}
	if invites[0].UsedAt != nil {
		t.Fatalf("link was consumed despite the accept failing (non-atomic)")
	}

	// And a real user can still redeem it afterwards.
	invitee, err := st.CreateUser(ctx, "invitee", "")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}
	added, err := st.AcceptInvite(ctx, token, invitee.ID)
	if err != nil || !added {
		t.Fatalf("AcceptInvite after failed attempt = %v, %v; want true, nil", added, err)
	}
}

// TestAcceptInviteUnknownToken: an unknown/invalid token yields ErrNotFound.
func TestAcceptInviteUnknownToken(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u, err := st.CreateUser(ctx, "u", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := st.AcceptInvite(ctx, "nope", u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AcceptInvite(unknown) err = %v, want ErrNotFound", err)
	}
}
