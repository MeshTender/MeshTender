// Package store provides the Postgres-backed persistence layer for MeshTender.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// defaultStatementTimeout caps any single statement on the app pool so one slow
// query can't hold a pooled connection indefinitely and starve other requests.
// A backstop, not an SLA — normal queries finish in milliseconds. Migrations are
// exempt (see migrationDB). Overridable via a statement_timeout in the DSN.
const defaultStatementTimeout = "30s"

// Store wraps a pgx connection pool and the data-access methods built on it.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a connection pool against the given DSN and verifies connectivity.
// The pool is bounded and connections are recycled, and each statement is capped
// (defaultStatementTimeout) unless the DSN sets its own statement_timeout.
func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Bound the pool so a burst of slow requests can't exhaust connections, and
	// recycle connections so none lives forever (jitter avoids synchronized churn).
	// Respect a pool_max_conns set in the DSN (operator tuning; the test harness
	// pins it low), applying our default only when unspecified.
	if !strings.Contains(dsn, "pool_max_conns") {
		cfg.MaxConns = 16
	}
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnLifetimeJitter = 5 * time.Minute
	cfg.MaxConnIdleTime = 30 * time.Minute
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	if _, ok := cfg.ConnConfig.RuntimeParams["statement_timeout"]; !ok {
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = defaultStatementTimeout
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the underlying pool.
func (s *Store) Close() { s.pool.Close() }

// Pool exposes the underlying pgx pool for components that need it directly
// (e.g. the scs session store).
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// Migrate runs all embedded goose migrations against the database, holding a Postgres
// advisory lock for the duration.
//
// The lock is what makes this safe to call from every replica at boot. Without it, a
// rolling deploy has several instances running `goose up` concurrently against one
// database: they interleave on the version table and can deadlock, double-apply, or
// leave a migration recorded but not fully applied. With it, the first instance to
// arrive migrates and the rest block, then find nothing pending and continue.
//
// goose's own session locker is used rather than a hand-rolled pg_advisory_lock,
// because a Postgres advisory lock is SESSION-scoped: taking it on one pooled
// connection and running the migration on another would silently protect nothing.
// goose pins a single *sql.Conn for the lock and the migrations together.
//
// One residual, by design: Provider.Up checks HasPending BEFORE acquiring the lock, and
// that check doesn't respect the locker. So multiple instances can all decide there's
// work to do and then serialize — the loser simply applies nothing. goose likewise
// retries creation of the version table, which is the one statement that can race on a
// brand-new database.
func (s *Store) Migrate(ctx context.Context) error {
	// The Provider takes the FS rooted at the migrations themselves, unlike the legacy
	// API's global SetBaseFS + directory argument.
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("migrations fs: %w", err)
	}
	db := s.migrationDB()
	defer func() { _ = db.Close() }()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("new session locker: %w", err)
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, sub, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("new goose provider: %w", err)
	}
	results, err := p.Up(ctx)
	if err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	// Report through slog rather than goose's own stdout logger, so migrations land in
	// the same structured stream as everything else. Silent when there's nothing to do,
	// which is every boot after the first.
	for _, r := range results {
		slog.Info("migration applied",
			"version", r.Source.Version, "path", r.Source.Path, "duration", r.Duration)
	}
	return nil
}

// migrationDB opens a dedicated database/sql handle for migrations with no
// statement_timeout. Migrations legitimately run long (index builds, backfills),
// so they must not inherit the app pool's per-statement cap (see New). It derives
// from the pool's already-parsed connection config (so pgxpool-only params like
// pool_max_conns are stripped) and overrides just the timeout.
func (s *Store) migrationDB() *sql.DB {
	cc := s.pool.Config().ConnConfig.Copy()
	if cc.RuntimeParams == nil {
		cc.RuntimeParams = map[string]string{}
	}
	cc.RuntimeParams["statement_timeout"] = "0" // uncapped for migrations
	return stdlib.OpenDB(*cc)
}
