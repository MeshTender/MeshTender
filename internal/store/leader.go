package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionLock is a held Postgres session-level advisory lock, used to elect one
// replica to run a singleton worker. A session lock belongs to the connection
// that took it, so the connection is pinned out of the pool until Release:
// running the unlock (or the liveness check) on another pooled connection would
// silently do nothing.
type SessionLock struct {
	conn *pgxpool.Conn
	key  int64
}

// TryAcquireSessionLock takes advisory lock key without waiting. It returns
// nil, nil when another session holds it.
func (s *Store) TryAcquireSessionLock(ctx context.Context, key int64) (*SessionLock, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire lock conn: %w", err)
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&got); err != nil {
		conn.Release()
		return nil, fmt.Errorf("try advisory lock: %w", err)
	}
	if !got {
		conn.Release()
		return nil, nil
	}
	return &SessionLock{conn: conn, key: key}, nil
}

// Alive checks the lock's connection is still up. If it isn't, the server has
// already dropped the lock and another replica may hold it: the caller must
// stop acting as leader.
func (l *SessionLock) Alive(ctx context.Context) error {
	if _, err := l.conn.Exec(ctx, `SELECT 1`); err != nil {
		return fmt.Errorf("lock connection lost: %w", err)
	}
	return nil
}

// Release unlocks and returns the connection to the pool. A broken connection
// is discarded by the pool, which also drops the lock server-side.
func (l *SessionLock) Release(ctx context.Context) {
	_, _ = l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, l.key)
	l.conn.Release()
}
