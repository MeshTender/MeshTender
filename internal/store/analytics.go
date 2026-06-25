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
	Visitor string
}

// InsertAnalyticsEvents bulk-inserts a batch of events via COPY.
func (s *Store) InsertAnalyticsEvents(ctx context.Context, evs []AnalyticsEvent) error {
	if len(evs) == 0 {
		return nil
	}
	rows := make([][]any, len(evs))
	for i, e := range evs {
		rows[i] = []any{e.Ts, e.Surface, e.Host, e.Path, e.Method, e.Status, e.Visitor}
	}
	_, err := s.pool.CopyFrom(ctx,
		pgx.Identifier{"analytics_events"},
		[]string{"ts", "surface", "host", "path", "method", "status", "visitor"},
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
			`INSERT INTO analytics_daily (day, requests, visitors)
			 SELECT ts::date, count(*), count(DISTINCT visitor)
			 FROM analytics_events WHERE ts >= (now() - interval '1 day')::date
			 GROUP BY ts::date`,
			`DELETE FROM analytics_daily_surface WHERE day >= (now() - interval '1 day')::date`,
			`INSERT INTO analytics_daily_surface (day, surface, requests)
			 SELECT ts::date, surface, count(*)
			 FROM analytics_events WHERE ts >= (now() - interval '1 day')::date
			 GROUP BY ts::date, surface`,
			`DELETE FROM analytics_daily_path WHERE day >= (now() - interval '1 day')::date`,
			`INSERT INTO analytics_daily_path (day, path, hits)
			 SELECT ts::date, path, count(*)
			 FROM analytics_events WHERE ts >= (now() - interval '1 day')::date
			 GROUP BY ts::date, path`,
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

// AnalyticsDaily returns the last `days` days of totals, oldest first.
func (s *Store) AnalyticsDaily(ctx context.Context, days int) ([]DayStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT day, requests, visitors FROM analytics_daily
		WHERE day >= (now()::date - $1::int) ORDER BY day`, days)
	if err != nil {
		return nil, fmt.Errorf("analytics daily: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (DayStat, error) {
		var d DayStat
		return d, r.Scan(&d.Day, &d.Requests, &d.Visitors)
	})
}

// AnalyticsBySurface returns request volume per surface over the last `days` days.
func (s *Store) AnalyticsBySurface(ctx context.Context, days int) ([]SurfaceStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT surface, sum(requests) FROM analytics_daily_surface
		WHERE day >= (now()::date - $1::int) GROUP BY surface ORDER BY sum(requests) DESC`, days)
	if err != nil {
		return nil, fmt.Errorf("analytics by surface: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (SurfaceStat, error) {
		var s SurfaceStat
		return s, r.Scan(&s.Surface, &s.Requests)
	})
}

// AnalyticsTopPaths returns the busiest paths over the last `days` days.
func (s *Store) AnalyticsTopPaths(ctx context.Context, days, limit int) ([]PathStat, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT path, sum(hits) FROM analytics_daily_path
		WHERE day >= (now()::date - $1::int) GROUP BY path ORDER BY sum(hits) DESC LIMIT $2`, days, limit)
	if err != nil {
		return nil, fmt.Errorf("analytics top paths: %w", err)
	}
	return collectRows(rows, func(r pgx.Row) (PathStat, error) {
		var p PathStat
		return p, r.Scan(&p.Path, &p.Hits)
	})
}
