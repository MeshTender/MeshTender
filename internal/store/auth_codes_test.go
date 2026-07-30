package store

import "testing"

func TestAuthCodes(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u, err := st.CreateUser(ctx, "codeuser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	loginID, err := st.CreateLogin(ctx, u.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}

	// Round-trip: a freshly minted code redeems once to its user, login, and next.
	code, err := st.CreateAuthCode(ctx, u.ID, loginID, "/repeaters")
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	gotID, gotLogin, gotNext, ok, err := st.ConsumeAuthCode(ctx, code)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !ok || gotID != u.ID || gotLogin != loginID || gotNext != "/repeaters" {
		t.Fatalf("consume = (%d, %q, %q, %v), want (%d, %q, /repeaters, true)", gotID, gotLogin, gotNext, ok, u.ID, loginID)
	}

	// Single-use: the same code can't be redeemed twice.
	if _, _, _, ok, err := st.ConsumeAuthCode(ctx, code); err != nil || ok {
		t.Fatalf("second consume = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	// Unknown codes are a soft failure, not an error.
	if _, _, _, ok, err := st.ConsumeAuthCode(ctx, "nope"); err != nil || ok {
		t.Fatalf("unknown consume = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	// Empty next defaults to "/".
	code2, _ := st.CreateAuthCode(ctx, u.ID, loginID, "")
	if _, _, gotNext, _, _ := st.ConsumeAuthCode(ctx, code2); gotNext != "/" {
		t.Fatalf("empty next = %q, want /", gotNext)
	}

	// Expired codes don't redeem.
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO auth_codes (code, user_id, next, expiry) VALUES ('stale', $1, '/', now() - interval '1 minute')`,
		u.ID); err != nil {
		t.Fatalf("insert expired: %v", err)
	}
	if _, _, _, ok, err := st.ConsumeAuthCode(ctx, "stale"); err != nil || ok {
		t.Fatalf("expired consume = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}

// TestPruneAuthCodes covers the sweep that keeps the table from growing without
// bound: expired rows go, unexpired ones stay, and it's safe to run when there's
// nothing to do.
//
// Regression for audit finding S4 — ConsumeAuthCode only ever deletes codes that
// are actually redeemed, so abandoned sign-ins used to leave rows behind forever.
func TestPruneAuthCodes(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t)
	u, err := st.CreateUser(ctx, "pruneuser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	loginID, err := st.CreateLogin(ctx, u.ID)
	if err != nil {
		t.Fatalf("create login: %v", err)
	}

	// A live code (expires ~60s out) that must survive, and two already-expired ones.
	live, err := st.CreateAuthCode(ctx, u.ID, loginID, "/")
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	for _, code := range []string{"expired-1", "expired-2"} {
		if _, err := st.pool.Exec(ctx,
			`INSERT INTO auth_codes (code, user_id, next, expiry) VALUES ($1, $2, '/', now() - interval '1 minute')`,
			code, u.ID); err != nil {
			t.Fatalf("insert expired %s: %v", code, err)
		}
	}

	n, err := st.PruneAuthCodes(ctx)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Errorf("prune removed %d rows, want 2", n)
	}

	// The live code must still redeem — pruning must not touch valid handoffs.
	if _, _, _, ok, err := st.ConsumeAuthCode(ctx, live); err != nil || !ok {
		t.Fatalf("live code after prune = (ok=%v, err=%v), want (true, nil)", ok, err)
	}

	// Idempotent: nothing left to remove.
	if n, err := st.PruneAuthCodes(ctx); err != nil || n != 0 {
		t.Fatalf("second prune = (%d, %v), want (0, nil)", n, err)
	}
}
