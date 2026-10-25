package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// A companion is a member's own MeshCore radio, registered so its adverts count
// as check-ins. Like a repeater it belongs to its owner and, through them, to
// every org they're a member of.

// MaxCompanionsPerUser bounds a user's registrations, which keeps every
// per-user and per-org companion listing bounded too.
const MaxCompanionsPerUser = 10

// UnconfirmedCompanionTTL is how long a registration waits for its first advert
// before it's dropped, freeing the key for whoever actually holds it.
const UnconfirmedCompanionTTL = 24 * time.Hour

// ErrCompanionLimit is returned when the owner already has MaxCompanionsPerUser.
var ErrCompanionLimit = errors.New("companion limit reached")

// Companion is a registered companion radio.
type Companion struct {
	ID           int64
	PublicID     string
	OwnerID      int64
	OwnerName    string
	Name         string
	PublicKeyHex string
	CreatedAt    time.Time
	ConfirmedAt  *time.Time
	LastHeardAt  *time.Time
	// LastAdvertAt is the sender's own clock on the newest advert heard.
	LastAdvertAt *time.Time
	// LastRejectedAt/LastRejectedSkew describe the most recent advert that was
	// heard but didn't count because the companion's clock was too far off.
	LastRejectedAt   *time.Time
	LastRejectedSkew *time.Duration
}

// Confirmed reports whether an advert from this companion has been heard since
// it was registered.
func (c *Companion) Confirmed() bool { return c.ConfirmedAt != nil }

const companionSelect = `
	SELECT c.id, c.public_id, c.owner_id, COALESCE(NULLIF(u.display_name, ''), u.username),
	       c.name, c.public_key_hex, c.created_at, c.confirmed_at, c.last_heard_at,
	       c.last_advert_ts, c.last_rejected_at, c.last_rejected_skew_s
	FROM companions c JOIN users u ON u.id = c.owner_id`

func scanCompanion(row pgx.Row) (*Companion, error) {
	var c Companion
	var advertTS *int64
	var skewS *int32
	err := row.Scan(&c.ID, &c.PublicID, &c.OwnerID, &c.OwnerName, &c.Name, &c.PublicKeyHex,
		&c.CreatedAt, &c.ConfirmedAt, &c.LastHeardAt, &advertTS, &c.LastRejectedAt, &skewS)
	if err != nil {
		return nil, err
	}
	c.setStored(advertTS, skewS)
	return &c, nil
}

// setStored converts the stored advert timestamp (unix secs) and rejection skew
// (secs) into their typed fields.
func (c *Companion) setStored(advertTS *int64, skewS *int32) {
	if advertTS != nil {
		t := time.Unix(*advertTS, 0)
		c.LastAdvertAt = &t
	}
	if skewS != nil {
		d := time.Duration(*skewS) * time.Second
		c.LastRejectedSkew = &d
	}
}

// CreateCompanion registers a companion for ownerID. publicKeyHex must already
// be validated and lowercased. Returns ErrDuplicate if anyone has registered the
// key, or ErrCompanionLimit if the owner is at the cap.
func (s *Store) CreateCompanion(ctx context.Context, ownerID int64, name, publicKeyHex string) (*Companion, error) {
	publicID, err := randomPublicID()
	if err != nil {
		return nil, err
	}
	var id int64
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// Serialize this owner's registrations so two concurrent adds can't both
		// pass the count check.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM users WHERE id = $1 FOR UPDATE`, ownerID); err != nil {
			return fmt.Errorf("lock owner: %w", err)
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM companions WHERE owner_id = $1`, ownerID).Scan(&n); err != nil {
			return fmt.Errorf("count companions: %w", err)
		}
		if n >= MaxCompanionsPerUser {
			return ErrCompanionLimit
		}
		err := tx.QueryRow(ctx, `
			INSERT INTO companions (public_id, owner_id, name, public_key_hex) VALUES ($1, $2, $3, $4)
			RETURNING id`, publicID, ownerID, name, publicKeyHex).Scan(&id)
		if isUniqueViolation(err) {
			return ErrDuplicate
		}
		return err
	})
	if err != nil {
		if errors.Is(err, ErrDuplicate) || errors.Is(err, ErrCompanionLimit) {
			return nil, err
		}
		return nil, fmt.Errorf("create companion: %w", err)
	}
	c, err := scanCompanion(s.pool.QueryRow(ctx, companionSelect+` WHERE c.id = $1`, id))
	return c, notFoundOr(err, "get created companion")
}

// ListCompanionsForUser returns the user's own companions by name (at most
// MaxCompanionsPerUser).
func (s *Store) ListCompanionsForUser(ctx context.Context, ownerID int64) ([]*Companion, error) {
	rows, err := s.pool.Query(ctx, companionSelect+`
		WHERE c.owner_id = $1 ORDER BY lower(c.name), c.id`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("list companions: %w", err)
	}
	return collectRows(rows, scanCompanion)
}

// GetCompanionForViewer returns the companion if viewerID may see it: they own
// it, or they share an org with its owner. Anything else is ErrNotFound, so a
// guessed public id reveals nothing.
func (s *Store) GetCompanionForViewer(ctx context.Context, publicID string, viewerID int64) (*Companion, error) {
	c, err := scanCompanion(s.pool.QueryRow(ctx, companionSelect+`
		WHERE c.public_id = $1
		  AND (c.owner_id = $2 OR EXISTS (
		        SELECT 1 FROM org_members mine
		        JOIN org_members theirs ON theirs.org_id = mine.org_id AND theirs.user_id = c.owner_id
		        WHERE mine.user_id = $2))`, publicID, viewerID))
	return c, notFoundOr(err, "get companion")
}

// DeleteCompanionOwned deletes the owner's companion and its sightings.
func (s *Store) DeleteCompanionOwned(ctx context.Context, ownerID int64, publicID string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM companions WHERE owner_id = $1 AND public_id = $2`, ownerID, publicID)
	if err != nil {
		return fmt.Errorf("delete companion: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// CompanionIDForKey returns the id of the companion registered under the key,
// or ErrNotFound. This is the ingester's cheap filter, run before any signature
// check; it rides the companions.public_key_hex unique index.
func (s *Store) CompanionIDForKey(ctx context.Context, publicKeyHex string) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `SELECT id FROM companions WHERE public_key_hex = $1`, publicKeyHex).Scan(&id)
	return id, notFoundOr(err, "companion for key")
}

// PruneUnconfirmedCompanions deletes registrations that never heard an advert
// within UnconfirmedCompanionTTL. Signature fits the janitor.
func (s *Store) PruneUnconfirmedCompanions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM companions WHERE confirmed_at IS NULL AND created_at < now() - make_interval(secs => $1)`,
		UnconfirmedCompanionTTL.Seconds())
	if err != nil {
		return 0, fmt.Errorf("prune unconfirmed companions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// OrgCompanionActivityPageSize is the activity page's keyset page size.
const OrgCompanionActivityPageSize = 50

// ActivityCursor is the keyset position on the activity page: the last row's
// (name, id), ordered by lower(name).
type ActivityCursor struct {
	Name string
	ID   int64
}

// ListOrgCompanionActivityPage returns one keyset page of the confirmed
// companions owned by the org's members, by name: who has checked in and when
// they were last heard. Deliberately nothing about where — which repeaters
// carried a member's adverts is the owner's to see (on their companion page),
// not the whole org's.
func (s *Store) ListOrgCompanionActivityPage(ctx context.Context, orgID int64, after *ActivityCursor) ([]*Companion, bool, error) {
	first := after == nil
	var afterName string
	var afterID int64
	if !first {
		afterName, afterID = after.Name, after.ID
	}
	rows, err := s.pool.Query(ctx, companionSelect+`
		JOIN org_members om ON om.org_id = $1 AND om.user_id = c.owner_id
		WHERE c.confirmed_at IS NOT NULL
		  AND ($2 OR (lower(c.name), c.id) > (lower($3), $4))
		ORDER BY lower(c.name), c.id
		LIMIT $5`, orgID, first, afterName, afterID, OrgCompanionActivityPageSize+1)
	if err != nil {
		return nil, false, fmt.Errorf("list org companion activity: %w", err)
	}
	out, err := collectRows(rows, scanCompanion)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(out) > OrgCompanionActivityPageSize
	if hasMore {
		out = out[:OrgCompanionActivityPageSize]
	}
	return out, hasMore, nil
}
