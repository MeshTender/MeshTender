package store

import "testing"

func TestLogins(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u, err := st.CreateUser(ctx, "loginuser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	id, err := st.CreateLogin(ctx, u.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}

	// A fresh login is valid and maps back to its user.
	if uid, ok, err := st.LoginValid(ctx, id); err != nil || !ok || uid != u.ID {
		t.Fatalf("LoginValid = (%d, %v, %v), want (%d, true, nil)", uid, ok, err, u.ID)
	}

	// Revoking is idempotent and makes the login invalid.
	if err := st.RevokeLogin(ctx, id); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if err := st.RevokeLogin(ctx, id); err != nil {
		t.Fatalf("revoke again: %v", err)
	}
	if _, ok, _ := st.LoginValid(ctx, id); ok {
		t.Fatalf("revoked login still valid")
	}

	// Unknown ids are a soft miss, not an error.
	if _, ok, err := st.LoginValid(ctx, "nope"); err != nil || ok {
		t.Fatalf("unknown LoginValid = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	// RevokeAllUserLogins kills every active login for the user ("log out everywhere").
	a, _ := st.CreateLogin(ctx, u.ID)
	b, _ := st.CreateLogin(ctx, u.ID)
	if err := st.RevokeAllUserLogins(ctx, u.ID); err != nil {
		t.Fatalf("revoke all: %v", err)
	}
	if _, ok, _ := st.LoginValid(ctx, a); ok {
		t.Fatalf("login a still valid after revoke-all")
	}
	if _, ok, _ := st.LoginValid(ctx, b); ok {
		t.Fatalf("login b still valid after revoke-all")
	}
}
