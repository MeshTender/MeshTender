package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// userFK is one foreign key pointing at users(id), with its ON DELETE action.
type userFK struct {
	Table  string
	Column string
	Action rune // pg_constraint.confdeltype: 'c' cascade, 'n' set null, 'a'/'r' block
}

// userFKs reads every FK referencing users(id) straight from the catalog, so the
// tests below cover the schema as it actually is rather than a list that rots.
func userFKs(t *testing.T, st *Store, ctx context.Context) []userFK {
	t.Helper()
	rows, err := st.pool.Query(ctx, `
		SELECT c.conrelid::regclass::text, a.attname, c.confdeltype::text
		FROM pg_constraint c
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = ANY (c.conkey)
		WHERE c.contype = 'f' AND c.confrelid = 'users'::regclass
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatalf("read user FKs: %v", err)
	}
	fks, err := collectRows(rows, func(r pgx.Row) (userFK, error) {
		var f userFK
		var action string
		err := r.Scan(&f.Table, &f.Column, &action)
		if action != "" {
			f.Action = rune(action[0])
		}
		return f, err
	})
	if err != nil {
		t.Fatalf("scan user FKs: %v", err)
	}
	return fks
}

// bootstrapAdmin creates the instance's first account, which store.CreateUser
// automatically promotes to superadmin. Deletion tests call this first so their
// subject is an ORDINARY user — otherwise the subject is itself the last site
// admin and every deletion is (correctly) refused.
func bootstrapAdmin(t *testing.T, st *Store, ctx context.Context) *User {
	t.Helper()
	admin, err := st.CreateUser(ctx, "instanceadmin", "")
	if err != nil {
		t.Fatalf("create bootstrap admin: %v", err)
	}
	if !admin.CapManageUsers {
		t.Fatal("the first account was not promoted to site admin; fixture assumption is stale")
	}
	return admin
}

// populatedDeletionFixture builds an account that has touched as much of the
// schema as it reasonably can — owned repeater, someone else's shared repeater,
// passkey, login, auth code, email token, profile link, org membership, console
// session, command log, maintenance entry, rename history — so the deletion
// tests are exercising real rows rather than an empty account.
func populatedDeletionFixture(t *testing.T, st *Store, ctx context.Context) (victim *User, ownRepeater *Repeater) {
	t.Helper()
	bootstrapAdmin(t, st, ctx)
	victim, err := st.CreateUser(ctx, "victim", "Vic Tim")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	other, err := st.CreateUser(ctx, "bystander", "")
	if err != nil {
		t.Fatalf("create other: %v", err)
	}

	ownRepeater, err = st.CreateRepeater(ctx, &Repeater{
		OwnerID: victim.ID, Name: "Theirs", PublicKeyHex: strings.Repeat("a", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create repeater: %v", err)
	}
	// A repeater owned by someone else, shared with the victim (with a grant):
	// their access goes, the repeater itself must not.
	theirs, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: other.ID, Name: "Not theirs", PublicKeyHex: strings.Repeat("b", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatalf("create other repeater: %v", err)
	}
	if _, err := st.AddShare(ctx, theirs.ID, victim.ID); err != nil {
		t.Fatalf("add share: %v", err)
	}
	catalog, err := st.ListCommands(ctx)
	if err != nil || len(catalog) == 0 {
		t.Fatalf("list commands: %v", err)
	}
	if err := st.SetShareCommands(ctx, theirs.ID, victim.ID, []int64{catalog[0].ID}); err != nil {
		t.Fatalf("set share commands: %v", err)
	}

	if err := st.AddCredential(ctx, victim.ID, []byte("cred-id"), []byte(`{}`), "laptop"); err != nil {
		t.Fatalf("add credential: %v", err)
	}
	loginID, err := st.CreateLogin(ctx, victim.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}
	if _, err := st.CreateAuthCode(ctx, victim.ID, loginID, "/"); err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	if _, err := st.CreateEmailToken(ctx, victim.ID, PurposeResetPassword, "", time.Hour); err != nil {
		t.Fatalf("create email token: %v", err)
	}
	if err := st.ReplaceUserLinks(ctx, victim.ID, []UserLink{{Platform: "web", URL: "https://example.com"}}); err != nil {
		t.Fatalf("replace links: %v", err)
	}
	// A rename, so username_changes already holds rows for this user — including
	// the IP and user agent it records.
	if err := st.SetUsername(ctx, victim.ID, "victim2", UsernameChangeContext{
		ChangedBy: victim.ID, IP: "203.0.113.7", UserAgent: "Mozilla/5.0 (test)",
	}, false); err != nil {
		t.Fatalf("rename: %v", err)
	}
	// Console, command and maintenance history against SOMEONE ELSE'S repeater.
	// That's the case worth testing: activity on a node that outlives the account
	// must stay in its owner's audit trail, anonymized. (The same rows on their own
	// repeater would simply cascade away with it, proving nothing about SET NULL.)
	sessID, err := st.StartConsoleSession(ctx, theirs.ID, victim.ID)
	if err != nil {
		t.Fatalf("start console session: %v", err)
	}
	if _, err := st.LogCommand(ctx, theirs.ID, victim.ID, sessID, catalog[0].ID, "ver"); err != nil {
		t.Fatalf("log command: %v", err)
	}
	if err := st.AddMaintenanceEntry(ctx, theirs.ID, victim.ID, "Vic Tim", "swapped antenna", time.Now()); err != nil {
		t.Fatalf("add maintenance: %v", err)
	}
	return victim, ownRepeater
}

// TestDeleteUserLeavesNoReferences is the invariant that matters most for a
// privacy feature: after deletion, NO row anywhere still references the deleted
// id. It reads the FK list from the catalog rather than hardcoding tables, so a
// table added later without a cascade fails here instead of quietly retaining
// personal data (or blocking deletion outright with a NO ACTION rule).
func TestDeleteUserLeavesNoReferences(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	victim, _ := populatedDeletionFixture(t, st, ctx)

	fks := userFKs(t, st, ctx)
	// Guard the guard: if the introspection query ever stops finding anything,
	// this test would pass by checking nothing at all.
	if len(fks) < 15 {
		t.Fatalf("found only %d FKs referencing users; introspection is broken", len(fks))
	}
	for _, fk := range fks {
		if fk.Action != 'c' && fk.Action != 'n' {
			t.Errorf("%s.%s references users with ON DELETE %q: deletion would be blocked, "+
				"not cascaded — every reference to a user must cascade or null out",
				fk.Table, fk.Column, string(fk.Action))
		}
	}

	if err := st.DeleteUser(ctx, victim.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	for _, fk := range fks {
		var n int
		q := "SELECT count(*) FROM " + fk.Table + " WHERE " + fk.Column + " = $1" //nolint:gosec // identifiers come from the catalog, not user input
		if err := st.pool.QueryRow(ctx, q, victim.ID).Scan(&n); err != nil {
			t.Fatalf("count %s.%s: %v", fk.Table, fk.Column, err)
		}
		if n != 0 {
			t.Errorf("%s.%s still has %d row(s) referencing the deleted user", fk.Table, fk.Column, n)
		}
	}
	if _, err := st.GetUserByID(ctx, victim.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetUserByID after delete = %v, want ErrNotFound", err)
	}
}

// TestDeleteUserKeepsAnonymizedHistory: the operational record of what was done
// to a repeater must outlive the account that did it (migration 0020's promise),
// and other people's repeaters must survive their access being deleted.
func TestDeleteUserKeepsAnonymizedHistory(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	victim, ownRepeater := populatedDeletionFixture(t, st, ctx)

	// The victim's own repeater goes; the one merely shared with them stays.
	var otherRepeaters int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM repeaters WHERE owner_id <> $1`, victim.ID).Scan(&otherRepeaters); err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteUser(ctx, victim.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	var owned int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM repeaters WHERE public_id = $1`, ownRepeater.PublicID).Scan(&owned); err != nil {
		t.Fatal(err)
	}
	if owned != 0 {
		t.Fatal("the deleted user's own repeater survived")
	}
	var left int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM repeaters`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != otherRepeaters {
		t.Fatalf("repeaters left = %d, want %d (other people's must survive)", left, otherRepeaters)
	}

	// The command log keeps its write-time username snapshot, with the user nulled.
	var sender *string
	var uid *int64
	if err := st.pool.QueryRow(ctx,
		`SELECT sender_username, user_id FROM command_log LIMIT 1`).Scan(&sender, &uid); err != nil {
		t.Fatalf("read command log: %v", err)
	}
	if uid != nil {
		t.Fatal("command_log.user_id was not nulled")
	}
	if sender == nil || *sender == "" {
		t.Fatal("command_log lost its sender_username snapshot; the audit trail is now anonymous AND empty")
	}

	// Same for the maintenance history on the surviving repeater: the entry stays,
	// attributed to the write-time name rather than to nobody.
	entries, err := st.ListMaintenance(ctx, theirsID(t, st, ctx))
	if err != nil {
		t.Fatalf("list maintenance: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("maintenance entries = %d, want 1 (the entry outlives its author)", len(entries))
	}
	if entries[0].AuthorID != nil {
		t.Fatal("maintenance author_id was not nulled")
	}
	if entries[0].AuthorName != "Vic Tim" {
		t.Fatalf("maintenance author name = %q, want the write-time snapshot", entries[0].AuthorName)
	}
}

// theirsID returns the id of the surviving (other owner's) repeater in the
// deletion fixture.
func theirsID(t *testing.T, st *Store, ctx context.Context) int64 {
	t.Helper()
	var id int64
	if err := st.pool.QueryRow(ctx,
		`SELECT id FROM repeaters WHERE name = 'Not theirs'`).Scan(&id); err != nil {
		t.Fatalf("find surviving repeater: %v", err)
	}
	return id
}

// TestDeleteUserScrubsRenameHistory: the rename audit trail SURVIVES deletion
// (the username cooldown is built on it), so the personal data inside it has to
// be erased by hand — the FK only nulls user_id. An IP address left behind here
// would outlive the account forever, which is both a broken promise and the kind
// of thing a privacy policy must not have to be vague about.
func TestDeleteUserScrubsRenameHistory(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	victim, _ := populatedDeletionFixture(t, st, ctx)

	// Precondition: the rename really did record an IP and user agent.
	var before int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM username_changes WHERE ip IS NOT NULL OR user_agent IS NOT NULL`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Fatal("fixture recorded no IP/user agent; this test would prove nothing")
	}

	if err := st.DeleteUser(ctx, victim.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	var leaked int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM username_changes WHERE ip IS NOT NULL OR user_agent IS NOT NULL`).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Errorf("%d rename row(s) still hold an IP or user agent after the account was deleted", leaked)
	}
	// The rows themselves must remain, or the username cooldown has nothing to
	// reserve against.
	var rows int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM username_changes`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows == 0 {
		t.Fatal("the rename history was deleted outright; the username cooldown depends on it")
	}
}

// TestDeleteUserReservesUsername: a freed handle can't be claimed immediately.
// Public profiles live at /u/{username} and @handles are quoted in logs and
// maintenance notes, so an instantly-reusable name would let someone inherit a
// departed user's history.
func TestDeleteUserReservesUsername(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	bootstrapAdmin(t, st, ctx)
	u, err := st.CreateUser(ctx, "departed", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	// Nobody may take the freed name — not a new signup...
	if _, err := st.CreateUser(ctx, "departed", ""); !errors.Is(err, ErrUsernameReserved) {
		t.Fatalf("CreateUser on a freed name = %v, want ErrUsernameReserved", err)
	}
	// ...nor an existing account renaming into it.
	other, err := st.CreateUser(ctx, "opportunist", "")
	if err != nil {
		t.Fatal(err)
	}
	err = st.SetUsername(ctx, other.ID, "departed", UsernameChangeContext{ChangedBy: other.ID}, false)
	if !errors.Is(err, ErrUsernameReserved) {
		t.Fatalf("rename into a freed name = %v, want ErrUsernameReserved", err)
	}
}

// TestDeleteUserSiteAdminGuard: the last administrator can't delete themselves
// out of the instance, but one of two can.
func TestDeleteUserSiteAdminGuard(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	first, err := st.CreateUser(ctx, "admin1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCapabilities(ctx, first.ID, true, true); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(ctx, first.ID); !errors.Is(err, ErrLastSiteAdmin) {
		t.Fatalf("deleting the only admin = %v, want ErrLastSiteAdmin", err)
	}

	second, err := st.CreateUser(ctx, "admin2", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCapabilities(ctx, second.ID, true, false); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(ctx, first.ID); err != nil {
		t.Fatalf("deleting one of two admins: %v", err)
	}
	// And now the remaining one is the last, so they're stuck too.
	if err := st.DeleteUser(ctx, second.ID); !errors.Is(err, ErrLastSiteAdmin) {
		t.Fatalf("deleting the now-last admin = %v, want ErrLastSiteAdmin", err)
	}
}

// TestDeleteUserOrgRules covers all three org outcomes: an org with other members
// but no other admin BLOCKS deletion; a solo org is deleted with the account; an
// org with another admin simply loses a member and survives intact.
func TestDeleteUserOrgRules(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	bootstrapAdmin(t, st, ctx)
	leaver, err := st.CreateUser(ctx, "leaver", "")
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.CreateUser(ctx, "member", "")
	if err != nil {
		t.Fatal(err)
	}

	// (a) sole admin, other members present → blocked.
	shared, err := st.CreateOrg(ctx, "Shared Club", leaver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, shared.ID, member.ID, "member"); err != nil {
		t.Fatal(err)
	}
	// (b) solo org → goes with the account.
	solo, err := st.CreateOrg(ctx, "Solo Org", leaver.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := st.DeleteUser(ctx, leaver.ID); !errors.Is(err, ErrSoleOrgAdmin) {
		t.Fatalf("deleting the sole admin of a populated org = %v, want ErrSoleOrgAdmin", err)
	}
	// Nothing was half-applied by the refused deletion.
	if _, err := st.GetOrg(ctx, solo.ID); err != nil {
		t.Fatalf("solo org was deleted by a refused account deletion: %v", err)
	}
	if _, err := st.GetUserByID(ctx, leaver.ID); err != nil {
		t.Fatalf("user was deleted despite the block: %v", err)
	}

	// Promoting someone else clears the block.
	if err := st.SetOrgMemberRole(ctx, shared.ID, member.ID, "admin"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteUser(ctx, leaver.ID); err != nil {
		t.Fatalf("DeleteUser after promoting a second admin: %v", err)
	}

	// (c) the shared org survives, minus the leaver; the solo org is gone.
	if _, err := st.GetOrg(ctx, shared.ID); err != nil {
		t.Fatalf("shared org did not survive: %v", err)
	}
	members, err := st.ListOrgMembers(ctx, shared.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].UserID != member.ID {
		t.Fatalf("shared org members = %+v, want just the promoted member", members)
	}
	if _, err := st.GetOrg(ctx, solo.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("solo org = %v, want ErrNotFound (deleted with its only member)", err)
	}
}

// TestPreviewUserDeletion: the confirm page's numbers must match what deletion
// actually does — including the steward count that decides whether a repeater
// can be handed over instead of destroyed.
func TestPreviewUserDeletion(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	victim, ownRepeater := populatedDeletionFixture(t, st, ctx)

	// Give the owned repeater a steward, so the preview can offer a transfer.
	successor, err := st.CreateUser(ctx, "successor", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddShare(ctx, ownRepeater.ID, successor.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.SetShareSteward(ctx, ownRepeater.ID, successor.ID, true); err != nil {
		t.Fatal(err)
	}
	solo, err := st.CreateOrg(ctx, "Solo", victim.ID)
	if err != nil {
		t.Fatal(err)
	}

	p, err := st.PreviewUserDeletion(ctx, victim.ID)
	if err != nil {
		t.Fatalf("PreviewUserDeletion: %v", err)
	}
	if p.Blocked() {
		t.Fatalf("preview reports blocked, want deletable: %+v", p)
	}
	if len(p.Repeaters) != 1 || p.Repeaters[0].PublicID != ownRepeater.PublicID {
		t.Fatalf("preview repeaters = %+v, want the one they own", p.Repeaters)
	}
	if p.Repeaters[0].Stewards != 1 {
		t.Fatalf("preview steward count = %d, want 1 (a transfer is possible)", p.Repeaters[0].Stewards)
	}
	if len(p.OrgsDeleted) != 1 || p.OrgsDeleted[0].ID != solo.ID {
		t.Fatalf("preview OrgsDeleted = %+v, want the solo org", p.OrgsDeleted)
	}
	if p.SharedWithUser != 1 {
		t.Fatalf("preview SharedWithUser = %d, want 1", p.SharedWithUser)
	}
	if p.Passkeys != 1 {
		t.Fatalf("preview Passkeys = %d, want 1", p.Passkeys)
	}

	// A blocked account reports the blocking org rather than a clean bill.
	blocker, err := st.CreateUser(ctx, "blocked", "")
	if err != nil {
		t.Fatal(err)
	}
	club, err := st.CreateOrg(ctx, "Club", blocker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddOrgMember(ctx, club.ID, victim.ID, "member"); err != nil {
		t.Fatal(err)
	}
	bp, err := st.PreviewUserDeletion(ctx, blocker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bp.Blocked() || len(bp.OrgsBlocked) != 1 || bp.OrgsBlocked[0].ID != club.ID {
		t.Fatalf("preview for a sole admin = %+v, want blocked on the club", bp)
	}
}

// TestDeleteUserMissing: deleting an already-deleted account is ErrNotFound, not
// a silent success.
func TestDeleteUserMissing(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	if err := st.DeleteUser(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteUser(missing) = %v, want ErrNotFound", err)
	}
}
