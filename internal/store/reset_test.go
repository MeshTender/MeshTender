package store

import (
	"strings"
	"testing"
)

func TestReset(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	count := func(table string) int {
		var n int
		if err := st.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return n
	}

	// Seed preserved data (a credentialed user + server identity) and disposable
	// data (repeater + org). The owner gets a password so Reset keeps it.
	owner, err := st.CreateUser(ctx, "owner", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetPassword(ctx, owner.ID, "bcrypt-hash"); err != nil {
		t.Fatal(err)
	}
	// A seeded-style account with no password and no passkey: Reset should prune it.
	if _, err := st.CreateUser(ctx, "seeded", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertServerIdentity(ctx, strings.Repeat("a", 64), []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	rep, err := st.CreateRepeater(ctx, &Repeater{
		OwnerID: owner.ID, Name: "R", PublicKeyHex: strings.Repeat("b", 64),
		RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrg(ctx, "Region", owner.ID); err != nil {
		t.Fatal(err)
	}
	_ = rep

	catalogBefore := count("command_catalog")
	if catalogBefore == 0 {
		t.Fatal("expected a seeded command catalog")
	}

	removed, err := st.Reset(ctx)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	if removed != 1 {
		t.Errorf("credential-less users removed = %d, want 1", removed)
	}

	// Preserved: the credentialed user + identity + catalog survive; the seeded
	// (credential-less) user is gone.
	if got := count("users"); got != 1 {
		t.Errorf("users after reset = %d, want 1", got)
	}
	if got := count("server_identity"); got != 1 {
		t.Errorf("server_identity after reset = %d, want 1", got)
	}
	if got := count("command_catalog"); got != catalogBefore {
		t.Errorf("command_catalog after reset = %d, want %d", got, catalogBefore)
	}
	// Wiped: user content is gone.
	if got := count("repeaters"); got != 0 {
		t.Errorf("repeaters after reset = %d, want 0", got)
	}
	if got := count("organizations"); got != 0 {
		t.Errorf("organizations after reset = %d, want 0", got)
	}
	if got := count("org_members"); got != 0 {
		t.Errorf("org_members after reset = %d, want 0", got)
	}

	// The kept user can still be looked up (login still works).
	if _, err := st.GetUserByID(ctx, owner.ID); err != nil {
		t.Errorf("GetUserByID after reset: %v", err)
	}
}
