package store

import (
	"context"
	"fmt"
)

// Channel kinds a net can send on (check_in_nets.channel_kind).
const (
	ChannelPublic  = "public"
	ChannelHashtag = "hashtag"
	ChannelPrivate = "private"
)

// NetAir is how net control's modem talks to a net: the channel its messages
// go out on, the region they're scoped to, the name they're sent as, and the
// prefix of channel messages worth logging. See migration 0051.
type NetAir struct {
	ChannelKind string
	// ChannelName is the hashtag channel's name (without "#"), or a private
	// channel's label.
	ChannelName string
	// PSKSealed is a private channel's key, secretbox-sealed. It never leaves
	// the server.
	PSKSealed     []byte
	ScopeRegionID *int64
	// ScopeToken is the scope region's `region def` token ("" when unscoped).
	ScopeToken string
	// OnAirName is the sender name; empty means the net's name.
	OnAirName string
	// LogPrefix starts the channel messages logged to a session; empty logs none.
	LogPrefix string
}

// HasKey reports whether a private channel key is stored.
func (a *NetAir) HasKey() bool { return len(a.PSKSealed) > 0 }

// GetNetAir returns the org's net's on-air settings, or ErrNotFound.
func (s *Store) GetNetAir(ctx context.Context, orgID, netID int64) (*NetAir, error) {
	var a NetAir
	var token *string
	err := s.pool.QueryRow(ctx, `
		SELECT n.channel_kind, n.channel_name, n.channel_psk_sealed, n.scope_region_id, r.token,
		       n.on_air_name, n.log_prefix
		FROM check_in_nets n
		LEFT JOIN config_regions r ON r.id = n.scope_region_id
		WHERE n.org_id = $1 AND n.id = $2`, orgID, netID).
		Scan(&a.ChannelKind, &a.ChannelName, &a.PSKSealed, &a.ScopeRegionID, &token, &a.OnAirName, &a.LogPrefix)
	if err != nil {
		return nil, notFoundOr(err, "get net air")
	}
	if token != nil {
		a.ScopeToken = *token
	}
	return &a, nil
}

// UpdateNetAir saves a net's on-air settings. pskSealed replaces the stored
// private key when non-nil and is otherwise kept, the way the broker password
// is; switching away from a private channel drops it. The scope region must be
// one of the org's — anything else is ErrNotFound, as is a net that isn't the
// org's. The schema refuses a private channel with no key.
func (s *Store) UpdateNetAir(ctx context.Context, orgID, netID int64, a NetAir, pskSealed []byte) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE check_in_nets SET
			channel_kind = $3,
			channel_name = $4,
			channel_psk_sealed = CASE WHEN $3 <> 'private' THEN NULL
			                          ELSE COALESCE($5, channel_psk_sealed) END,
			scope_region_id = $6,
			on_air_name = $7,
			log_prefix = $8
		WHERE org_id = $1 AND id = $2
		  AND ($6::bigint IS NULL OR EXISTS (SELECT 1 FROM config_regions WHERE id = $6 AND org_id = $1))`,
		orgID, netID, a.ChannelKind, a.ChannelName, pskSealed, a.ScopeRegionID, a.OnAirName, a.LogPrefix)
	if err != nil {
		return fmt.Errorf("update net air: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
