package store

import (
	"errors"
	"testing"
)

// TestReserveUserIDAndCreateWithID covers the deferred passkey-signup primitives:
// ReserveUserID hands out increasing ids without writing rows, and
// CreateUserWithID later persists the row at exactly that id (so the WebAuthn
// user handle, which is the id, still resolves).
func TestReserveUserIDAndCreateWithID(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)

	id1, err := st.ReserveUserID(ctx)
	if err != nil {
		t.Fatalf("reserve 1: %v", err)
	}
	id2, err := st.ReserveUserID(ctx)
	if err != nil {
		t.Fatalf("reserve 2: %v", err)
	}
	if id2 <= id1 {
		t.Fatalf("reserved ids not increasing: %d then %d", id1, id2)
	}

	// Reserving must not create any rows (that's the whole point — no orphans).
	var n int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if n != 0 {
		t.Fatalf("ReserveUserID created %d user rows, want 0", n)
	}

	// Create at the reserved id; it must round-trip through GetUserByID — the path
	// discoverable login uses to resolve a credential's handle back to an account.
	u, err := st.CreateUserWithID(ctx, id2, "deferred", "Deferred User")
	if err != nil {
		t.Fatalf("CreateUserWithID: %v", err)
	}
	if u.ID != id2 {
		t.Fatalf("created id = %d, want reserved %d", u.ID, id2)
	}
	got, err := st.GetUserByID(ctx, id2)
	if err != nil || got.Username != "deferred" {
		t.Fatalf("GetUserByID(%d) = %+v, %v", id2, got, err)
	}
	// The first real account still bootstraps instance caps.
	if !u.CapManageUsers || !u.CapManageCatalog {
		t.Fatalf("first account should bootstrap caps, got %+v", u)
	}

	// A duplicate username is rejected even via the explicit-id path.
	id3, _ := st.ReserveUserID(ctx)
	if _, err := st.CreateUserWithID(ctx, id3, "deferred", ""); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate username = %v, want ErrDuplicate", err)
	}
}
