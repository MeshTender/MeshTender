package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// A check-in net is a weekly window in which members check in by sending a
// flood advert. Sessions are materialized ahead of time (see
// EnsureUpcomingNetSessions); rosters are computed from advert_sightings.

// MaxNetsPerOrg bounds an org's nets, keeping the net list and the janitor's
// materialization pass bounded.
const MaxNetsPerOrg = 20

// netSessionHorizon is how far ahead sessions are materialized. Longer than a
// week so the next session always exists, even right after one ends.
const netSessionHorizon = 8 * 24 * time.Hour

// ErrNetLimit is returned when the org already has MaxNetsPerOrg nets.
var ErrNetLimit = errors.New("net limit reached")

// NetInput is the editable part of a net.
type NetInput struct {
	Name            string
	Description     string
	Weekday         time.Weekday
	StartMinute     int // minutes after local midnight
	DurationMinutes int
	Timezone        string // IANA name; must load with time.LoadLocation
	Active          bool
}

// StartClock is the local start time as "15:04", for a time input.
func (in NetInput) StartClock() string {
	return fmt.Sprintf("%02d:%02d", in.StartMinute/60, in.StartMinute%60)
}

// Net is a check-in net.
type Net struct {
	ID    int64
	OrgID int64
	NetInput
	CreatedAt time.Time
}

// Window is one session's time span, [Start, End).
type Window struct {
	Start, End time.Time
}

// NetOccurrences returns the windows of a weekly net that overlap
// [from, from+horizon), in order. The start is a local wall-clock time in loc,
// so a net stays at 19:00 across DST changes (time.Date normalizes a start that
// falls in a spring-forward gap).
func NetOccurrences(weekday time.Weekday, startMinute, durationMinutes int, loc *time.Location, from time.Time, horizon time.Duration) []Window {
	dur := time.Duration(durationMinutes) * time.Minute
	until := from.Add(horizon)
	// Begin a day before `from` (local) so a session already in progress is
	// included; walk whole local days, not 24h steps, so DST can't skip one.
	local := from.In(loc)
	day := time.Date(local.Year(), local.Month(), local.Day()-1, 0, 0, 0, 0, loc)
	var out []Window
	for ; !day.After(until); day = time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc) {
		if day.Weekday() != weekday {
			continue
		}
		start := time.Date(day.Year(), day.Month(), day.Day(), startMinute/60, startMinute%60, 0, 0, loc)
		end := start.Add(dur)
		if end.After(from) && start.Before(until) {
			out = append(out, Window{Start: start, End: end})
		}
	}
	return out
}

func (n *Net) location() (*time.Location, error) {
	return time.LoadLocation(n.Timezone)
}

const netSelect = `
	SELECT id, org_id, name, description, weekday, start_minute, duration_minutes, timezone, active, created_at
	FROM check_in_nets`

func scanNet(row pgx.Row) (*Net, error) {
	var n Net
	var weekday, startMinute int16
	err := row.Scan(&n.ID, &n.OrgID, &n.Name, &n.Description, &weekday, &startMinute,
		&n.DurationMinutes, &n.Timezone, &n.Active, &n.CreatedAt)
	if err != nil {
		return nil, err
	}
	n.Weekday, n.StartMinute = time.Weekday(weekday), int(startMinute)
	return &n, nil
}

// CreateNet adds a net to the org and materializes its upcoming sessions.
// in.Timezone must already be validated. Returns ErrNetLimit at the cap.
func (s *Store) CreateNet(ctx context.Context, orgID int64, in NetInput, createdBy int64, now time.Time) (*Net, error) {
	var net *Net
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE`, orgID); err != nil {
			return fmt.Errorf("lock org: %w", err)
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM check_in_nets WHERE org_id = $1`, orgID).Scan(&count); err != nil {
			return fmt.Errorf("count nets: %w", err)
		}
		if count >= MaxNetsPerOrg {
			return ErrNetLimit
		}
		var err error
		net, err = scanNet(tx.QueryRow(ctx, `
			INSERT INTO check_in_nets (org_id, name, description, weekday, start_minute, duration_minutes, timezone, active, created_by)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id, org_id, name, description, weekday, start_minute, duration_minutes, timezone, active, created_at`,
			orgID, in.Name, in.Description, int16(in.Weekday), int16(in.StartMinute), //nolint:gosec // validated ranges
			in.DurationMinutes, in.Timezone, in.Active, createdBy))
		if err != nil {
			return fmt.Errorf("insert net: %w", err)
		}
		return materializeNetSessions(ctx, tx, net, now)
	})
	if err != nil {
		if errors.Is(err, ErrNetLimit) {
			return nil, err
		}
		return nil, fmt.Errorf("create net: %w", err)
	}
	return net, nil
}

// UpdateNet edits the org's net. Sessions that haven't started are rebuilt on
// the new schedule; started and past sessions keep the window they ran in.
func (s *Store) UpdateNet(ctx context.Context, orgID, netID int64, in NetInput, now time.Time) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		net, err := scanNet(tx.QueryRow(ctx, `
			UPDATE check_in_nets SET name = $3, description = $4, weekday = $5, start_minute = $6,
			       duration_minutes = $7, timezone = $8, active = $9
			WHERE org_id = $1 AND id = $2
			RETURNING id, org_id, name, description, weekday, start_minute, duration_minutes, timezone, active, created_at`,
			orgID, netID, in.Name, in.Description, int16(in.Weekday), int16(in.StartMinute), //nolint:gosec // validated ranges
			in.DurationMinutes, in.Timezone, in.Active))
		if err != nil {
			return notFoundOr(err, "update net")
		}
		if _, err := tx.Exec(ctx, `DELETE FROM net_sessions WHERE net_id = $1 AND starts_at > $2`, netID, now); err != nil {
			return fmt.Errorf("drop unstarted sessions: %w", err)
		}
		return materializeNetSessions(ctx, tx, net, now)
	})
}

// DeleteNet removes the org's net and all its sessions.
func (s *Store) DeleteNet(ctx context.Context, orgID, netID int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM check_in_nets WHERE org_id = $1 AND id = $2`, orgID, netID)
	if err != nil {
		return fmt.Errorf("delete net: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetNet returns the org's net, or ErrNotFound.
func (s *Store) GetNet(ctx context.Context, orgID, netID int64) (*Net, error) {
	n, err := scanNet(s.pool.QueryRow(ctx, netSelect+` WHERE org_id = $1 AND id = $2`, orgID, netID))
	return n, notFoundOr(err, "get net")
}

// NetSummary is a net with its next session that hasn't ended (nil when the
// net is inactive).
type NetSummary struct {
	Net
	Next *NetSession
}

// ListOrgNetSummaries returns the org's nets by name (at most MaxNetsPerOrg), each with its next
// session, in one query (a LATERAL per net on the (net_id, starts_at) key).
func (s *Store) ListOrgNetSummaries(ctx context.Context, orgID int64, now time.Time) ([]NetSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.org_id, n.name, n.description, n.weekday, n.start_minute, n.duration_minutes,
		       n.timezone, n.active, n.created_at, ns.id, ns.starts_at, ns.ends_at
		FROM check_in_nets n
		LEFT JOIN LATERAL (
			SELECT id, starts_at, ends_at FROM net_sessions
			WHERE net_id = n.id AND ends_at > $2 ORDER BY starts_at LIMIT 1) ns ON TRUE
		WHERE n.org_id = $1
		ORDER BY lower(n.name), n.id`, orgID, now)
	if err != nil {
		return nil, fmt.Errorf("list org net summaries: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (NetSummary, error) {
		var ns NetSummary
		var weekday, startMinute int16
		var sid *int64
		var starts, ends *time.Time
		err := r.Scan(&ns.ID, &ns.OrgID, &ns.Name, &ns.Description, &weekday, &startMinute,
			&ns.DurationMinutes, &ns.Timezone, &ns.Active, &ns.CreatedAt, &sid, &starts, &ends)
		ns.Weekday, ns.StartMinute = time.Weekday(weekday), int(startMinute)
		if sid != nil {
			ns.Next = &NetSession{ID: *sid, NetID: ns.ID, StartsAt: *starts, EndsAt: *ends}
		}
		return ns, err
	})
}

// materializeNetSessions inserts the net's sessions overlapping the horizon.
// Idempotent: existing sessions are kept. Inactive nets get none.
func materializeNetSessions(ctx context.Context, tx pgx.Tx, net *Net, now time.Time) error {
	if !net.Active {
		return nil
	}
	loc, err := net.location()
	if err != nil {
		return fmt.Errorf("net %d timezone: %w", net.ID, err)
	}
	for _, w := range NetOccurrences(net.Weekday, net.StartMinute, net.DurationMinutes, loc, now, netSessionHorizon) {
		if _, err := tx.Exec(ctx, `
			INSERT INTO net_sessions (net_id, starts_at, ends_at) VALUES ($1, $2, $3)
			ON CONFLICT (net_id, starts_at) DO NOTHING`, net.ID, w.Start, w.End); err != nil {
			return fmt.Errorf("insert net session: %w", err)
		}
	}
	return nil
}

// EnsureUpcomingNetSessions materializes upcoming sessions for every active
// net. The janitor runs it every few minutes. Bounded by orgs × MaxNetsPerOrg.
func (s *Store) EnsureUpcomingNetSessions(ctx context.Context, now time.Time) error {
	rows, err := s.pool.Query(ctx, netSelect+` WHERE active ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list active nets: %w", err)
	}
	nets, err := collectRows(rows, scanNet)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if err := s.inTx(ctx, func(tx pgx.Tx) error { return materializeNetSessions(ctx, tx, n, now) }); err != nil {
			return err
		}
	}
	return nil
}

// NetSession is one occurrence of a net, with how many members checked in.
type NetSession struct {
	ID        int64
	NetID     int64
	StartsAt  time.Time
	EndsAt    time.Time
	CheckedIn int
}

// NetSessionsPageSize is the net page's sessions page size.
const NetSessionsPageSize = 20

// rosterCompanions is the set of companions a roster covers for org $1:
// confirmed, owned by a current member.
const rosterCompanions = `
	SELECT c.id FROM companions c
	JOIN org_members om ON om.org_id = $1 AND om.user_id = c.owner_id
	WHERE c.confirmed_at IS NOT NULL`

// ListNetSessionsPage returns one keyset page of the org's net's sessions that
// have started, newest first, each with its check-in count. before is the last
// row's starts_at, or nil for the first page.
func (s *Store) ListNetSessionsPage(ctx context.Context, orgID, netID int64, now time.Time, before *time.Time) ([]NetSession, bool, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ns.id, ns.net_id, ns.starts_at, ns.ends_at,
		       (SELECT count(DISTINCT c.owner_id) FROM advert_checkins k
		        JOIN companions c ON c.id = k.companion_id
		        WHERE k.companion_id IN (`+rosterCompanions+`)
		          AND k.first_heard_at >= ns.starts_at AND k.first_heard_at < ns.ends_at)
		FROM net_sessions ns
		JOIN check_in_nets n ON n.id = ns.net_id AND n.org_id = $1
		WHERE ns.net_id = $2 AND ns.starts_at <= $3
		  AND ($4::timestamptz IS NULL OR ns.starts_at < $4)
		ORDER BY ns.starts_at DESC
		LIMIT $5`, orgID, netID, now, before, NetSessionsPageSize+1)
	if err != nil {
		return nil, false, fmt.Errorf("list net sessions: %w", err)
	}
	out, err := collectRows(rows, func(r pgx.Row) (NetSession, error) {
		var ns NetSession
		return ns, r.Scan(&ns.ID, &ns.NetID, &ns.StartsAt, &ns.EndsAt, &ns.CheckedIn)
	})
	if err != nil {
		return nil, false, err
	}
	hasMore := len(out) > NetSessionsPageSize
	if hasMore {
		out = out[:NetSessionsPageSize]
	}
	return out, hasMore, nil
}

// NextNetSession returns the net's next session that hasn't ended, or
// ErrNotFound (an inactive net has none).
func (s *Store) NextNetSession(ctx context.Context, netID int64, now time.Time) (*NetSession, error) {
	var ns NetSession
	err := s.pool.QueryRow(ctx, `
		SELECT id, net_id, starts_at, ends_at FROM net_sessions
		WHERE net_id = $1 AND ends_at > $2 ORDER BY starts_at LIMIT 1`, netID, now).
		Scan(&ns.ID, &ns.NetID, &ns.StartsAt, &ns.EndsAt)
	if err != nil {
		return nil, notFoundOr(err, "next net session")
	}
	return &ns, nil
}

// GetNetSession returns a session of the org's net, or ErrNotFound.
func (s *Store) GetNetSession(ctx context.Context, orgID, netID, sessionID int64) (*NetSession, error) {
	var ns NetSession
	err := s.pool.QueryRow(ctx, `
		SELECT ns.id, ns.net_id, ns.starts_at, ns.ends_at FROM net_sessions ns
		JOIN check_in_nets n ON n.id = ns.net_id AND n.org_id = $1
		WHERE ns.net_id = $2 AND ns.id = $3`, orgID, netID, sessionID).
		Scan(&ns.ID, &ns.NetID, &ns.StartsAt, &ns.EndsAt)
	if err != nil {
		return nil, notFoundOr(err, "get net session")
	}
	return &ns, nil
}

// RosterEntry is one member on a session roster. A member checks in with
// whichever of their companions they have to hand, so the roster is by person:
// FirstHeardAt and the companion are from their earliest check-in in the
// session, and are empty when nothing was heard from any of their companions.
type RosterEntry struct {
	MemberName string
	// CompanionPublicID and CompanionName identify the device the member
	// checked in on (their earliest check-in in the window).
	CompanionPublicID string
	CompanionName     string
	FirstHeardAt      *time.Time
	// Spread and Observers are from the member's best check-in in the
	// session: the most repeaters seen relaying one of their adverts, and the
	// most observers that heard one. (Combining across adverts would need the
	// relay sets, which aren't kept.)
	Spread    int
	Observers int
	// ZeroHopOnly is true when every check-in was a zero-hop advert, which only
	// observers in direct range can hear.
	ZeroHopOnly bool
}

// CheckedIn reports whether the member was heard during the session.
func (e *RosterEntry) CheckedIn() bool { return e.FirstHeardAt != nil }

// NetSessionRoster returns one entry per org member with a confirmed companion,
// with what was heard from any of their companions during the window:
// checked-in members first (by time heard), then the rest by name.
// Unpaginated: bounded by the org's membership, and a roster is read as a whole.
func (s *Store) NetSessionRoster(ctx context.Context, orgID int64, session *NetSession) ([]RosterEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(NULLIF(u.display_name, ''), u.username),
		       COALESCE(h.companion_public_id, ''), COALESCE(h.companion_name, ''),
		       h.first_heard, COALESCE(h.spread, 0), COALESCE(h.observers, 0), COALESCE(h.zero_hop_only, FALSE)
		FROM users u
		JOIN org_members om ON om.org_id = $1 AND om.user_id = u.id
		LEFT JOIN LATERAL (
			SELECT min(k.first_heard_at) AS first_heard, max(k.spread) AS spread,
			       max(k.observers) AS observers, bool_and(k.zero_hop) AS zero_hop_only,
			       (array_agg(c.public_id ORDER BY k.first_heard_at, k.id))[1] AS companion_public_id,
			       (array_agg(c.name ORDER BY k.first_heard_at, k.id))[1] AS companion_name
			FROM companions c
			JOIN advert_checkins k ON k.companion_id = c.id
			WHERE c.owner_id = u.id AND c.confirmed_at IS NOT NULL
			  AND k.first_heard_at >= $2 AND k.first_heard_at < $3
			HAVING count(*) > 0) h ON TRUE
		WHERE EXISTS (SELECT 1 FROM companions c WHERE c.owner_id = u.id AND c.confirmed_at IS NOT NULL)
		ORDER BY h.first_heard NULLS LAST, lower(COALESCE(NULLIF(u.display_name, ''), u.username)), u.id`,
		orgID, session.StartsAt, session.EndsAt)
	if err != nil {
		return nil, fmt.Errorf("net session roster: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (RosterEntry, error) {
		var e RosterEntry
		return e, r.Scan(&e.MemberName, &e.CompanionPublicID, &e.CompanionName,
			&e.FirstHeardAt, &e.Spread, &e.Observers, &e.ZeroHopOnly)
	})
}
