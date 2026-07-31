package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// TokenPurpose distinguishes the two emailed-token flows. It's filtered on at
// redemption, so a verification link can never be replayed as a password reset.
type TokenPurpose string

const (
	// PurposeVerifyEmail proves an address belongs to the account that set it.
	PurposeVerifyEmail TokenPurpose = "verify"
	// PurposeResetPassword authorizes setting a new password on one account.
	PurposeResetPassword TokenPurpose = "reset"
)

// VerifyTokenTTL is how long an address-confirmation link stays usable. Generous:
// people check mail hours later, and the worst case of a stale link is a resend.
const VerifyTokenTTL = 24 * time.Hour

// ResetTokenTTL is how long a password-reset link stays usable. Deliberately much
// shorter than VerifyTokenTTL — this one is a live credential for taking over an
// account, and the legitimate user acts on it within minutes of asking.
const ResetTokenTTL = 45 * time.Minute

// hashToken is the one-way transform applied before a token touches the database.
// Only the hash is stored, so a leaked email_tokens table yields nothing
// replayable. Hex of SHA-256: tokens are 256 bits of randomness from randomToken,
// so there is no dictionary to attack and no salt/stretching needed.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// NormalizeEmail trims an address and lowercases it for comparison.
//
// The local part is technically case-sensitive per RFC 5321, but no mail provider
// in practice treats it that way, and someone typing "Me@Example.com" at the reset
// form means the address they registered. Deliberately NOT doing Gmail-style dot or
// +tag stripping: that's provider-specific, wrong elsewhere, and surprising.
func NormalizeEmail(addr string) string {
	return strings.ToLower(strings.TrimSpace(addr))
}

// SetEmail stores the account's address and marks it unverified, since the new
// address hasn't been proven yet. Passing an empty address clears both columns.
// Format validation is the caller's responsibility (see auth's mail.ParseAddress
// check).
func (s *Store) SetEmail(ctx context.Context, userID int64, addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return s.ClearEmail(ctx, userID)
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET email = $2, email_verified_at = NULL WHERE id = $1`,
		userID, addr)
	if err != nil {
		return fmt.Errorf("set email: %w", err)
	}
	return nil
}

// ClearEmail removes the account's address. Outstanding tokens for it go too:
// leaving a live reset link pointing at an address the user just disowned would
// keep a mailbox they may no longer control able to take the account over.
func (s *Store) ClearEmail(ctx context.Context, userID int64) error {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET email = NULL, email_verified_at = NULL WHERE id = $1`,
			userID); err != nil {
			return fmt.Errorf("clear email: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM email_tokens WHERE user_id = $1`, userID); err != nil {
			return fmt.Errorf("clear email tokens: %w", err)
		}
		return nil
	})
	return err
}

// MarkEmailVerified confirms the account's address, but only if it still matches
// addr — the address the redeemed token was issued for. Someone who requests a link
// for one address and then changes it to another must not have the old link confirm
// the new one. Reports whether the row was updated.
func (s *Store) MarkEmailVerified(ctx context.Context, userID int64, addr string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET email_verified_at = now()
		 WHERE id = $1 AND email IS NOT NULL AND lower(email) = lower($2)`,
		userID, addr)
	if err != nil {
		return false, fmt.Errorf("mark email verified: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// CreateEmailToken mints a single-use token for userID and returns it in the clear
// — the only time it exists outside the recipient's mailbox, since the database
// keeps just its hash. addr records which address a verify token was issued for and
// should be empty for a reset token.
func (s *Store) CreateEmailToken(ctx context.Context, userID int64, purpose TokenPurpose, addr string, ttl time.Duration) (string, error) {
	raw, err := randomToken()
	if err != nil {
		return "", err
	}
	var addrArg *string
	if addr != "" {
		addrArg = &addr
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO email_tokens (user_id, purpose, token_hash, email, expires_at)
		 VALUES ($1, $2, $3, $4, now() + $5)`,
		userID, string(purpose), hashToken(raw), addrArg, ttl)
	if err != nil {
		return "", fmt.Errorf("create email token: %w", err)
	}
	return raw, nil
}

// EmailToken is a redeemed or inspected token.
type EmailToken struct {
	UserID int64
	// Email is the address a verify token was issued for, "" for a reset token.
	Email     string
	ExpiresAt time.Time
}

// PeekEmailToken looks a token up without consuming it, so a GET can render a form
// (and name the account it belongs to) while leaving the token spendable for the
// POST that follows. ok is false when the token is unknown, expired, or of another
// purpose — all of which the caller should treat as "invalid link", not an error.
func (s *Store) PeekEmailToken(ctx context.Context, purpose TokenPurpose, raw string) (EmailToken, bool, error) {
	var t EmailToken
	var addr *string
	err := s.pool.QueryRow(ctx,
		`SELECT user_id, email, expires_at FROM email_tokens
		 WHERE token_hash = $1 AND purpose = $2 AND expires_at > now()`,
		hashToken(raw), string(purpose)).Scan(&t.UserID, &addr, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmailToken{}, false, nil
	}
	if err != nil {
		return EmailToken{}, false, fmt.Errorf("peek email token: %w", err)
	}
	if addr != nil {
		t.Email = *addr
	}
	return t, true, nil
}

// ConsumeEmailToken atomically redeems a token, deleting it so it can be spent
// exactly once (the DELETE … RETURNING pattern the login handoff uses). ok is false
// when the token is unknown, expired, already spent, or of another purpose — an
// auth failure for the caller to report generically, not an error.
func (s *Store) ConsumeEmailToken(ctx context.Context, purpose TokenPurpose, raw string) (EmailToken, bool, error) {
	var t EmailToken
	var addr *string
	err := s.pool.QueryRow(ctx,
		`DELETE FROM email_tokens
		 WHERE token_hash = $1 AND purpose = $2 AND expires_at > now()
		 RETURNING user_id, email, expires_at`,
		hashToken(raw), string(purpose)).Scan(&t.UserID, &addr, &t.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return EmailToken{}, false, nil
	}
	if err != nil {
		return EmailToken{}, false, fmt.Errorf("consume email token: %w", err)
	}
	if addr != nil {
		t.Email = *addr
	}
	return t, true, nil
}

// DeleteEmailTokens drops every outstanding token of one purpose for a user. Called
// after a successful reset so the other links from the same request (or from an
// earlier one) die with the password they were minted to change.
func (s *Store) DeleteEmailTokens(ctx context.Context, userID int64, purpose TokenPurpose) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM email_tokens WHERE user_id = $1 AND purpose = $2`, userID, string(purpose))
	if err != nil {
		return fmt.Errorf("delete email tokens: %w", err)
	}
	return nil
}

// CountRecentEmailTokens reports how many tokens of one purpose were minted for a
// user within window. It backs the per-account cap: without it, anyone who knows an
// address could have us mail its owner repeatedly, and a metered daily send quota
// would be trivial to exhaust.
func (s *Store) CountRecentEmailTokens(ctx context.Context, userID int64, purpose TokenPurpose, window time.Duration) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM email_tokens
		 WHERE user_id = $1 AND purpose = $2 AND created_at > now() - $3::interval`,
		userID, string(purpose), window).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count recent email tokens: %w", err)
	}
	return n, nil
}

// FindUsersByVerifiedEmail returns every account holding addr as a VERIFIED
// address, matched case-insensitively.
//
// It returns a slice rather than one user because the address column is
// deliberately not unique (see migration 0043): one person may run a personal and
// an ops account on the same mailbox. The caller mails one message listing each
// match with its own link — a token names exactly one account, so which account to
// reset is never ambiguous. An unknown address yields an empty slice, not
// ErrNotFound: "no accounts here" is an ordinary outcome the caller must not
// distinguish to the visitor.
func (s *Store) FindUsersByVerifiedEmail(ctx context.Context, addr string) ([]*User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+userCols+` FROM users
		 WHERE email IS NOT NULL AND email_verified_at IS NOT NULL AND lower(email) = lower($1)
		 ORDER BY username`,
		NormalizeEmail(addr))
	if err != nil {
		return nil, fmt.Errorf("find users by email: %w", err)
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user by email: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find users by email: %w", err)
	}
	return out, nil
}

// PruneEmailTokens deletes tokens whose expiry has passed, returning how many went.
// Redemption only removes tokens that are actually spent, so without this the
// abandoned ones accumulate forever — each holding a user_id and, for verify
// tokens, an address. Index-assisted by email_tokens_expiry_idx.
func (s *Store) PruneEmailTokens(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM email_tokens WHERE expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("prune email tokens: %w", err)
	}
	return tag.RowsAffected(), nil
}
