package store

import (
	"context"
	"testing"
	"time"
)

// emailTestStore returns a Store on a fresh, throwaway database (see internal/testdb).
func emailTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	return orgTestStore(t)
}

// mkUser creates a user, optionally with a password hash and a verified address.
func mkUser(t *testing.T, st *Store, ctx context.Context, name, addr string, verified, withPassword bool) *User {
	t.Helper()
	u, err := st.CreateUser(ctx, name, "")
	if err != nil {
		t.Fatalf("create user %q: %v", name, err)
	}
	if withPassword {
		if err := st.SetPassword(ctx, u.ID, "$2a$10$notarealhashjustplaceholdervalue000000000000000000000"); err != nil {
			t.Fatalf("set password: %v", err)
		}
	}
	if addr != "" {
		if err := st.SetEmail(ctx, u.ID, addr); err != nil {
			t.Fatalf("set email: %v", err)
		}
		if verified {
			ok, err := st.MarkEmailVerified(ctx, u.ID, addr)
			if err != nil || !ok {
				t.Fatalf("mark verified: ok=%v err=%v", ok, err)
			}
		}
	}
	reloaded, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	return reloaded
}

// expireToken backdates a token so it reads as just-lapsed, without waiting out the
// real TTL.
func expireToken(t *testing.T, st *Store, ctx context.Context, raw string) {
	t.Helper()
	if _, err := st.pool.Exec(ctx,
		`UPDATE email_tokens SET expires_at = now() - interval '1 minute' WHERE token_hash = $1`,
		hashToken(raw)); err != nil {
		t.Fatalf("expire token: %v", err)
	}
}

// TestSetEmailStartsUnverified: a newly-set address is never trusted on the word of
// whoever typed it. If it were, a typo would silently point recovery at a stranger's
// mailbox — or nobody's.
func TestSetEmailStartsUnverified(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", false, true)

	if u.Email == nil || *u.Email != "alice@example.test" {
		t.Fatalf("Email = %v, want alice@example.test", u.Email)
	}
	if u.EmailVerified() {
		t.Error("EmailVerified() = true for a freshly set address")
	}
	if u.CanResetPassword() {
		t.Error("CanResetPassword() = true on an unverified address")
	}
}

// TestChangingEmailRevokesVerification: an address that was verified, then replaced,
// must go back to unverified — otherwise typing a new address would inherit the old
// one's trust and immediately be able to receive reset links.
func TestChangingEmailRevokesVerification(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)
	if !u.CanResetPassword() {
		t.Fatal("precondition: verified address with a password should be resettable")
	}

	if err := st.SetEmail(ctx, u.ID, "attacker@example.test"); err != nil {
		t.Fatalf("set email: %v", err)
	}
	after, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.EmailVerified() {
		t.Error("EmailVerified() = true after the address changed")
	}
}

// TestMarkEmailVerifiedRequiresMatchingAddress closes the swap window: request a
// link for address A, change the address to B, then click the old link. The stale
// link must not confirm B.
func TestMarkEmailVerifiedRequiresMatchingAddress(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", false, true)

	if err := st.SetEmail(ctx, u.ID, "second@example.test"); err != nil {
		t.Fatalf("set email: %v", err)
	}
	// The old link was issued for the first address.
	ok, err := st.MarkEmailVerified(ctx, u.ID, "alice@example.test")
	if err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if ok {
		t.Error("a link issued for the previous address verified the current one")
	}
	after, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.EmailVerified() {
		t.Error("address ended up verified")
	}
}

// TestMarkEmailVerifiedIsCaseInsensitive: mail providers don't distinguish case, so
// a link issued for "Alice@Example.test" must confirm the stored "alice@example.test".
func TestMarkEmailVerifiedIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", false, true)

	ok, err := st.MarkEmailVerified(ctx, u.ID, "Alice@Example.TEST")
	if err != nil {
		t.Fatalf("mark verified: %v", err)
	}
	if !ok {
		t.Fatal("case-differing address failed to verify")
	}
}

// TestConsumeEmailTokenSingleUse: a reset link is a credential, so redeeming it must
// spend it. A replayable link means a leaked mailbox archive stays dangerous forever.
func TestConsumeEmailTokenSingleUse(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)

	raw, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	tok, ok, err := st.ConsumeEmailToken(ctx, PurposeResetPassword, raw)
	if err != nil || !ok {
		t.Fatalf("first consume: ok=%v err=%v", ok, err)
	}
	if tok.UserID != u.ID {
		t.Errorf("UserID = %d, want %d", tok.UserID, u.ID)
	}
	if _, ok, err := st.ConsumeEmailToken(ctx, PurposeResetPassword, raw); err != nil || ok {
		t.Errorf("second consume: ok=%v err=%v, want ok=false", ok, err)
	}
}

// TestPeekEmailTokenDoesNotConsume: the GET that renders the reset form must leave
// the token spendable for the POST that follows, or every link would die on being
// opened (and mail clients that prefetch links would burn them sight unseen).
func TestPeekEmailTokenDoesNotConsume(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)

	raw, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	for i := range 2 {
		tok, ok, err := st.PeekEmailToken(ctx, PurposeResetPassword, raw)
		if err != nil || !ok {
			t.Fatalf("peek %d: ok=%v err=%v", i, ok, err)
		}
		if tok.UserID != u.ID {
			t.Errorf("peek %d: UserID = %d, want %d", i, tok.UserID, u.ID)
		}
	}
	if _, ok, _ := st.ConsumeEmailToken(ctx, PurposeResetPassword, raw); !ok {
		t.Error("token was not spendable after being peeked")
	}
}

// TestEmailTokenPurposeIsolation: a verification link must not be redeemable as a
// password reset. Without the purpose filter, the low-value token (mailed to an
// unverified, possibly mistyped address) would authorize taking over the account.
func TestEmailTokenPurposeIsolation(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", false, true)

	raw, err := st.CreateEmailToken(ctx, u.ID, PurposeVerifyEmail, "alice@example.test", VerifyTokenTTL)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if _, ok, err := st.ConsumeEmailToken(ctx, PurposeResetPassword, raw); err != nil || ok {
		t.Errorf("verify token redeemed as a reset: ok=%v err=%v", ok, err)
	}
	// Still good for what it was minted for.
	if _, ok, err := st.ConsumeEmailToken(ctx, PurposeVerifyEmail, raw); err != nil || !ok {
		t.Errorf("verify token failed for its own purpose: ok=%v err=%v", ok, err)
	}
}

// TestConsumeEmailTokenRejectsExpired: the short reset TTL is only real if expiry is
// enforced at redemption, not merely displayed.
func TestConsumeEmailTokenRejectsExpired(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)

	raw, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	expireToken(t, st, ctx, raw)

	if _, ok, err := st.PeekEmailToken(ctx, PurposeResetPassword, raw); err != nil || ok {
		t.Errorf("peek of expired token: ok=%v err=%v", ok, err)
	}
	if _, ok, err := st.ConsumeEmailToken(ctx, PurposeResetPassword, raw); err != nil || ok {
		t.Errorf("consume of expired token: ok=%v err=%v", ok, err)
	}
}

// TestEmailTokensStoreNoPlaintext: the point of hashing. If the raw token were
// stored, a leaked table would be a pile of live account-takeover links.
func TestEmailTokensStoreNoPlaintext(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)

	raw, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	var n int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_tokens WHERE token_hash = $1`, raw).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 0 {
		t.Error("the raw token is stored in token_hash")
	}
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_tokens WHERE token_hash = $1`, hashToken(raw)).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	if n != 1 {
		t.Errorf("hashed token rows = %d, want 1", n)
	}
}

// TestFindUsersByVerifiedEmailFanout: the address column is deliberately not unique
// (one person, a personal plus an ops account), so a lookup must return every match.
// Dropping any of them would make one account silently unrecoverable.
func TestFindUsersByVerifiedEmailFanout(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	const addr = "jon@example.test"
	mkUser(t, st, ctx, "jleight", addr, true, true)
	mkUser(t, st, ctx, "meshtender-ops", addr, true, true)
	mkUser(t, st, ctx, "someone-else", "other@example.test", true, true)

	found, err := st.FindUsersByVerifiedEmail(ctx, addr)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("found %d accounts, want 2", len(found))
	}
	if found[0].Username != "jleight" || found[1].Username != "meshtender-ops" {
		t.Errorf("usernames = %q/%q", found[0].Username, found[1].Username)
	}
}

// TestFindUsersByVerifiedEmailIgnoresUnverified: an address nobody has proven must
// never receive recovery mail — that's what makes a typo (or an address someone
// else owns) harmless.
func TestFindUsersByVerifiedEmailIgnoresUnverified(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	mkUser(t, st, ctx, "alice", "shared@example.test", false, true)
	mkUser(t, st, ctx, "bob", "shared@example.test", true, true)

	found, err := st.FindUsersByVerifiedEmail(ctx, "shared@example.test")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 1 || found[0].Username != "bob" {
		t.Fatalf("found %d accounts (%v), want only bob", len(found), usernames(found))
	}
}

// TestFindUsersByVerifiedEmailCaseInsensitive: people type their address however
// they remember it. A case-sensitive lookup would tell a legitimate user "no
// account here" and leave them stuck.
func TestFindUsersByVerifiedEmailCaseInsensitive(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	mkUser(t, st, ctx, "alice", "Alice@Example.test", true, true)

	for _, probe := range []string{"alice@example.test", "ALICE@EXAMPLE.TEST", "  Alice@Example.test  "} {
		found, err := st.FindUsersByVerifiedEmail(ctx, probe)
		if err != nil {
			t.Fatalf("find %q: %v", probe, err)
		}
		if len(found) != 1 {
			t.Errorf("find %q returned %d accounts, want 1", probe, len(found))
		}
	}
}

// TestFindUsersByVerifiedEmailUnknownIsEmpty: an unknown address is an ordinary
// outcome, not an error — the caller must not be able to tell it apart from a hit
// when reporting to the visitor.
func TestFindUsersByVerifiedEmailUnknownIsEmpty(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)

	found, err := st.FindUsersByVerifiedEmail(ctx, "nobody@example.test")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(found) != 0 {
		t.Errorf("found %d accounts, want 0", len(found))
	}
}

// TestCanResetPasswordRequiresPassword is the regression test for the rule that
// keeps a passkey-only account's security floor off the mailbox: email recovery only
// ever sets a password on an account that already has one. Relaxing this quietly
// would demote the strongest accounts to whoever controls the inbox.
func TestCanResetPasswordRequiresPassword(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	passkeyOnly := mkUser(t, st, ctx, "passkey-only", "pk@example.test", true, false)
	withPassword := mkUser(t, st, ctx, "has-password", "pw@example.test", true, true)

	if passkeyOnly.CanResetPassword() {
		t.Error("a passkey-only account reports itself resettable by email")
	}
	if !withPassword.CanResetPassword() {
		t.Error("a password account with a verified address is not resettable")
	}
}

// TestClearEmailDropsOutstandingTokens: disowning an address must kill links already
// sent to it, or a mailbox the user no longer controls keeps a live way in.
func TestClearEmailDropsOutstandingTokens(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)

	raw, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	if err := st.ClearEmail(ctx, u.ID); err != nil {
		t.Fatalf("clear email: %v", err)
	}
	if _, ok, err := st.ConsumeEmailToken(ctx, PurposeResetPassword, raw); err != nil || ok {
		t.Errorf("token still live after the address was cleared: ok=%v err=%v", ok, err)
	}
	after, err := st.GetUserByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if after.Email != nil {
		t.Errorf("Email = %v after clear, want nil", after.Email)
	}
}

// TestDeleteEmailTokensScopedToPurpose: a completed reset invalidates the other
// reset links from that request, but must not sweep away an unrelated pending
// address verification.
func TestDeleteEmailTokensScopedToPurpose(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)

	reset, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL)
	if err != nil {
		t.Fatalf("create reset token: %v", err)
	}
	verify, err := st.CreateEmailToken(ctx, u.ID, PurposeVerifyEmail, "alice@example.test", VerifyTokenTTL)
	if err != nil {
		t.Fatalf("create verify token: %v", err)
	}

	if err := st.DeleteEmailTokens(ctx, u.ID, PurposeResetPassword); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, _ := st.ConsumeEmailToken(ctx, PurposeResetPassword, reset); ok {
		t.Error("reset token survived")
	}
	if _, ok, _ := st.ConsumeEmailToken(ctx, PurposeVerifyEmail, verify); !ok {
		t.Error("verification token was swept away with the reset tokens")
	}
}

// TestCountRecentEmailTokens backs the per-account cap that keeps us from being used
// to flood someone's inbox. Only tokens inside the window count.
func TestCountRecentEmailTokens(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)

	for range 3 {
		if _, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL); err != nil {
			t.Fatalf("create token: %v", err)
		}
	}
	// A verify token must not count against the reset budget.
	if _, err := st.CreateEmailToken(ctx, u.ID, PurposeVerifyEmail, "alice@example.test", VerifyTokenTTL); err != nil {
		t.Fatalf("create verify token: %v", err)
	}

	n, err := st.CountRecentEmailTokens(ctx, u.ID, PurposeResetPassword, time.Hour)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}

	// Backdate them out of the window; the budget refills.
	if _, err := st.pool.Exec(ctx,
		`UPDATE email_tokens SET created_at = now() - interval '2 hours' WHERE user_id = $1`, u.ID); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	n, err = st.CountRecentEmailTokens(ctx, u.ID, PurposeResetPassword, time.Hour)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("count after backdating = %d, want 0", n)
	}
}

// TestPruneEmailTokens: redemption only deletes tokens someone actually clicked, so
// the janitor is the only thing keeping abandoned ones from accumulating forever.
func TestPruneEmailTokens(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)

	stale, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	live, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL)
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	expireToken(t, st, ctx, stale)

	n, err := st.PruneEmailTokens(ctx)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	if _, ok, _ := st.ConsumeEmailToken(ctx, PurposeResetPassword, live); !ok {
		t.Error("prune took an unexpired token")
	}
}

// TestUserDeletionCascadesEmailTokens: the tokens reference users, so removing an
// account (the --reset sweep today, account deletion later) must not leave orphans.
func TestUserDeletionCascadesEmailTokens(t *testing.T) {
	t.Parallel()
	st, ctx := emailTestStore(t)
	u := mkUser(t, st, ctx, "alice", "alice@example.test", true, true)
	if _, err := st.CreateEmailToken(ctx, u.ID, PurposeResetPassword, "", ResetTokenTTL); err != nil {
		t.Fatalf("create token: %v", err)
	}

	if _, err := st.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, u.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	var n int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM email_tokens WHERE user_id = $1`, u.ID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("%d orphaned tokens remain", n)
	}
}

func usernames(us []*User) []string {
	out := make([]string, 0, len(us))
	for _, u := range us {
		out = append(out, u.Username)
	}
	return out
}
