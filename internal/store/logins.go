package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// A login row is the source of truth for one real sign-in. Cookies are host-only
// (the auth, app, root, and custom-domain hosts each get their own), so a single
// sign-in spawns several per-host sessions; each stores this login id and is
// validated against the row on every request. Revoking the row therefore logs
// the user out of every host at once. See docs/auth-cross-host.md.

// CreateLogin records a new sign-in for userID and returns its id.
func (s *Store) CreateLogin(ctx context.Context, userID int64) (string, error) {
	id, err := randomToken()
	if err != nil {
		return "", err
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO logins (id, user_id) VALUES ($1, $2)`, id, userID); err != nil {
		return "", fmt.Errorf("create login: %w", err)
	}
	return id, nil
}

// LoginValid reports whether a login id is still active (exists and not revoked),
// returning the user it belongs to. A missing or revoked row yields ok=false
// (and no error) — the caller should treat that as logged out.
func (s *Store) LoginValid(ctx context.Context, id string) (userID int64, ok bool, err error) {
	err = s.pool.QueryRow(ctx,
		`SELECT user_id FROM logins WHERE id = $1 AND revoked_at IS NULL`, id).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("login valid: %w", err)
	}
	return userID, true, nil
}

// RevokeLogin marks a login revoked (idempotent). Every host session keyed to it
// falls to anonymous on its next request.
func (s *Store) RevokeLogin(ctx context.Context, id string) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE logins SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id); err != nil {
		return fmt.Errorf("revoke login: %w", err)
	}
	return nil
}

// RevokeAllUserLogins revokes every active login for a user ("log out everywhere").
func (s *Store) RevokeAllUserLogins(ctx context.Context, userID int64) error {
	if _, err := s.pool.Exec(ctx,
		`UPDATE logins SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return fmt.Errorf("revoke user logins: %w", err)
	}
	return nil
}
