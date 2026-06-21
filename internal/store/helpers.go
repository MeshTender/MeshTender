package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// notFoundOr maps pgx.ErrNoRows to ErrNotFound and otherwise wraps err with the
// given context. It returns nil when err is nil, so a QueryRow scan collapses to
// `return notFoundOr(err, "get org")`.
func notFoundOr(err error, what string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("%s: %w", what, err)
}

// scanID scans a single int64 column, for the `SELECT id …` list queries.
func scanID(row pgx.Row) (int64, error) {
	var id int64
	err := row.Scan(&id)
	return id, err
}

// collectRows drains rows, scanning each into a T via scan, and closes rows. It
// is the single place the rows.Next/Scan/rows.Err loop lives. The scan callback
// receives the pgx.Rows as a pgx.Row, matching the row-at-a-time scan helpers.
func collectRows[T any](rows pgx.Rows, scan func(pgx.Row) (T, error)) ([]T, error) {
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// inTx runs fn inside a transaction, rolling back on error (or panic via the
// deferred Rollback, which is a no-op once committed) and committing otherwise.
func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
