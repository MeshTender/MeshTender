package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetRepeaterOwned returns the repeater only if owned by ownerID, else
// ErrNotFound. Used to gate owner-only operations like sharing.
func (s *Store) GetRepeaterOwned(ctx context.Context, ownerID, repeaterID int64) (*Repeater, error) {
	row := s.pool.QueryRow(ctx, repeaterSelect+`
		WHERE r.id = $2 AND r.owner_id = $1`, ownerID, repeaterID)
	r, err := scanRepeater(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get owned repeater: %w", err)
	}
	return r, nil
}

// ShareInfo describes a user a repeater is shared with.
type ShareInfo struct {
	UserID      int64
	Username    string
	DisplayName *string
}

// Name returns the display name if set, else the username.
func (si ShareInfo) Name() string {
	if si.DisplayName != nil && *si.DisplayName != "" {
		return *si.DisplayName
	}
	return si.Username
}

// AddShare grants a user access to a repeater. It is idempotent and reports
// whether a new share was created.
func (s *Store) AddShare(ctx context.Context, repeaterID, userID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO repeater_shares (repeater_id, user_id) VALUES ($1, $2)
		 ON CONFLICT (repeater_id, user_id) DO NOTHING`,
		repeaterID, userID)
	if err != nil {
		return false, fmt.Errorf("add share: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// IsShared reports whether a repeater is already shared with a user.
func (s *Store) IsShared(ctx context.Context, repeaterID, userID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM repeater_shares WHERE repeater_id = $1 AND user_id = $2)`,
		repeaterID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("is shared: %w", err)
	}
	return exists, nil
}

// RemoveShare revokes a user's access to a repeater.
func (s *Store) RemoveShare(ctx context.Context, repeaterID, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM repeater_shares WHERE repeater_id = $1 AND user_id = $2`, repeaterID, userID)
	if err != nil {
		return fmt.Errorf("remove share: %w", err)
	}
	return nil
}

// ListShares returns the users a repeater is shared with.
func (s *Store) ListShares(ctx context.Context, repeaterID int64) ([]ShareInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.username, u.display_name
		FROM repeater_shares rs JOIN users u ON u.id = rs.user_id
		WHERE rs.repeater_id = $1
		ORDER BY u.username`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	defer rows.Close()

	var out []ShareInfo
	for rows.Next() {
		var si ShareInfo
		if err := rows.Scan(&si.UserID, &si.Username, &si.DisplayName); err != nil {
			return nil, fmt.Errorf("scan share: %w", err)
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// --- share links (single-use invites) ---

// Invite is a single-use share link. Used invites retain who consumed them and
// when, as an audit trail.
type Invite struct {
	ID          int64
	Token       string
	Description string
	CreatedAt   time.Time
	UsedAt      *time.Time
	UsedByName  *string // display name or username of the consumer, if used
}

// CreateInvite mints a new single-use share link for a repeater, returning its
// token. description is an owner-facing label (may be empty).
func (s *Store) CreateInvite(ctx context.Context, repeaterID int64, description string) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO repeater_invites (repeater_id, token, description) VALUES ($1, $2, $3)`,
		repeaterID, token, description)
	if err != nil {
		return "", fmt.Errorf("create invite: %w", err)
	}
	return token, nil
}

// ListInvites returns all invites for a repeater (pending and used), newest first.
func (s *Store) ListInvites(ctx context.Context, repeaterID int64) ([]Invite, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id, i.token, i.description, i.created_at, i.used_at,
		       COALESCE(NULLIF(u.display_name, ''), u.username)
		FROM repeater_invites i
		LEFT JOIN users u ON u.id = i.used_by
		WHERE i.repeater_id = $1
		ORDER BY i.created_at DESC`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	defer rows.Close()

	var out []Invite
	for rows.Next() {
		var inv Invite
		if err := rows.Scan(&inv.ID, &inv.Token, &inv.Description, &inv.CreatedAt, &inv.UsedAt, &inv.UsedByName); err != nil {
			return nil, fmt.Errorf("scan invite: %w", err)
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// DeleteInvite removes an invite by id, scoped to its repeater (so an owner can
// only delete links for their own repeater). Idempotent.
func (s *Store) DeleteInvite(ctx context.Context, repeaterID, inviteID int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM repeater_invites WHERE id = $1 AND repeater_id = $2`, inviteID, repeaterID)
	if err != nil {
		return fmt.Errorf("delete invite: %w", err)
	}
	return nil
}

// RepeaterByInviteToken resolves a *valid* (unused) share-link token to its
// repeater, or ErrNotFound if the token is unknown, revoked, or already used.
// queryUserID is used only to populate the Shared flag and owner display.
func (s *Store) RepeaterByInviteToken(ctx context.Context, queryUserID int64, token string) (*Repeater, error) {
	row := s.pool.QueryRow(ctx, repeaterSelect+`
		WHERE r.id = (SELECT repeater_id FROM repeater_invites WHERE token = $2 AND used_at IS NULL)`,
		queryUserID, token)
	r, err := scanRepeater(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("repeater by invite: %w", err)
	}
	return r, nil
}

// ConsumeInvite atomically marks an unused invite as used by userID and returns
// its repeater id. Returns ErrNotFound if the token is unknown/revoked or was
// already consumed (the single-use guard, safe against concurrent accepts).
func (s *Store) ConsumeInvite(ctx context.Context, token string, userID int64) (int64, error) {
	var repeaterID int64
	err := s.pool.QueryRow(ctx,
		`UPDATE repeater_invites SET used_at = now(), used_by = $2
		 WHERE token = $1 AND used_at IS NULL
		 RETURNING repeater_id`, token, userID).Scan(&repeaterID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("consume invite: %w", err)
	}
	return repeaterID, nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
