// Package store provides the Postgres-backed persistence layer for MeshTender.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
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

// Migrate runs all embedded goose migrations against the database.
func (s *Store) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set dialect: %w", err)
	}
	db := s.migrationDB()
	defer func() { _ = db.Close() }()
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
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
