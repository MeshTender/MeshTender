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

// Invite is a pending single-use share link. Redeemed links are deleted (see
// AcceptInvite), so every Invite is unused.
type Invite struct {
	ID          int64
	Token       string
	Description string
	CreatedAt   time.Time
}

// CreateInvite mints a new single-use share link for a repeater, returning its
// token. description is an owner-facing label (may be empty). commandIDs is the
// initial command set the accepter is granted on redemption (may be empty — the
// owner can grant more afterwards); it and the invite row are written together so
// the link never exists without its recorded grant.
func (s *Store) CreateInvite(ctx context.Context, repeaterID int64, description string, commandIDs []int64) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var inviteID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO repeater_invites (repeater_id, token, description) VALUES ($1, $2, $3) RETURNING id`,
			repeaterID, token, description).Scan(&inviteID); err != nil {
			return fmt.Errorf("create invite: %w", err)
		}
		for _, cid := range commandIDs {
			if _, err := tx.Exec(ctx,
				`INSERT INTO invite_commands (invite_id, command_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				inviteID, cid); err != nil {
				return fmt.Errorf("seed invite commands: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// ListInvites returns a repeater's pending (unredeemed) share links, newest first.
func (s *Store) ListInvites(ctx context.Context, repeaterID int64) ([]Invite, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, token, description, created_at
		FROM repeater_invites
		WHERE repeater_id = $1
		ORDER BY created_at DESC`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (Invite, error) {
		var inv Invite
		err := r.Scan(&inv.ID, &inv.Token, &inv.Description, &inv.CreatedAt)
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

// RepeaterByInviteToken resolves a valid share-link token to its repeater, or
// ErrNotFound if the token is unknown, revoked, or already redeemed (redemption
// deletes the link). queryUserID only populates the Shared flag and owner display.
func (s *Store) RepeaterByInviteToken(ctx context.Context, queryUserID int64, token string) (*Repeater, error) {
	row := s.pool.QueryRow(ctx, repeaterSelect+`
		WHERE r.id = (SELECT repeater_id FROM repeater_invites WHERE token = $2)`,
		queryUserID, token)
	r, err := scanRepeater(row)
	if err != nil {
		return nil, notFoundOr(err, "repeater by invite")
	}
	return r, nil
}

// AcceptInvite redeems a single-use share link for userID in one transaction so
// the steps are all-or-nothing: it grants the share, seeds the command set the
// owner chose for this link (invite_commands), and deletes the link — a redeemed
// link doesn't stick around. If any step fails the whole thing rolls back, so a
// failure can never delete the link without granting access (or grant access
// without deleting the link, leaving it redeemable again).
//
// It returns whether a new share was created (false if the user already had one)
// and ErrNotFound if the token is unknown, revoked, or already redeemed. The row
// lock is the single-use gate: concurrent accepts serialize on it, and the winner
// deletes the row, so losers find nothing.
func (s *Store) AcceptInvite(ctx context.Context, token string, userID int64) (added bool, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var inviteID, repeaterID int64
		if err := tx.QueryRow(ctx,
			`SELECT id, repeater_id FROM repeater_invites WHERE token = $1 FOR UPDATE`,
			token).Scan(&inviteID, &repeaterID); err != nil {
			return notFoundOr(err, "consume invite")
		}
		tag, err := tx.Exec(ctx,
			`INSERT INTO repeater_shares (repeater_id, user_id) VALUES ($1, $2)
			 ON CONFLICT (repeater_id, user_id) DO NOTHING`, repeaterID, userID)
		if err != nil {
			return fmt.Errorf("add share: %w", err)
		}
		added = tag.RowsAffected() > 0
		if added {
			// Seed the new share with the command set the owner chose for this link,
			// before the delete below cascades invite_commands away.
			if _, err := tx.Exec(ctx, `
				INSERT INTO share_commands (repeater_id, user_id, command_id)
				SELECT $1, $2, command_id FROM invite_commands WHERE invite_id = $3
				ON CONFLICT DO NOTHING`, repeaterID, userID, inviteID); err != nil {
				return fmt.Errorf("seed share commands: %w", err)
			}
		}
		// Consume the single-use link by deleting it (cascades its invite_commands).
		if _, err := tx.Exec(ctx, `DELETE FROM repeater_invites WHERE id = $1`, inviteID); err != nil {
			return fmt.Errorf("consume invite: %w", err)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return added, nil
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
