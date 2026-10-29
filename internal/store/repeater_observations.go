package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MeshTender/MeshTender/internal/observe"
)

// A repeater's observed state is what its own verified adverts say about it,
// as heard by the approved observers of any org it's in. It sits beside what
// the owner registered and never overwrites it. It's kept per public key, not
// per repeater: two owners can register the same key, and the repeater says
// the same thing to every org that hears it.

// Retention of repeater_adverts rows. Flood rows feed the interval estimate,
// so they cover at least three adverts at the 168 h maximum; zero-hop rows
// only deduplicate copies, and a fresh install sends one every 2 minutes.
const (
	RepeaterFloodAdvertRetention   = 35 * 24 * time.Hour
	RepeaterZeroHopAdvertRetention = 48 * time.Hour
)

// RepeaterAdvert is a verified advert from a registered repeater, as the
// ingester records it.
type RepeaterAdvert struct {
	PublicKeyHex string
	AdvertTS     uint32
	ReceivedAt   time.Time
	// Flood: sent as a (possibly scoped) flood rather than zero-hop.
	Flood    bool
	Name     string
	Lat, Lon *float64
	// PathHashSize and Packet are only meaningful for a flood advert.
	PathHashSize uint8
	Packet       []byte
}

// RepeaterObservation is the latest of what a repeater's adverts said.
type RepeaterObservation struct {
	PublicKeyHex  string
	LastHeardAt   time.Time
	LastFloodAt   *time.Time
	LastZeroHopAt *time.Time
	AdvertName    string
	// AdvertLat and AdvertLon are nil when the newest advert carried no location.
	AdvertLat, AdvertLon *float64
	// ClockOffset is heard time minus the sender's timestamp: positive when the
	// repeater's clock is behind.
	ClockOffset time.Duration
	// PathHashSize is nil until a flood advert has been heard.
	PathHashSize *int16
	// LastFloodPacket is the newest flood advert as heard, for observe.MatchScope.
	LastFloodPacket []byte
	FloodInterval   *observe.FloodInterval
}

// RecordRepeaterAdvert records one copy of a repeater's advert. Only the first
// copy of each advert counts — other observers' copies, and replays of one
// already heard, change nothing — and it reports whether this was it. The
// newest advert's name and location win; a flood advert also refreshes the
// path hash size, scope packet and interval estimate.
func (s *Store) RecordRepeaterAdvert(ctx context.Context, a RepeaterAdvert) (bool, error) {
	var created bool
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO repeater_adverts (public_key_hex, advert_ts, zero_hop, first_heard_at)
			VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
			a.PublicKeyHex, int64(a.AdvertTS), !a.Flood, a.ReceivedAt)
		if err != nil {
			return fmt.Errorf("insert repeater advert: %w", err)
		}
		if created = tag.RowsAffected() == 1; !created {
			return nil
		}
		var floodAt, zeroHopAt *time.Time
		var hashSize *int16
		var packet []byte
		if a.Flood {
			floodAt, hashSize, packet = &a.ReceivedAt, new(int16(a.PathHashSize)), a.Packet
		} else {
			zeroHopAt = &a.ReceivedAt
		}
		offset := a.ReceivedAt.Unix() - int64(a.AdvertTS)
		// GREATEST/CASE on heard time: adverts can be processed out of order,
		// and the sender's own timestamp can't be trusted to order them.
		if _, err := tx.Exec(ctx, `
			INSERT INTO repeater_observations AS o (public_key_hex, last_heard_at, last_flood_at,
				last_zero_hop_at, advert_name, advert_lat, advert_lon, clock_offset_s, path_hash_size,
				last_flood_packet)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			ON CONFLICT (public_key_hex) DO UPDATE SET
				last_heard_at = GREATEST(o.last_heard_at, EXCLUDED.last_heard_at),
				last_flood_at = GREATEST(o.last_flood_at, EXCLUDED.last_flood_at),
				last_zero_hop_at = GREATEST(o.last_zero_hop_at, EXCLUDED.last_zero_hop_at),
				advert_name = CASE WHEN EXCLUDED.last_heard_at >= o.last_heard_at
					THEN EXCLUDED.advert_name ELSE o.advert_name END,
				advert_lat = CASE WHEN EXCLUDED.last_heard_at >= o.last_heard_at
					THEN EXCLUDED.advert_lat ELSE o.advert_lat END,
				advert_lon = CASE WHEN EXCLUDED.last_heard_at >= o.last_heard_at
					THEN EXCLUDED.advert_lon ELSE o.advert_lon END,
				clock_offset_s = CASE WHEN EXCLUDED.last_heard_at >= o.last_heard_at
					THEN EXCLUDED.clock_offset_s ELSE o.clock_offset_s END,
				path_hash_size = CASE WHEN EXCLUDED.last_flood_at >= COALESCE(o.last_flood_at, '-infinity')
					THEN EXCLUDED.path_hash_size ELSE o.path_hash_size END,
				last_flood_packet = CASE WHEN EXCLUDED.last_flood_at >= COALESCE(o.last_flood_at, '-infinity')
					THEN EXCLUDED.last_flood_packet ELSE o.last_flood_packet END`,
			a.PublicKeyHex, a.ReceivedAt, floodAt, zeroHopAt, a.Name, a.Lat, a.Lon, offset,
			hashSize, packet); err != nil {
			return fmt.Errorf("upsert repeater observation: %w", err)
		}
		if !a.Flood {
			return nil
		}
		return updateFloodInterval(ctx, tx, a.PublicKeyHex)
	})
	return created, err
}

// updateFloodInterval re-estimates a key's flood advert interval from its
// retained flood adverts: bounded by RepeaterFloodAdvertRetention at the 3 h
// minimum interval, a few hundred rows.
func updateFloodInterval(ctx context.Context, tx pgx.Tx, key string) error {
	rows, err := tx.Query(ctx, `
		SELECT first_heard_at FROM repeater_adverts
		WHERE public_key_hex = $1 AND NOT zero_hop`, key)
	if err != nil {
		return fmt.Errorf("list flood adverts: %w", err)
	}
	heard, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		return fmt.Errorf("list flood adverts: %w", err)
	}
	var hours *int
	samples := 0
	if fi, ok := observe.EstimateFloodInterval(heard); ok {
		hours, samples = &fi.Hours, fi.Samples
	}
	if _, err := tx.Exec(ctx, `
		UPDATE repeater_observations SET flood_interval_h = $2, flood_interval_samples = $3
		WHERE public_key_hex = $1`, key, hours, samples); err != nil {
		return fmt.Errorf("update flood interval: %w", err)
	}
	return nil
}

const repeaterObservationColumns = `public_key_hex, last_heard_at, last_flood_at, last_zero_hop_at,
	advert_name, advert_lat, advert_lon, clock_offset_s, path_hash_size, last_flood_packet,
	flood_interval_h, flood_interval_samples`

func scanRepeaterObservation(r pgx.Row) (RepeaterObservation, error) {
	var o RepeaterObservation
	var offset int64
	var hours *int
	var samples int
	err := r.Scan(&o.PublicKeyHex, &o.LastHeardAt, &o.LastFloodAt, &o.LastZeroHopAt, &o.AdvertName,
		&o.AdvertLat, &o.AdvertLon, &offset, &o.PathHashSize, &o.LastFloodPacket, &hours, &samples)
	o.ClockOffset = time.Duration(offset) * time.Second
	if hours != nil {
		o.FloodInterval = &observe.FloodInterval{Hours: *hours, Samples: samples}
	}
	return o, err
}

// GetRepeaterObservation returns what's been heard from a key, or ErrNotFound.
func (s *Store) GetRepeaterObservation(ctx context.Context, key string) (*RepeaterObservation, error) {
	o, err := scanRepeaterObservation(s.pool.QueryRow(ctx,
		`SELECT `+repeaterObservationColumns+` FROM repeater_observations WHERE public_key_hex = $1`, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get repeater observation: %w", err)
	}
	return &o, nil
}

// ListRepeaterObservations returns what's been heard from each of keys, by
// key; keys never heard are absent. One query for a whole list page.
func (s *Store) ListRepeaterObservations(ctx context.Context, keys []string) (map[string]RepeaterObservation, error) {
	out := make(map[string]RepeaterObservation, len(keys))
	if len(keys) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+repeaterObservationColumns+` FROM repeater_observations WHERE public_key_hex = ANY($1)`, keys)
	if err != nil {
		return nil, fmt.Errorf("list repeater observations: %w", err)
	}
	obs, err := collectRows(rows, scanRepeaterObservation)
	if err != nil {
		return nil, fmt.Errorf("list repeater observations: %w", err)
	}
	for _, o := range obs {
		out[o.PublicKeyHex] = o
	}
	return out, nil
}

// OrgHasRepeaterKey reports whether a repeater with this public key takes part
// in the org (owned by a member, not opted out) — whether the org's observers
// may record its adverts.
func (s *Store) OrgHasRepeaterKey(ctx context.Context, orgID int64, key string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM repeaters r
			JOIN org_members om ON om.org_id = $1 AND om.user_id = r.owner_id
			WHERE r.public_key_hex = $2
			  AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
			                  WHERE e.org_id = $1 AND e.repeater_id = r.id))`,
		orgID, key).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("org has repeater key: %w", err)
	}
	return ok, nil
}

// PruneRepeaterAdverts deletes advert rows past their retention, and the
// observations of keys no longer registered to any repeater. Signature fits
// the janitor.
func (s *Store) PruneRepeaterAdverts(ctx context.Context) (int64, error) {
	now := time.Now()
	var n int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		for _, del := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM repeater_adverts WHERE NOT zero_hop AND first_heard_at < $1`,
				[]any{now.Add(-RepeaterFloodAdvertRetention)}},
			{`DELETE FROM repeater_adverts WHERE zero_hop AND first_heard_at < $1`,
				[]any{now.Add(-RepeaterZeroHopAdvertRetention)}},
			{`DELETE FROM repeater_observations o
			  WHERE NOT EXISTS (SELECT 1 FROM repeaters r WHERE r.public_key_hex = o.public_key_hex)`, nil},
		} {
			tag, err := tx.Exec(ctx, del.sql, del.args...)
			if err != nil {
				return fmt.Errorf("prune repeater adverts: %w", err)
			}
			n += tag.RowsAffected()
		}
		return nil
	})
	return n, err
}

// RegionName is a region's MeshCore name and its human label.
type RegionName struct {
	Token       string
	DisplayName string
}

// ListRepeaterRegionNames returns the regions of every org the repeater takes
// part in — the candidates for the scope its flood adverts were sent with.
func (s *Store) ListRepeaterRegionNames(ctx context.Context, repeaterID int64) ([]RegionName, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (cr.token) cr.token, cr.display_name
		FROM repeaters r
		JOIN org_members om ON om.user_id = r.owner_id
		JOIN config_regions cr ON cr.org_id = om.org_id
		WHERE r.id = $1
		  AND NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                  WHERE e.org_id = om.org_id AND e.repeater_id = r.id)
		ORDER BY cr.token, cr.display_name`, repeaterID)
	if err != nil {
		return nil, fmt.Errorf("list repeater region names: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (RegionName, error) {
		var n RegionName
		err := r.Scan(&n.Token, &n.DisplayName)
		return n, err
	})
}
