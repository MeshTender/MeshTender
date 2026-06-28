// Package store provides the Postgres-backed persistence layer for MeshTender.
package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Store wraps a pgx connection pool and the data-access methods built on it.
type Store struct {
	pool *pgxpool.Pool
}

// New opens a connection pool against the given DSN and verifies connectivity.
func New(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
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
	// goose needs a database/sql handle; derive one from the pool config.
	db := stdlib.OpenDBFromPool(s.pool)
	defer func() { _ = db.Close() }()
	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
