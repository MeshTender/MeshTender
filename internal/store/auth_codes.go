package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// authCodeTTL bounds how long a handoff code is valid between the auth host
// minting it and the app host consuming it. Kept short: the browser redirects
// immediately, so this only needs to cover one network round-trip.
const authCodeTTL = 60 * time.Second

// CreateAuthCode mints a single-use handoff code for userID, to be redeemed at
// the app host's callback. loginID threads the originating login row so the
// redeeming host reuses it rather than minting a second one. next is the
// validated app-local path to land on.
func (s *Store) CreateAuthCode(ctx context.Context, userID int64, loginID, next string) (string, error) {
	code, err := randomToken()
	if err != nil {
		return "", err
	}
	if next == "" {
		next = "/"
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO auth_codes (code, user_id, login_id, next, expiry) VALUES ($1, $2, $3, $4, now() + $5)`,
		code, userID, loginID, next, authCodeTTL)
	if err != nil {
		return "", fmt.Errorf("create auth code: %w", err)
	}
	return code, nil
}

// ConsumeAuthCode atomically redeems a handoff code, returning its user id, the
// originating login id, and the post-auth path. The code is deleted on success
// (single-use) and only matches while unexpired. ok is false when the code is
// unknown, expired, or already used — the caller should treat that as an auth
// failure, not an error.
func (s *Store) ConsumeAuthCode(ctx context.Context, code string) (userID int64, loginID, next string, ok bool, err error) {
	err = s.pool.QueryRow(ctx,
		`DELETE FROM auth_codes WHERE code = $1 AND expiry > now() RETURNING user_id, login_id, next`,
		code).Scan(&userID, &loginID, &next)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, "", "", false, nil
	}
	if err != nil {
		return 0, "", "", false, fmt.Errorf("consume auth code: %w", err)
	}
	return userID, loginID, next, true, nil
}
