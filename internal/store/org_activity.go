package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// OrgActivityKind is what an entry in an org's recent activity records.
type OrgActivityKind string

const (
	// ActivityRepeaterAdded: a repeater joined the org — registered by a member,
	// or brought along when its owner joined. Who added it isn't recorded.
	ActivityRepeaterAdded OrgActivityKind = "repeater_added"
	// ActivityNetStarted: a net session is in progress.
	ActivityNetStarted OrgActivityKind = "net_started"
	// ActivityNetEnded: a net session ended, with its check-in count.
	ActivityNetEnded OrgActivityKind = "net_ended"
)

// OrgActivity is one entry in an org's recent activity. Name is the repeater's
// or the net's; NetID/SessionID are set for net entries and CheckedIn for
// ended sessions.
type OrgActivity struct {
	Kind      OrgActivityKind
	At        time.Time
	Name      string
	NetID     int64
	SessionID int64
	CheckedIn int
}

// ListOrgActivity returns the org's most recent activity at now, newest first,
// at most limit entries. It isn't a stored log: entries are derived from the
// org's repeaters and its nets' sessions, so a repeater that leaves the org or a
// deleted net drops out of it.
//
// Bounded: repeaters are the org's participating ones (their join time is
// computed, so they're sorted in memory — an org has at most a few hundred);
// sessions are the latest `limit` of each net (at most MaxNetsPerOrg, each a
// keyed read on (net_id, starts_at)); check-ins are counted only for the
// entries returned.
func (s *Store) ListOrgActivity(ctx context.Context, orgID int64, now time.Time, limit int) ([]OrgActivity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.kind, a.happened_at, a.name, a.net_id, a.session_id,
		       CASE WHEN a.kind = 'net_ended' THEN `+sessionCheckedIn+` ELSE 0 END
		FROM (
		  (SELECT 'repeater_added' AS kind, GREATEST(r.created_at, om.joined_at) AS happened_at, r.name,
		          0::bigint AS net_id, 0::bigint AS session_id, NULL::timestamptz AS starts_at, NULL::timestamptz AS ends_at
		   FROM repeaters r
		   JOIN org_members om ON om.org_id = $1 AND om.user_id = r.owner_id
		   WHERE NOT EXISTS (SELECT 1 FROM org_repeater_excludes e
		                     WHERE e.org_id = $1 AND e.repeater_id = r.id)
		     AND GREATEST(r.created_at, om.joined_at) <= $2
		   ORDER BY 2 DESC LIMIT $3)
		  UNION ALL
		  (SELECT CASE WHEN x.ends_at <= $2 THEN 'net_ended' ELSE 'net_started' END,
		          CASE WHEN x.ends_at <= $2 THEN x.ends_at ELSE x.starts_at END,
		          n.name, n.id, x.id, x.starts_at, x.ends_at
		   FROM check_in_nets n
		   CROSS JOIN LATERAL (
		     SELECT id, starts_at, ends_at FROM net_sessions
		     WHERE net_id = n.id AND starts_at <= $2
		     ORDER BY starts_at DESC LIMIT $3) x
		   WHERE n.org_id = $1)
		  ORDER BY happened_at DESC LIMIT $3
		) a
		-- sessionCheckedIn reads the session as ns.
		CROSS JOIN LATERAL (SELECT a.session_id AS id, a.starts_at, a.ends_at) ns
		ORDER BY a.happened_at DESC`, orgID, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list org activity: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (OrgActivity, error) {
		var a OrgActivity
		var kind string
		err := r.Scan(&kind, &a.At, &a.Name, &a.NetID, &a.SessionID, &a.CheckedIn)
		a.Kind = OrgActivityKind(kind)
		return a, err
	})
}

// NetSessionsCheckedIn counts the members checked in to each of the org's
// sessions in ids, in one query. Sessions of other orgs are left out.
func (s *Store) NetSessionsCheckedIn(ctx context.Context, orgID int64, ids []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT ns.id, `+sessionCheckedIn+`
		FROM net_sessions ns
		JOIN check_in_nets n ON n.id = ns.net_id AND n.org_id = $1
		WHERE ns.id = ANY($2)`, orgID, ids)
	if err != nil {
		return nil, fmt.Errorf("count session check-ins: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan session check-ins: %w", err)
		}
		out[id] = n
	}
	return out, rows.Err()
}

// OrgConfigSummary is what an org recommends for its repeaters, in brief: its
// profiles' names in display order, and whether it has any regions.
type OrgConfigSummary struct {
	Profiles   []string
	HasRegions bool
}

// GetOrgConfigSummary loads the org's OrgConfigSummary. Profiles are bounded by
// what an admin creates (a handful).
func (s *Store) GetOrgConfigSummary(ctx context.Context, orgID int64) (OrgConfigSummary, error) {
	var sum OrgConfigSummary
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE((SELECT array_agg(name ORDER BY position, name) FROM config_profiles WHERE org_id = $1), '{}'),
		       EXISTS (SELECT 1 FROM config_regions WHERE org_id = $1)`, orgID).
		Scan(&sum.Profiles, &sum.HasRegions); err != nil {
		return sum, fmt.Errorf("org config summary: %w", err)
	}
	return sum, nil
}
