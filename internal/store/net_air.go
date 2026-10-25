package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

// ActiveNetLog is an in-progress session of a net that logs channel messages,
// with what's needed to hear them.
type ActiveNetLog struct {
	OrgID     int64
	SessionID int64
	NetName   string
	Air       NetAir
}

// ListActiveNetLogs returns every session in progress at now whose net has a
// log prefix, across all orgs. Sessions start and end on whole minutes, so a
// caller can reuse the result until the minute changes. Bounded by the nets
// (MaxNetsPerOrg per org), each finding its session on the (net_id,
// starts_at) key.
func (s *Store) ListActiveNetLogs(ctx context.Context, now time.Time) ([]ActiveNetLog, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT n.org_id, ns.id, n.name, n.channel_kind, n.channel_name, n.channel_psk_sealed,
		       n.on_air_name, n.log_prefix
		FROM check_in_nets n
		JOIN LATERAL (
			SELECT id, ends_at FROM net_sessions
			WHERE net_id = n.id AND starts_at <= $1
			ORDER BY starts_at DESC LIMIT 1) ns ON ns.ends_at > $1
		WHERE n.log_prefix <> ''`, now)
	if err != nil {
		return nil, fmt.Errorf("list active net logs: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (ActiveNetLog, error) {
		var l ActiveNetLog
		return l, r.Scan(&l.OrgID, &l.SessionID, &l.NetName, &l.Air.ChannelKind, &l.Air.ChannelName,
			&l.Air.PSKSealed, &l.Air.OnAirName, &l.Air.LogPrefix)
	})
}
