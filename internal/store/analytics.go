package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AnalyticsEvent is one recorded request, written in batches to analytics_events.
type AnalyticsEvent struct {
	Ts      time.Time
	Surface string
	Host    string
	Path    string
	Method  string
	Status  int
	Kind    string // "visit" | "probe" | "notfound" | "bot" — see internal/analytics
	Visitor string
}

// InsertAnalyticsEvents bulk-inserts a batch of events via COPY.
func (s *Store) InsertAnalyticsEvents(ctx context.Context, evs []AnalyticsEvent) error {
	if len(evs) == 0 {
		return nil
	}
	rows := make([][]any, len(evs))
	for i, e := range evs {
		rows[i] = []any{e.Ts, e.Surface, e.Host, e.Path, e.Method, e.Status, e.Kind, e.Visitor}
	}
	_, err := s.pool.CopyFrom(ctx,
		pgx.Identifier{"analytics_events"},
		[]string{"ts", "surface", "host", "path", "method", "status", "kind", "visitor"},
		pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("insert analytics events: %w", err)
	}
	return nil
}

// RollupAnalytics recomputes the daily aggregate tables for the last two calendar
// days (today + yesterday) from the raw events — idempotent, so it can run on a
// ticker. Older days were rolled up on previous runs and don't change.
func (s *Store) RollupAnalytics(ctx context.Context) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		stmts := []string{
			`DELETE FROM analytics_daily WHERE day >= (now() - interval '1 day')::date`,
			`INSERT INTO analytics_daily (day, kind, requests, visitors)
			 SELECT ts::date, kind, count(*), count(DISTINCT visitor)
			 FROM analytics_events WHERE ts >= (now() - interval '1 day')::date
			 GROUP BY ts::date, kind`,
			`DELETE FROM analytics_daily_surface WHERE day >= (now() - interval '1 day')::date`,
			`INSERT INTO analytics_daily_surface (day, kind, surface, requests)
			 SELECT ts::date, kind, surface, count(*)
			 FROM analytics_events WHERE ts >= (now() - interval '1 day')::date
			 GROUP BY ts::date, kind, surface`,
			`DELETE FROM analytics_daily_path WHERE day >= (now() - interval '1 day')::date`,
			`INSERT INTO analytics_daily_path (day, kind, path, hits)
			 SELECT ts::date, kind, path, count(*)
			 FROM analytics_events WHERE ts >= (now() - interval '1 day')::date
			 GROUP BY ts::date, kind, path`,
		}
		for _, q := range stmts {
			if _, err := tx.Exec(ctx, q); err != nil {
				return fmt.Errorf("rollup analytics: %w", err)
			}
		}
		return nil
	})
}

// PruneAnalytics drops raw events older than keepDays (aggregates are retained).
func (s *Store) PruneAnalytics(ctx context.Context, keepDays int) error {
	_, err := s.pool.Exec(ctx,
		fmt.Sprintf(`DELETE FROM analytics_events WHERE ts < now() - interval '%d days'`, keepDays))
	if err != nil {
		return fmt.Errorf("prune analytics: %w", err)
	}
	return nil
}

// DayStat is one day's totals for the traffic chart.
type DayStat struct {
	Day      time.Time
	Requests int64
	Visitors int64
}

// SurfaceStat is request volume for one surface over a window.
type SurfaceStat struct {
	Surface  string
	Requests int64
}

// PathStat is hit volume for one path over a window.
type PathStat struct {
	Path string
	Hits int64
}

// HostStat is request volume for one request host over a window. Useful for
// seeing which hosts fall into the "custom" surface (anything not matching the
// configured app/auth/root hosts — e.g. the bare IP, a probe, or a crawler).
type HostStat struct {
	Host     string
	Requests int64
}

// VisitorStat is request volume for one (daily-rotating) visitor hash, plus the
// surface and last path it was seen on, for a "traffic by user" view.
type VisitorStat struct {
	Visitor  string
	Surface  string
	LastSeen time.Time
	LastPath string
	Requests int64
}

// KindStat is request volume and distinct sources for one event kind.
type KindStat struct {
	Kind     string
	Requests int64
	Sources  int64
}

// AnalyticsKindSummary returns per-kind totals over the last `days` days. It
// reads raw events rather than the rollups so "sources" is a true distinct count
// across the window — summing the daily rollup's visitor counts would instead
// count a scanner that runs every day once per day.
func (s *Store) AnalyticsKindSummary(ctx context.Context, days int) ([]KindStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT kind, count(*), count(DISTINCT visitor) FROM analytics_events
		WHERE ts >= now() - ($1::int * interval '1 day')
		GROUP BY kind ORDER BY count(*) DESC`, days)
	if err != nil {
		return nil, fmt.Errorf("analytics kind summary: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (KindStat, error) {
		var k KindStat
		return k, r.Scan(&k.Kind, &k.Requests, &k.Sources)
	})
}

// AnalyticsTopHosts returns the busiest request hosts of one kind over the last
// `days` days, read live from the raw events so the breakdown reflects current
// traffic.
func (s *Store) AnalyticsTopHosts(ctx context.Context, days int, kind string, limit int) ([]HostStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT host, count(*) FROM analytics_events
		WHERE ts >= now() - ($1::int * interval '1 day') AND kind = $2
		GROUP BY host ORDER BY count(*) DESC LIMIT $3`, days, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("analytics top hosts: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (HostStat, error) {
		var h HostStat
		return h, r.Scan(&h.Host, &h.Requests)
	})
}

// AnalyticsTopVisitors returns the busiest visitor hashes of one kind over the
// last `days` days, with the surface and most-recent path each was seen on.
// Hashes rotate daily, so a heavy daily user appears once per day, not once
// overall. Filtering by kind is what keeps a scanner replaying a wordlist out of
// the visitor list — it can outweigh every real reader combined.
func (s *Store) AnalyticsTopVisitors(ctx context.Context, days int, kind string, limit int) ([]VisitorStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT e.visitor, count(*) AS n,
		       (array_agg(e.surface ORDER BY e.ts DESC))[1] AS surface,
		       max(e.ts) AS last_seen,
		       (array_agg(e.path ORDER BY e.ts DESC))[1] AS last_path
		FROM analytics_events e
		WHERE e.ts >= now() - ($1::int * interval '1 day') AND e.kind = $2
		GROUP BY e.visitor ORDER BY n DESC LIMIT $3`, days, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("analytics top visitors: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (VisitorStat, error) {
		var v VisitorStat
		return v, r.Scan(&v.Visitor, &v.Requests, &v.Surface, &v.LastSeen, &v.LastPath)
	})
}

// AnalyticsDaily returns the last `days` days of totals for one kind, oldest
// first.
func (s *Store) AnalyticsDaily(ctx context.Context, days int, kind string) ([]DayStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT day, requests, visitors FROM analytics_daily
		WHERE day >= (now()::date - $1::int) AND kind = $2 ORDER BY day`, days, kind)
	if err != nil {
		return nil, fmt.Errorf("analytics daily: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (DayStat, error) {
		var d DayStat
		return d, r.Scan(&d.Day, &d.Requests, &d.Visitors)
	})
}

// AnalyticsBySurface returns request volume per surface for one kind over the
// last `days` days.
func (s *Store) AnalyticsBySurface(ctx context.Context, days int, kind string) ([]SurfaceStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT surface, sum(requests) FROM analytics_daily_surface
		WHERE day >= (now()::date - $1::int) AND kind = $2
		GROUP BY surface ORDER BY sum(requests) DESC`, days, kind)
	if err != nil {
		return nil, fmt.Errorf("analytics by surface: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (SurfaceStat, error) {
		var s SurfaceStat
		return s, r.Scan(&s.Surface, &s.Requests)
	})
}

// AnalyticsTopPaths returns the busiest paths of one kind over the last `days`
// days — the pages people read for "visit", the wordlist being replayed for
// "probe".
func (s *Store) AnalyticsTopPaths(ctx context.Context, days int, kind string, limit int) ([]PathStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT path, sum(hits) FROM analytics_daily_path
		WHERE day >= (now()::date - $1::int) AND kind = $2
		GROUP BY path ORDER BY sum(hits) DESC LIMIT $3`, days, kind, limit)
	if err != nil {
		return nil, fmt.Errorf("analytics top paths: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (PathStat, error) {
		var p PathStat
		return p, r.Scan(&p.Path, &p.Hits)
	})
}
