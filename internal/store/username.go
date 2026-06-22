package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Usernames are user-changeable, but with two guards that make a freed handle
// safe to leave behind:
//
//   - UsernameReleaseCooldown: once a username is given up, nobody else may
//     claim it for this window — only its previous owner can take it back. This
//     blunts the impersonation/squatting attack where a renamed-away handle is
//     grabbed to inherit its history.
//   - UsernameRenameInterval: a user may only rename themselves this often.
//     Admin-initiated changes bypass it.
const (
	UsernameReleaseCooldown = 90 * 24 * time.Hour
	UsernameRenameInterval  = 30 * 24 * time.Hour
)

// ErrUsernameReserved is returned when a username was recently released by
// someone else and is still within its cooldown. It is deliberately distinct
// from ErrDuplicate internally, but callers should surface both as a generic
// "unavailable" so they don't reveal that a name was previously in use.
var ErrUsernameReserved = errors.New("store: username reserved")

// ErrRenameTooSoon is returned when a self-service rename is attempted before
// the per-user rename interval has elapsed.
var ErrRenameTooSoon = errors.New("store: rename too soon")

// UsernameChangeContext is the audit metadata recorded alongside a rename.
type UsernameChangeContext struct {
	ChangedBy int64  // the acting user: the account itself, or an admin
	IP        string // client IP, "" if unknown
	UserAgent string // client UA, "" if unknown
}

// nameReservedByOther reports whether candidate was released within the cooldown
// by some user other than exceptUserID (pass 0 to match every prior owner, e.g.
// at signup where there is no incumbent). The previous owner is always allowed
// to reclaim their own freed name. q is the pool or an open transaction.
func nameReservedByOther(ctx context.Context, q rowQuerier, candidate string, exceptUserID int64) (bool, error) {
	cutoff := time.Now().Add(-UsernameReleaseCooldown)
	var reserved bool
	err := q.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM username_changes
			WHERE lower(old_username) = $1
			  AND changed_at > $2
			  AND user_id IS DISTINCT FROM $3
		)`, candidate, cutoff, exceptUserID).Scan(&reserved)
	if err != nil {
		return false, fmt.Errorf("check reserved: %w", err)
	}
	return reserved, nil
}

// SetUsername renames userID to newUsername, recording the change in the audit
// trail. All checks and the update run in one transaction; the UNIQUE
// constraint is the final race guard.
//
// enforceInterval gates the per-user rename rate limit: pass true for
// self-service changes, false for admin-initiated ones (which still respect
// uniqueness and the release cooldown on names others hold). Returns nil with no
// change when newUsername already matches the current one.
func (s *Store) SetUsername(ctx context.Context, userID int64, newUsername string, meta UsernameChangeContext, enforceInterval bool) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var old string
		if err := tx.QueryRow(ctx, `SELECT username FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&old); err != nil {
			return notFoundOr(err, "get user")
		}
		if old == newUsername {
			return nil // no-op
		}

		if enforceInterval {
			var last *time.Time
			if err := tx.QueryRow(ctx,
				`SELECT max(changed_at) FROM username_changes WHERE user_id = $1 AND changed_by = $1`,
				userID).Scan(&last); err != nil {
				return fmt.Errorf("rate check: %w", err)
			}
			if last != nil && last.After(time.Now().Add(-UsernameRenameInterval)) {
				return ErrRenameTooSoon
			}
		}

		reserved, err := nameReservedByOther(ctx, tx, newUsername, userID)
		if err != nil {
			return err
		}
		if reserved {
			return ErrUsernameReserved
		}

		if _, err := tx.Exec(ctx, `UPDATE users SET username = $1 WHERE id = $2`, newUsername, userID); err != nil {
			if isUniqueViolation(err) {
				return ErrDuplicate
			}
			return fmt.Errorf("update username: %w", err)
		}

		if _, err := tx.Exec(ctx, `
			INSERT INTO username_changes (user_id, old_username, new_username, changed_by, ip, user_agent)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			userID, old, newUsername, meta.ChangedBy, nullStr(meta.IP), nullStr(meta.UserAgent)); err != nil {
			return fmt.Errorf("record username change: %w", err)
		}
		return nil
	})
}

// NextRenameAllowed returns the earliest time userID may change their own
// username again, or nil if they may change it now (no prior self-service change
// within the interval).
func (s *Store) NextRenameAllowed(ctx context.Context, userID int64) (*time.Time, error) {
	var last *time.Time
	if err := s.pool.QueryRow(ctx,
		`SELECT max(changed_at) FROM username_changes WHERE user_id = $1 AND changed_by = $1`,
		userID).Scan(&last); err != nil {
		return nil, fmt.Errorf("next rename allowed: %w", err)
	}
	if last == nil {
		return nil, nil
	}
	next := last.Add(UsernameRenameInterval)
	if next.After(time.Now()) {
		return &next, nil
	}
	return nil, nil
}

// UsernameChange is one row of a user's rename history, for the admin view.
type UsernameChange struct {
	OldUsername string
	NewUsername string
	ChangedAt   time.Time
	BySelf      bool    // changed_by == user_id
	ByActor     *string // acting username when admin-initiated and still known
	IP          *string
	UserAgent   *string
}

// ListUsernameChanges returns a user's rename history, newest first. Admin-only:
// old usernames are never exposed on public or member-facing surfaces.
func (s *Store) ListUsernameChanges(ctx context.Context, userID int64, limit int) ([]UsernameChange, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.old_username, c.new_username, c.changed_at,
		       (c.changed_by IS NOT DISTINCT FROM c.user_id) AS by_self,
		       a.username, c.ip, c.user_agent
		FROM username_changes c
		LEFT JOIN users a ON a.id = c.changed_by
		WHERE c.user_id = $1
		ORDER BY c.changed_at DESC, c.id DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list username changes: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (UsernameChange, error) {
		var c UsernameChange
		err := r.Scan(&c.OldUsername, &c.NewUsername, &c.ChangedAt, &c.BySelf, &c.ByActor, &c.IP, &c.UserAgent)
		return c, err
	})
}

// nullStr returns nil for an empty string so it stores as SQL NULL.
func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
