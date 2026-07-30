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
	ExpiresAt   time.Time
}

// Expired reports whether the link is past its expiry and can no longer be
// redeemed. The store won't hand it out either way; this is for presentation, so
// the owner can see a spent link before deleting it.
func (i Invite) Expired() bool { return !i.ExpiresAt.IsZero() && time.Now().After(i.ExpiresAt) }

// InviteTTL is how long a new share link stays redeemable. Share links are
// credentials that travel through chat and email, so they get a hard shelf life
// rather than living until someone remembers to revoke them.
//
// It's applied when a link is minted and stored on the row (see migration 0040), so
// changing this constant affects only future links — outstanding ones keep the
// expiry they were created with. That's deliberate: a computed expiry would mean
// lengthening this value silently resurrects links that already died.
const InviteTTL = 7 * 24 * time.Hour

// CreateInvite mints a new single-use share link for a repeater, returning its
// token. description is an owner-facing label (may be empty). commandIDs is the
// initial command set the accepter is granted on redemption (may be empty — the
// owner can grant more afterwards); it and the invite row are written together so
// the link never exists without its recorded grant. The link expires after
// InviteTTL.
func (s *Store) CreateInvite(ctx context.Context, repeaterID int64, description string, commandIDs []int64) (string, error) {
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var inviteID int64
		if err := tx.QueryRow(ctx,
			`INSERT INTO repeater_invites (repeater_id, token, description, expires_at)
			 VALUES ($1, $2, $3, now() + $4) RETURNING id`,
			repeaterID, token, description, InviteTTL).Scan(&inviteID); err != nil {
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
// Expired links are included — the owner should see that a link is spent, and be
// able to delete it — so callers must present Invite.Expired() rather than implying
// every row is live.
func (s *Store) ListInvites(ctx context.Context, repeaterID int64) ([]Invite, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, token, description, created_at, expires_at
		FROM repeater_invites
		WHERE repeater_id = $1
		ORDER BY created_at DESC`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (Invite, error) {
		var inv Invite
		err := r.Scan(&inv.ID, &inv.Token, &inv.Description, &inv.CreatedAt, &inv.ExpiresAt)
		return inv, err
	})
}

// ExpiredInviteGrace is how long a lapsed share link stays in the table before the
// janitor removes it. It exists so the owner can actually SEE that a link went stale
// — the share page lists expired links with an Expired badge and a Remove button,
// and sweeping them immediately would make that state unobservable (the janitor runs
// every few minutes) and the button unreachable. Deleting them silently is the worse
// UX for something the owner handed to a person: the link just vanishes with no
// explanation of why it stopped working.
//
// Holding the row is not holding a credential: every lookup filters on expires_at,
// so a lapsed link can't be redeemed, and the share page stops rendering its token
// once it expires. Growth is bounded by a month of link creation.
const ExpiredInviteGrace = 30 * 24 * time.Hour

// PruneInvites deletes share links that lapsed more than ExpiredInviteGrace ago,
// returning how many went. Redemption deletes a link and an owner can delete one by
// hand, but an unredeemed link that simply timed out would otherwise sit in the table
// forever. Index-assisted by repeater_invites_expires_at_idx.
func (s *Store) PruneInvites(ctx context.Context) (int64, error) {
	// $1 needs the explicit ::interval cast. Without it `now() - $1` is ambiguous —
	// Postgres can read it as timestamptz - timestamptz → interval, which then makes
	// the comparison `timestamptz < interval` and fails at plan time. (The `now() + $n`
	// expressions elsewhere are fine because addition has only one candidate.)
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM repeater_invites WHERE expires_at < now() - $1::interval`, ExpiredInviteGrace)
	if err != nil {
		return 0, fmt.Errorf("prune invites: %w", err)
	}
	return tag.RowsAffected(), nil
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
// ErrNotFound if the token is unknown, revoked, expired, or already redeemed
// (redemption deletes the link). queryUserID only populates the Shared flag and
// owner display.
func (s *Store) RepeaterByInviteToken(ctx context.Context, queryUserID int64, token string) (*Repeater, error) {
	row := s.pool.QueryRow(ctx, repeaterSelect+`
		WHERE r.id = (SELECT repeater_id FROM repeater_invites
		              WHERE token = $2 AND expires_at > now())`,
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
// and ErrNotFound if the token is unknown, revoked, expired, or already redeemed.
// The row lock is the single-use gate: concurrent accepts serialize on it, and the
// winner deletes the row, so losers find nothing. The expiry is checked inside that
// same locked read, so a link can't be redeemed by a request that started before it
// lapsed.
func (s *Store) AcceptInvite(ctx context.Context, token string, userID int64) (added bool, err error) {
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		var inviteID, repeaterID int64
		if err := tx.QueryRow(ctx,
			`SELECT id, repeater_id FROM repeater_invites
			 WHERE token = $1 AND expires_at > now() FOR UPDATE`,
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

// publicIDLen is how many base62 characters a public id carries. 62^12 is just over
// 2^71, so ids are far too sparse to enumerate by guessing.
const publicIDLen = 12

// maxUnbiasedByte is the smallest byte value that must be REJECTED to keep `% 62`
// uniform. 256 isn't a multiple of 62 (4*62 = 248, with 8 left over), so folding a
// full byte range onto the alphabet hands those 8 remainders to the first 8
// characters — making '0'–'7' appear 5/256 of the time against 4/256 for every other
// character, i.e. 25% more often. Discarding 248–255 leaves exactly four bytes per
// character and removes the skew.
const maxUnbiasedByte = byte((256 / len(base62)) * len(base62)) // 248

// randomPublicID returns a short, opaque, URL-safe identifier (publicIDLen base62
// chars) used in place of sequential integer ids in URLs.
//
// Bytes at or above maxUnbiasedByte are drawn again rather than folded, so every
// character is equally likely. Only 8 of 256 values are rejected (~3%), so the refill
// loop almost never runs a second time.
func randomPublicID() (string, error) {
	out := make([]byte, 0, publicIDLen)
	buf := make([]byte, publicIDLen)
	for len(out) < publicIDLen {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("random public id: %w", err)
		}
		for _, c := range buf {
			if c >= maxUnbiasedByte {
				continue // would skew the distribution; draw another byte
			}
			out = append(out, base62[int(c)%len(base62)])
			if len(out) == publicIDLen {
				break
			}
		}
	}
	return string(out), nil
}
