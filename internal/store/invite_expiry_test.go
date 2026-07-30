package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// inviteFixture builds an owner + repeater and returns them, for the expiry tests.
func inviteFixture(t *testing.T, st *Store, ctx context.Context, owner string) (*User, *Repeater) {
	t.Helper()
	u, err := st.CreateUser(ctx, owner, "")
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: u.ID, Name: "Rep", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	return u, rep
}

// expireInvite backdates a link's expiry by a minute so it reads as just-lapsed,
// without waiting out the real TTL.
func expireInvite(t *testing.T, st *Store, ctx context.Context, token string) {
	t.Helper()
	setInviteExpiry(t, st, ctx, token, -time.Minute)
}

// setInviteExpiry moves a link's expiry by offset relative to now (negative = in the
// past), for exercising the prune grace window without waiting weeks.
func setInviteExpiry(t *testing.T, st *Store, ctx context.Context, token string, offset time.Duration) {
	t.Helper()
	if _, err := st.pool.Exec(ctx,
		`UPDATE repeater_invites SET expires_at = now() + $2 WHERE token = $1`,
		token, offset); err != nil {
		t.Fatalf("set invite expiry: %v", err)
	}
}

// TestCreateInviteSetsExpiry: a new link gets a stored expiry roughly InviteTTL out.
// Storing it (rather than deriving it from created_at at query time) is what makes a
// later change to InviteTTL affect only new links — an outstanding link keeps the
// expiry it was minted with, so lengthening the constant can't revive dead links.
func TestCreateInviteSetsExpiry(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	_, rep := inviteFixture(t, st, ctx, "expiry-owner")

	before := time.Now()
	token, err := st.CreateInvite(ctx, rep.ID, "join", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	invites, err := st.ListInvites(ctx, rep.ID)
	if err != nil || len(invites) != 1 {
		t.Fatalf("ListInvites = %d, %v; want 1", len(invites), err)
	}
	inv := invites[0]
	if inv.Token != token {
		t.Fatalf("token = %q, want %q", inv.Token, token)
	}
	if inv.ExpiresAt.IsZero() {
		t.Fatal("ExpiresAt is zero — a link must carry its own expiry")
	}
	// Allow a generous window for clock skew between the test and the database.
	want := before.Add(InviteTTL)
	if delta := inv.ExpiresAt.Sub(want); delta < -time.Minute || delta > time.Minute {
		t.Errorf("ExpiresAt = %v, want ~%v (InviteTTL out); off by %v", inv.ExpiresAt, want, delta)
	}
	if inv.Expired() {
		t.Error("a freshly minted link reports Expired()")
	}
}

// TestExpiredInviteCannotBeRedeemed is the finding itself: before this change a
// share link stayed valid forever, so one pasted into a chat channel years earlier
// still granted repeater access to whoever found it first.
func TestExpiredInviteCannotBeRedeemed(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	_, rep := inviteFixture(t, st, ctx, "stale-owner")

	token, err := st.CreateInvite(ctx, rep.ID, "join", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	invitee, err := st.CreateUser(ctx, "stale-invitee", "")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}

	expireInvite(t, st, ctx, token)

	// The redemption path must refuse it.
	if _, err := st.AcceptInvite(ctx, token, invitee.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AcceptInvite(expired) err = %v, want ErrNotFound", err)
	}
	// And no share may have been granted as a side effect.
	shares, err := st.ListShares(ctx, rep.ID)
	if err != nil {
		t.Fatalf("list shares: %v", err)
	}
	for _, sh := range shares {
		if sh.UserID == invitee.ID {
			t.Fatal("an expired link granted access")
		}
	}

	// The landing page that previews the link must not resolve it either — otherwise
	// a recipient sees a working invite page whose accept then fails.
	if _, err := st.RepeaterByInviteToken(ctx, invitee.ID, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RepeaterByInviteToken(expired) err = %v, want ErrNotFound", err)
	}
}

// TestUnexpiredInviteStillWorks guards the obvious over-correction: the expiry check
// must not break links that are still inside their window.
func TestUnexpiredInviteStillWorks(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	_, rep := inviteFixture(t, st, ctx, "live-owner")

	token, err := st.CreateInvite(ctx, rep.ID, "join", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	invitee, err := st.CreateUser(ctx, "live-invitee", "")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}
	if _, err := st.RepeaterByInviteToken(ctx, invitee.ID, token); err != nil {
		t.Fatalf("RepeaterByInviteToken(live) = %v, want success", err)
	}
	added, err := st.AcceptInvite(ctx, token, invitee.ID)
	if err != nil || !added {
		t.Fatalf("AcceptInvite(live) = (%v, %v), want (true, nil)", added, err)
	}
}

// TestListInvitesShowsExpired: an owner needs to see that a link is spent — hiding
// expired rows would leave them wondering where the link went, with no way to tidy
// up. The store returns them; Expired() is how the UI marks them.
func TestListInvitesShowsExpired(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	_, rep := inviteFixture(t, st, ctx, "list-owner")

	token, err := st.CreateInvite(ctx, rep.ID, "join", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	expireInvite(t, st, ctx, token)

	invites, err := st.ListInvites(ctx, rep.ID)
	if err != nil || len(invites) != 1 {
		t.Fatalf("ListInvites = %d, %v; want the expired link still listed", len(invites), err)
	}
	if !invites[0].Expired() {
		t.Errorf("Expired() = false for a link whose expires_at is %v", invites[0].ExpiresAt)
	}
}

// TestPruneInvitesKeepsRecentlyExpired is the reason the sweep has a grace period:
// the share page lists expired links with an Expired badge and a Remove button, and
// the janitor runs every few minutes. Deleting on expiry would make that state
// unobservable and the button unreachable, so a just-lapsed link must survive a
// sweep. Only links long past their expiry get collected.
func TestPruneInvitesKeepsRecentlyExpired(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	_, rep := inviteFixture(t, st, ctx, "prune-owner")

	live, err := st.CreateInvite(ctx, rep.ID, "live", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	justLapsed, err := st.CreateInvite(ctx, rep.ID, "just lapsed", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	ancient, err := st.CreateInvite(ctx, rep.ID, "ancient", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	// Lapsed a minute ago: the owner should still see it.
	expireInvite(t, st, ctx, justLapsed)
	// Lapsed well beyond the grace window: collectable.
	setInviteExpiry(t, st, ctx, ancient, -(ExpiredInviteGrace + 24*time.Hour))

	n, err := st.PruneInvites(ctx)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("prune removed %d, want 1 (only the long-expired link)", n)
	}

	invites, err := st.ListInvites(ctx, rep.ID)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	kept := map[string]bool{}
	for _, inv := range invites {
		kept[inv.Token] = true
	}
	if !kept[live] {
		t.Error("the live link was pruned")
	}
	if !kept[justLapsed] {
		t.Error("a just-expired link was pruned — the owner never gets to see or remove it")
	}
	if kept[ancient] {
		t.Error("a link long past the grace window survived the sweep")
	}

	// Idempotent: nothing else is collectable yet.
	if n, err := st.PruneInvites(ctx); err != nil || n != 0 {
		t.Fatalf("second prune = (%d, %v), want (0, nil)", n, err)
	}
}

// TestPruneInvitesGraceBoundary pins the edge: a link that lapsed just inside the
// window stays, one just outside goes.
func TestPruneInvitesGraceBoundary(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	_, rep := inviteFixture(t, st, ctx, "boundary-owner")

	inside, err := st.CreateInvite(ctx, rep.ID, "inside grace", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	outside, err := st.CreateInvite(ctx, rep.ID, "outside grace", nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	// An hour either side of the boundary, so clock skew can't decide the outcome.
	setInviteExpiry(t, st, ctx, inside, -(ExpiredInviteGrace - time.Hour))
	setInviteExpiry(t, st, ctx, outside, -(ExpiredInviteGrace + time.Hour))

	if _, err := st.PruneInvites(ctx); err != nil {
		t.Fatalf("prune: %v", err)
	}
	invites, err := st.ListInvites(ctx, rep.ID)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	kept := map[string]bool{}
	for _, inv := range invites {
		kept[inv.Token] = true
	}
	if !kept[inside] {
		t.Errorf("a link %v past expiry was pruned; grace is %v", ExpiredInviteGrace-time.Hour, ExpiredInviteGrace)
	}
	if kept[outside] {
		t.Errorf("a link %v past expiry survived; grace is %v", ExpiredInviteGrace+time.Hour, ExpiredInviteGrace)
	}
}
