package store

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
)

// A check-in is one advert from a registered companion, kept as counts: when
// it was first heard, how many observers heard it, how many repeaters were
// seen relaying it, and whether it was zero-hop. Which repeaters carried it,
// and in what order, would say roughly where the member is; the sets behind
// the counts exist only while the advert propagates (see AdvertSetTTL).

// CheckinRetention is how long check-ins (and the net sessions whose rosters
// are read from them) are kept.
const CheckinRetention = 365 * 24 * time.Hour

// AdvertSetTTL is how long the relay and receiver sets behind a check-in's
// counts are kept after it was first heard. A flood advert reaches every
// observer within seconds; the margin covers slow uplinks. The ingester's
// leader sweeps them every reconcile pass (at most 30s), so nothing outlives
// this by much.
const AdvertSetTTL = 5 * time.Minute

// AdvertHeard is one observer's copy of a registered companion's advert, as
// the ingester records it: the advert's identity, who heard it, and the
// repeaters on its path. Path order is deliberately not kept.
type AdvertHeard struct {
	CompanionID          int64
	AdvertTS             uint32
	ObserverPublicKeyHex string
	ReceivedAt           time.Time
	// ZeroHop is true for a zero-hop advert (direct, empty path).
	ZeroHop bool
	// Relays are the path entries: one hash per repeater that forwarded it.
	Relays [][]byte
}

// RecordAdvertHeard adds one copy of an advert to its check-in: the check-in
// row is created on the first copy, the copy's observer and relays are added to
// the advert's sets, and the counts are refreshed from them — exactly, however
// many copies, duplicates or replicas there are. It also advances the
// companion's last-heard state; the first copy heard after registration
// confirms it. It reports whether this copy created the check-in.
func (s *Store) RecordAdvertHeard(ctx context.Context, h AdvertHeard) (bool, error) {
	var created bool
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx, `
			INSERT INTO advert_checkins (companion_id, advert_ts, first_heard_at, zero_hop)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (companion_id, advert_ts) DO UPDATE SET
				first_heard_at = LEAST(advert_checkins.first_heard_at, EXCLUDED.first_heard_at),
				zero_hop = advert_checkins.zero_hop AND EXCLUDED.zero_hop
			RETURNING id, (xmax = 0)`,
			h.CompanionID, int64(h.AdvertTS), h.ReceivedAt, h.ZeroHop).Scan(&id, &created)
		if err != nil {
			return fmt.Errorf("upsert checkin: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO advert_receivers (checkin_id, observer_public_key_hex) VALUES ($1, $2)
			ON CONFLICT DO NOTHING`, id, h.ObserverPublicKeyHex); err != nil {
			return fmt.Errorf("add receiver: %w", err)
		}
		if len(h.Relays) > 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO advert_relays (checkin_id, hash) SELECT $1, unnest($2::bytea[])
				ON CONFLICT DO NOTHING`, id, h.Relays); err != nil {
				return fmt.Errorf("add relays: %w", err)
			}
		}
		// GREATEST: a straggling copy arriving after the sets expired must not
		// shrink counts that were taken while they were complete.
		if _, err := tx.Exec(ctx, `
			UPDATE advert_checkins SET
				observers = GREATEST(observers, (SELECT count(*) FROM advert_receivers WHERE checkin_id = $1)),
				spread = GREATEST(spread, (SELECT count(*) FROM advert_relays WHERE checkin_id = $1))
			WHERE id = $1`, id); err != nil {
			return fmt.Errorf("count checkin: %w", err)
		}
		_, err = tx.Exec(ctx, `
			UPDATE companions SET
				last_heard_at = GREATEST(last_heard_at, $2),
				last_advert_ts = GREATEST(last_advert_ts, $3),
				confirmed_at = COALESCE(confirmed_at, CASE WHEN $2 >= created_at THEN $2 END)
			WHERE id = $1`, h.CompanionID, h.ReceivedAt, int64(h.AdvertTS))
		if err != nil {
			return fmt.Errorf("advance companion: %w", err)
		}
		return nil
	})
	return created, err
}

// PruneAdvertSets deletes the relay and receiver sets of adverts first heard
// more than AdvertSetTTL ago, leaving their counts. Signature fits the janitor.
func (s *Store) PruneAdvertSets(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-AdvertSetTTL)
	var n int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		for _, del := range []string{
			`DELETE FROM advert_relays x USING advert_checkins c WHERE x.checkin_id = c.id AND c.first_heard_at < $1`,
			`DELETE FROM advert_receivers x USING advert_checkins c WHERE x.checkin_id = c.id AND c.first_heard_at < $1`,
		} {
			tag, err := tx.Exec(ctx, del, cutoff)
			if err != nil {
				return fmt.Errorf("prune advert sets: %w", err)
			}
			n += tag.RowsAffected()
		}
		return nil
	})
	return n, err
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

// Checkin is one advert from a companion, as counts.
type Checkin struct {
	ID           int64
	AdvertTS     uint32
	FirstHeardAt time.Time
	ZeroHop      bool
	Observers    int
	Spread       int
}

// CheckinsPageSize is the companion page's check-ins page size.
const CheckinsPageSize = 50

// CheckinCursor is the keyset position in a companion's check-ins, newest
// first: the last row's (first_heard_at, id).
type CheckinCursor struct {
	FirstHeardAt time.Time
	ID           int64
}

// ListCompanionCheckinsPage returns one keyset page of a companion's check-ins,
// newest first.
func (s *Store) ListCompanionCheckinsPage(ctx context.Context, companionID int64, before *CheckinCursor) ([]Checkin, bool, error) {
	first := before == nil
	var curAt time.Time
	var curID int64
	if !first {
		curAt, curID = before.FirstHeardAt, before.ID
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, advert_ts, first_heard_at, zero_hop, observers, spread
		FROM advert_checkins
		WHERE companion_id = $1
		  AND ($2 OR (first_heard_at, id) < ($3, $4))
		ORDER BY first_heard_at DESC, id DESC
		LIMIT $5`, companionID, first, curAt, curID, CheckinsPageSize+1)
	if err != nil {
		return nil, false, fmt.Errorf("list checkins: %w", err)
	}
	out, err := collectRows(rows, func(r pgx.Row) (Checkin, error) {
		var c Checkin
		var ts int64
		err := r.Scan(&c.ID, &ts, &c.FirstHeardAt, &c.ZeroHop, &c.Observers, &c.Spread)
		c.AdvertTS = uint32(ts) //nolint:gosec // stored from a uint32
		return c, err
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(out) > CheckinsPageSize
	if hasMore {
		out = out[:CheckinsPageSize]
	}
	return out, hasMore, nil
}

// PruneCheckins deletes check-ins, and the net sessions whose rosters they
// fed, older than CheckinRetention. Signature fits the janitor.
func (s *Store) PruneCheckins(ctx context.Context) (int64, error) {
	cutoff := time.Now().Add(-CheckinRetention)
	var n int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM advert_checkins WHERE first_heard_at < $1`, cutoff)
		if err != nil {
			return fmt.Errorf("prune checkins: %w", err)
		}
		n = tag.RowsAffected()
		if _, err := tx.Exec(ctx, `DELETE FROM net_sessions WHERE ends_at < $1`, cutoff); err != nil {
			return fmt.Errorf("prune net sessions: %w", err)
		}
		return nil
	})
	return n, err
}
