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
