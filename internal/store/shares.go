package store

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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
	if err != nil {
		return nil, notFoundOr(err, "get owned repeater")
	}
	return r, nil
}

// ShareInfo describes a user a repeater is shared with.
type ShareInfo struct {
	UserID      int64
	Username    string
	DisplayName *string
	// Steward marks this person a designated co-maintainer, listed on the
	// repeater's public page.
	Steward bool
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

// ShareCounts summarizes how widely a repeater is shared.
type ShareCounts struct {
	Users int // direct user shares
	Orgs  int // organizations this repeater participates in
}

// RepeaterSharingCounts returns per-repeater share and org-participation counts
// for every repeater owned by ownerID, keyed by repeater id.
func (s *Store) RepeaterSharingCounts(ctx context.Context, ownerID int64) (map[int64]ShareCounts, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.id,
		       (SELECT count(*) FROM repeater_shares rs WHERE rs.repeater_id = r.id),
		       (SELECT count(*) FROM org_members om
		         WHERE om.user_id = r.owner_id
		           AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                           WHERE e.org_id = om.org_id AND e.repeater_id = r.id))
		FROM repeaters r WHERE r.owner_id = $1`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("repeater sharing counts: %w", err)
	}
	defer rows.Close()
	out := map[int64]ShareCounts{}
	for rows.Next() {
		var id int64
		var c ShareCounts
		if err := rows.Scan(&id, &c.Users, &c.Orgs); err != nil {
			return nil, fmt.Errorf("scan sharing counts: %w", err)
		}
		out[id] = c
	}
	return out, rows.Err()
}

// ListShares returns the users a repeater is shared with.
func (s *Store) ListShares(ctx context.Context, repeaterID int64) ([]ShareInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.username, u.display_name, rs.steward
		FROM repeater_shares rs JOIN users u ON u.id = rs.user_id
		WHERE rs.repeater_id = $1
		ORDER BY COALESCE(NULLIF(u.display_name, ''), u.username)`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (ShareInfo, error) {
		var si ShareInfo
		err := r.Scan(&si.UserID, &si.Username, &si.DisplayName, &si.Steward)
		return si, err
	})
}

// IsSteward reports whether a user is a steward of a repeater (a co-operator with
// full command access).
func (s *Store) IsSteward(ctx context.Context, repeaterID, userID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM repeater_shares WHERE repeater_id = $1 AND user_id = $2 AND steward)`,
		repeaterID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("is steward: %w", err)
	}
	return ok, nil
}

// SetShareSteward flags (or unflags) a shared user as a steward of the repeater.
// Scoped to the repeater, so it only affects an existing share.
func (s *Store) SetShareSteward(ctx context.Context, repeaterID, userID int64, steward bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE repeater_shares SET steward = $3 WHERE repeater_id = $1 AND user_id = $2`,
		repeaterID, userID, steward)
	if err != nil {
		return fmt.Errorf("set share steward: %w", err)
	}
	return nil
}

// ListStewards returns the steward-flagged shared users for a repeater (the
// designated backup maintainers shown on the public page), by display name.
func (s *Store) ListStewards(ctx context.Context, repeaterID int64) ([]ShareInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, u.username, u.display_name, rs.steward
		FROM repeater_shares rs JOIN users u ON u.id = rs.user_id
		WHERE rs.repeater_id = $1 AND rs.steward
		ORDER BY COALESCE(NULLIF(u.display_name, ''), u.username)`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list stewards: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (ShareInfo, error) {
		var si ShareInfo
		err := r.Scan(&si.UserID, &si.Username, &si.DisplayName, &si.Steward)
		return si, err
	})
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
	return collectRows(rows, func(r pgx.Row) (Invite, error) {
		var inv Invite
		err := r.Scan(&inv.ID, &inv.Token, &inv.Description, &inv.CreatedAt, &inv.UsedAt, &inv.UsedByName)
		return inv, err
	})
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
	if err != nil {
		return nil, notFoundOr(err, "repeater by invite")
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
	if err != nil {
		return 0, notFoundOr(err, "consume invite")
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

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// randomPublicID returns a short, opaque, URL-safe identifier (12 base62 chars,
// ~71 bits) used in place of sequential integer ids in URLs.
func randomPublicID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random public id: %w", err)
	}
	for i, c := range b {
		b[i] = base62[int(c)%len(base62)]
	}
	return string(b), nil
}
