package store

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// SightingRetention is how long advert sightings (and the net sessions whose
// rosters are computed from them) are kept.
const SightingRetention = 365 * 24 * time.Hour

// AdvertSighting is one observer hearing one route of a companion's advert.
type AdvertSighting struct {
	ID                   int64
	CompanionID          int64
	ObserverPublicKeyHex string
	// ObserverName is the observer's registered name in some org, if any; empty
	// once every org has removed it.
	ObserverName string
	ReceivedAt   time.Time
	AdvertTS     uint32
	RouteType    byte
	PathHashSize uint8
	// Path is the flattened hash list, PathHashSize bytes per hop.
	Path []byte
	SNR  *float64
	RSSI *int
}

// Hops splits Path into one slice per repeater, oldest first.
func (s *AdvertSighting) Hops() [][]byte {
	return splitPath(s.Path, s.PathHashSize)
}

func splitPath(path []byte, size uint8) [][]byte {
	if size == 0 {
		return nil
	}
	hops := make([][]byte, 0, len(path)/int(size))
	for i := 0; i+int(size) <= len(path); i += int(size) {
		hops = append(hops, path[i:i+int(size)])
	}
	return hops
}

// RecordAdvertSighting stores a verified sighting and advances the companion's
// last-heard state. It reports false when the identical sighting was already
// stored (an uplink duplicate, or the same broker seen through two orgs). The
// first sighting heard after registration confirms the companion.
func (s *Store) RecordAdvertSighting(ctx context.Context, sg AdvertSighting) (bool, error) {
	var inserted bool
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO advert_sightings (companion_id, observer_public_key_hex, received_at, advert_ts,
			                              route_type, path_hash_size, path, snr, rssi)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT DO NOTHING`,
			sg.CompanionID, sg.ObserverPublicKeyHex, sg.ReceivedAt, int64(sg.AdvertTS),
			int16(sg.RouteType), int16(sg.PathHashSize), sg.Path, sg.SNR, sg.RSSI)
		if err != nil {
			return fmt.Errorf("insert sighting: %w", err)
		}
		if inserted = tag.RowsAffected() == 1; !inserted {
			return nil
		}
		_, err = tx.Exec(ctx, `
			UPDATE companions SET
				last_heard_at = GREATEST(last_heard_at, $2),
				last_advert_ts = GREATEST(last_advert_ts, $3),
				confirmed_at = COALESCE(confirmed_at, CASE WHEN $2 >= created_at THEN $2 END)
			WHERE id = $1`, sg.CompanionID, sg.ReceivedAt, int64(sg.AdvertTS))
		if err != nil {
			return fmt.Errorf("advance companion: %w", err)
		}
		return nil
	})
	return inserted, err
}

// RecordAdvertRejected notes that a companion's advert verified but was heard
// too far from its own timestamp to count, so the owner can see why.
func (s *Store) RecordAdvertRejected(ctx context.Context, companionID int64, at time.Time, skew time.Duration) error {
	// A garbage advert clock can be decades off; clamp to the column's range.
	secs := max(min(int64(skew/time.Second), math.MaxInt32), math.MinInt32)
	_, err := s.pool.Exec(ctx, `
		UPDATE companions SET last_rejected_at = $2, last_rejected_skew_s = $3 WHERE id = $1`,
		companionID, at, int32(secs))
	if err != nil {
		return fmt.Errorf("record advert rejected: %w", err)
	}
	return nil
}

// SightingsPageSize is the companion page's sightings page size.
const SightingsPageSize = 50

// SightingCursor is the keyset position in a companion's sightings, newest
// first: the last row's (received_at, id).
type SightingCursor struct {
	ReceivedAt time.Time
	ID         int64
}

// sightingObserverName picks a registered name for the observer from any org.
// Observer names describe shared infrastructure, not people.
const sightingObserverName = `
	COALESCE((SELECT o.name FROM org_observers o
	          WHERE o.public_key_hex = s.observer_public_key_hex
	          ORDER BY o.id LIMIT 1), '')`

// ListCompanionSightingsPage returns one keyset page of a companion's
// sightings, newest first.
func (s *Store) ListCompanionSightingsPage(ctx context.Context, companionID int64, before *SightingCursor) ([]AdvertSighting, bool, error) {
	first := before == nil
	var curAt time.Time
	var curID int64
	if !first {
		curAt, curID = before.ReceivedAt, before.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.companion_id, s.observer_public_key_hex, `+sightingObserverName+`,
		       s.received_at, s.advert_ts, s.route_type, s.path_hash_size, s.path, s.snr, s.rssi
		FROM advert_sightings s
		WHERE s.companion_id = $1
		  AND ($2 OR (s.received_at, s.id) < ($3, $4))
		ORDER BY s.received_at DESC, s.id DESC
		LIMIT $5`, companionID, first, curAt, curID, SightingsPageSize+1)
	if err != nil {
		return nil, false, fmt.Errorf("list sightings: %w", err)
	}
	out, err := collectRows(rows, scanSighting)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(out) > SightingsPageSize
	if hasMore {
		out = out[:SightingsPageSize]
	}
	return out, hasMore, nil
}

func scanSighting(r pgx.Row) (AdvertSighting, error) {
	var sg AdvertSighting
	var ts int64
	var route, size int16
	var snr *float32
	var rssi *int16
	err := r.Scan(&sg.ID, &sg.CompanionID, &sg.ObserverPublicKeyHex, &sg.ObserverName,
		&sg.ReceivedAt, &ts, &route, &size, &sg.Path, &snr, &rssi)
	sg.AdvertTS, sg.RouteType, sg.PathHashSize = uint32(ts), byte(route), uint8(size) //nolint:gosec // stored from these types
	if snr != nil {
		v := float64(*snr)
		sg.SNR = &v
	}
	if rssi != nil {
		v := int(*rssi)
		sg.RSSI = &v
	}
	return sg, err
}

// CompanionRoute is a distinct path a companion's adverts reached an observer
// by, with how often and how recently.
type CompanionRoute struct {
	ObserverPublicKeyHex string
	ObserverName         string
	PathHashSize         uint8
	Path                 []byte
	Count                int
	LastHeardAt          time.Time
}

// Hops splits Path into one slice per repeater, oldest first.
func (r *CompanionRoute) Hops() [][]byte { return splitPath(r.Path, r.PathHashSize) }

// companionRoutesLimit caps the routes view: the most-used paths tell the story,
// and a busy mesh can produce many one-off variants.
const companionRoutesLimit = 20

// companionRoutesWindow is how far back the routes view looks.
const companionRoutesWindow = 90 * 24 * time.Hour

// ListCompanionRoutes returns the companion's most-used routes over the last 90
// days, most frequent first.
func (s *Store) ListCompanionRoutes(ctx context.Context, companionID int64, now time.Time) ([]CompanionRoute, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.observer_public_key_hex, `+sightingObserverName+`,
		       s.path_hash_size, s.path, count(*), max(s.received_at)
		FROM advert_sightings s
		WHERE s.companion_id = $1 AND s.received_at >= $2
		GROUP BY s.observer_public_key_hex, s.path_hash_size, s.path
		ORDER BY count(*) DESC, max(s.received_at) DESC
		LIMIT $3`, companionID, now.Add(-companionRoutesWindow), companionRoutesLimit)
	if err != nil {
		return nil, fmt.Errorf("list companion routes: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (CompanionRoute, error) {
		var cr CompanionRoute
		var size int16
		err := r.Scan(&cr.ObserverPublicKeyHex, &cr.ObserverName, &size, &cr.Path, &cr.Count, &cr.LastHeardAt)
		cr.PathHashSize = uint8(size) //nolint:gosec // stored from a uint8
		return cr, err
	})
}

// PruneSightings deletes sightings, and the net sessions whose rosters they
// fed, older than SightingRetention. Signature fits the janitor.
func (s *Store) PruneSightings(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-SightingRetention)
	var n int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM advert_sightings WHERE received_at < $1`, cutoff)
		if err != nil {
			return fmt.Errorf("prune sightings: %w", err)
		}
		n = tag.RowsAffected()
		if _, err := tx.Exec(ctx, `DELETE FROM net_sessions WHERE ends_at < $1`, cutoff); err != nil {
			return fmt.Errorf("prune net sessions: %w", err)
		}
		return nil
	})
	return n, err
}
