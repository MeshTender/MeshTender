package store

import "testing"

func TestAuthCodes(t *testing.T) {
	st, ctx := orgTestStore(t)
	u, err := st.CreateUser(ctx, "codeuser", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Round-trip: a freshly minted code redeems once to its user and next path.
	code, err := st.CreateAuthCode(ctx, u.ID, "/repeaters")
	if err != nil {
		t.Fatalf("create auth code: %v", err)
	}
	gotID, gotNext, ok, err := st.ConsumeAuthCode(ctx, code)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !ok || gotID != u.ID || gotNext != "/repeaters" {
		t.Fatalf("consume = (%d, %q, %v), want (%d, /repeaters, true)", gotID, gotNext, ok, u.ID)
	}

	// Single-use: the same code can't be redeemed twice.
	if _, _, ok, err := st.ConsumeAuthCode(ctx, code); err != nil || ok {
		t.Fatalf("second consume = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	// Unknown codes are a soft failure, not an error.
	if _, _, ok, err := st.ConsumeAuthCode(ctx, "nope"); err != nil || ok {
		t.Fatalf("unknown consume = (ok=%v, err=%v), want (false, nil)", ok, err)
	}

	// Empty next defaults to "/".
	code2, _ := st.CreateAuthCode(ctx, u.ID, "")
	if _, gotNext, _, _ := st.ConsumeAuthCode(ctx, code2); gotNext != "/" {
		t.Fatalf("empty next = %q, want /", gotNext)
	}

	// Expired codes don't redeem.
	if _, err := st.pool.Exec(ctx,
		`INSERT INTO auth_codes (code, user_id, next, expiry) VALUES ('stale', $1, '/', now() - interval '1 minute')`,
		u.ID); err != nil {
		t.Fatalf("insert expired: %v", err)
	}
	if _, _, ok, err := st.ConsumeAuthCode(ctx, "stale"); err != nil || ok {
		t.Fatalf("expired consume = (ok=%v, err=%v), want (false, nil)", ok, err)
	}
}
