package store

import (
	"strings"
	"testing"
)

// TestReplaceServerIdentityIfUnused covers the three outcomes that decide whether a
// restore is safe. The refusal is the important one: installing a different identity
// while repeaters are registered would leave every one of them holding an ACL entry for
// a key MeshTender no longer has.
func TestReplaceServerIdentityIfUnused(t *testing.T) {
	t.Parallel()

	const (
		keyA = "aaaa000000000000000000000000000000000000000000000000000000000001"
		keyB = "bbbb000000000000000000000000000000000000000000000000000000000002"
	)

	t.Run("installs when nothing is stored", func(t *testing.T) {
		t.Parallel()
		st, ctx := orgTestStore(t)
		got, err := st.ReplaceServerIdentityIfUnused(ctx, keyA, []byte("sealed-a"))
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
		if got != RestoreInstalled {
			t.Fatalf("outcome = %v, want RestoreInstalled", got)
		}
		pub, sealed, err := st.GetServerIdentity(ctx)
		if err != nil {
			t.Fatalf("get identity: %v", err)
		}
		if pub != keyA || string(sealed) != "sealed-a" {
			t.Errorf("stored (%s, %q), want (%s, sealed-a)", pub, sealed, keyA)
		}
	})

	t.Run("no-op when the backup is already current", func(t *testing.T) {
		t.Parallel()
		st, ctx := orgTestStore(t)
		if err := st.InsertServerIdentity(ctx, keyA, []byte("sealed-a")); err != nil {
			t.Fatalf("seed identity: %v", err)
		}
		got, err := st.ReplaceServerIdentityIfUnused(ctx, keyA, []byte("sealed-a-again"))
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
		if got != RestoreAlreadyCurrent {
			t.Fatalf("outcome = %v, want RestoreAlreadyCurrent", got)
		}
		// Nothing may have been written — re-pasting the same backup must be inert.
		_, sealed, err := st.GetServerIdentity(ctx)
		if err != nil {
			t.Fatalf("get identity: %v", err)
		}
		if string(sealed) != "sealed-a" {
			t.Errorf("sealed seed was rewritten to %q on a no-op", sealed)
		}
	})

	t.Run("replaces a different identity when no repeaters exist", func(t *testing.T) {
		t.Parallel()
		st, ctx := orgTestStore(t)
		if err := st.InsertServerIdentity(ctx, keyA, []byte("sealed-a")); err != nil {
			t.Fatalf("seed identity: %v", err)
		}
		// This is the real disaster-recovery shape: a throwaway identity minted at boot
		// on an empty database, with nothing registered against it yet.
		got, err := st.ReplaceServerIdentityIfUnused(ctx, keyB, []byte("sealed-b"))
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
		if got != RestoreInstalled {
			t.Fatalf("outcome = %v, want RestoreInstalled", got)
		}
		pub, _, err := st.GetServerIdentity(ctx)
		if err != nil {
			t.Fatalf("get identity: %v", err)
		}
		if pub != keyB {
			t.Errorf("stored identity = %s, want %s", pub, keyB)
		}
	})

	t.Run("refuses a different identity once repeaters are registered", func(t *testing.T) {
		t.Parallel()
		st, ctx := orgTestStore(t)
		if err := st.InsertServerIdentity(ctx, keyA, []byte("sealed-a")); err != nil {
			t.Fatalf("seed identity: %v", err)
		}
		owner, err := st.CreateUser(ctx, "identityowner", "")
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		if _, err := st.CreateRepeater(ctx, &Repeater{
			OwnerID: owner.ID, Name: "In Field", PublicKeyHex: strings.Repeat("c", 64),
			RadioFreqHz: 1, RadioBwHz: 1, RadioSF: 11, RadioCR: 5,
		}); err != nil {
			t.Fatalf("create repeater: %v", err)
		}

		got, err := st.ReplaceServerIdentityIfUnused(ctx, keyB, []byte("sealed-b"))
		if err != nil {
			t.Fatalf("restore: %v", err)
		}
		if got != RestoreRefusedInUse {
			t.Fatalf("outcome = %v, want RestoreRefusedInUse", got)
		}
		// And the refusal must not have written anything.
		pub, sealed, err := st.GetServerIdentity(ctx)
		if err != nil {
			t.Fatalf("get identity: %v", err)
		}
		if pub != keyA || string(sealed) != "sealed-a" {
			t.Errorf("a refused restore still modified the identity: (%s, %q)", pub, sealed)
		}
	})
}
