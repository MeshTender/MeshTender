package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// The channels an org recommends its members join (org_channels, migration
// 0058). A name starting with "#" is a hashtag channel, keyed by its name; any
// other name is a private channel whose key is stored sealed and shown only to
// members.

// MaxOrgChannels caps an org's channels: companions hold 8–40, so a Guide
// listing more than this wouldn't fit on most of them anyway.
const MaxOrgChannels = 20

// ErrChannelLimit is returned when the org already has MaxOrgChannels channels.
var ErrChannelLimit = errors.New("store: organization channel limit reached")

// OrgChannel is one of an org's channels.
type OrgChannel struct {
	ID   int64
	Name string
	// SecretSealed is a private channel's 16-byte key, sealed with the master
	// key (internal/secretbox); nil for a hashtag channel.
	SecretSealed []byte
	// The suggested scope: ScopeRegionID, or with neither set the member's
	// default scope, or Unscoped.
	ScopeRegionID *int64
	Unscoped      bool
	Note          string
}

// Hashtag reports whether the channel is a hashtag channel, keyed by its name.
func (c OrgChannel) Hashtag() bool { return IsHashtagChannel(c.Name) }

// IsHashtagChannel reports whether a channel name is a hashtag channel's.
func IsHashtagChannel(name string) bool { return strings.HasPrefix(name, "#") }

const orgChannelColumns = `id, name, secret_sealed, scope_region_id, unscoped, note`

func scanOrgChannel(r pgx.Row) (OrgChannel, error) {
	var c OrgChannel
	err := r.Scan(&c.ID, &c.Name, &c.SecretSealed, &c.ScopeRegionID, &c.Unscoped, &c.Note)
	return c, err
}

// ListOrgChannels returns an org's channels, hashtag channels first, then by
// name. Bounded by MaxOrgChannels.
func (s *Store) ListOrgChannels(ctx context.Context, orgID int64) ([]OrgChannel, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+orgChannelColumns+` FROM org_channels WHERE org_id = $1
		ORDER BY left(name, 1) <> '#', lower(name), id`, orgID)
	if err != nil {
		return nil, fmt.Errorf("list org channels: %w", err)
	}
	return collectRows(rows, scanOrgChannel)
}

// GetOrgChannel returns one of an org's channels, or ErrNotFound.
func (s *Store) GetOrgChannel(ctx context.Context, orgID, channelID int64) (*OrgChannel, error) {
	c, err := scanOrgChannel(s.pool.QueryRow(ctx,
		`SELECT `+orgChannelColumns+` FROM org_channels WHERE id = $1 AND org_id = $2`, channelID, orgID))
	if err != nil {
		return nil, notFoundOr(err, "get org channel")
	}
	return &c, nil
}

// CreateOrgChannel adds a channel to an org. A private channel needs its sealed
// key; a hashtag channel has none. Returns ErrChannelLimit at the cap, or
// ErrDuplicate when the org already has a channel by that name.
func (s *Store) CreateOrgChannel(ctx context.Context, orgID int64, c OrgChannel) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return fmt.Errorf("lock org: %w", err)
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM org_channels WHERE org_id = $1`, orgID).Scan(&count); err != nil {
			return fmt.Errorf("count channels: %w", err)
		}
		if count >= MaxOrgChannels {
			return ErrChannelLimit
		}
		return tx.QueryRow(ctx, `
			INSERT INTO org_channels (org_id, name, secret_sealed, scope_region_id, unscoped, note)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
			orgID, c.Name, c.SecretSealed, c.ScopeRegionID, c.Unscoped, c.Note).Scan(&id)
	})
	switch {
	case isUniqueViolation(err):
		return 0, ErrDuplicate
	case errors.Is(err, ErrChannelLimit):
		return 0, err
	case err != nil:
		return 0, fmt.Errorf("create org channel: %w", err)
	}
	return id, nil
}

// UpdateOrgChannel replaces one of an org's channels. c.SecretSealed is the
// private key to store; nil keeps the one stored (renaming a hashtag channel to
// a private name needs a key — the schema refuses one without). Returns
// ErrNotFound or ErrDuplicate.
func (s *Store) UpdateOrgChannel(ctx context.Context, orgID, channelID int64, c OrgChannel) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE org_channels SET name = $3,
			secret_sealed = CASE WHEN left($3, 1) = '#' THEN NULL ELSE COALESCE($4, secret_sealed) END,
			scope_region_id = $5, unscoped = $6, note = $7
		WHERE id = $1 AND org_id = $2`,
		channelID, orgID, c.Name, c.SecretSealed, c.ScopeRegionID, c.Unscoped, c.Note)
	if isUniqueViolation(err) {
		return ErrDuplicate
	}
	if err != nil {
		return fmt.Errorf("update org channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOrgChannel removes one of an org's channels, or returns ErrNotFound.
func (s *Store) DeleteOrgChannel(ctx context.Context, orgID, channelID int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM org_channels WHERE id = $1 AND org_id = $2`, channelID, orgID)
	if err != nil {
		return fmt.Errorf("delete org channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
