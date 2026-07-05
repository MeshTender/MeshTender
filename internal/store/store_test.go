package store

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jleight/meshtender/internal/testdb"
)

// freshDSN returns an isolated test database DSN, optionally with extra query
// params merged in (e.g. a short statement_timeout).
func freshDSN(t *testing.T, params map[string]string) string {
	t.Helper()
	dsn := testdb.Fresh(t, migrateTemplate)
	if len(params) == 0 {
		return dsn
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// TestNewAppliesPoolConfig: New bounds and configures the pool, respects a
// pool_max_conns from the DSN, and caps statements with the default timeout.
func TestNewAppliesPoolConfig(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := New(ctx, freshDSN(t, nil)) // testdb DSN pins pool_max_conns=4
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	cfg := st.pool.Config()
	if cfg.MaxConns != 4 {
		t.Errorf("MaxConns = %d, want 4 (DSN pool_max_conns respected)", cfg.MaxConns)
	}
	if cfg.MaxConnLifetime != time.Hour {
		t.Errorf("MaxConnLifetime = %v, want 1h", cfg.MaxConnLifetime)
	}
	if cfg.MaxConnIdleTime != 30*time.Minute {
		t.Errorf("MaxConnIdleTime = %v, want 30m", cfg.MaxConnIdleTime)
	}

	var timeout string
	if err := st.pool.QueryRow(ctx, `SHOW statement_timeout`).Scan(&timeout); err != nil {
		t.Fatalf("show statement_timeout: %v", err)
	}
	if timeout != "30s" {
		t.Errorf("statement_timeout = %q, want 30s", timeout)
	}
}

// TestStatementTimeoutEnforced: a query exceeding the pool's statement_timeout is
// aborted, so one slow query can't hold a connection indefinitely.
func TestStatementTimeoutEnforced(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := New(ctx, freshDSN(t, map[string]string{"statement_timeout": "200"})) // 200ms
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	_, err = st.pool.Exec(ctx, `SELECT pg_sleep(2)`)
	if err == nil {
		t.Fatal("pg_sleep(2) succeeded under a 200ms statement_timeout, want it aborted")
	}
	if !strings.Contains(err.Error(), "statement timeout") {
		t.Fatalf("err = %v, want a statement timeout cancellation", err)
	}
}

// TestMigrationDBUncapped: migrations run on a connection with no
// statement_timeout even when the app pool caps it short, so long DDL isn't
// killed. This is the safety property behind running migrations off the pool.
func TestMigrationDBUncapped(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := New(ctx, freshDSN(t, map[string]string{"statement_timeout": "200"})) // pool capped at 200ms
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer st.Close()

	db := st.migrationDB()
	defer func() { _ = db.Close() }()

	var setting string
	if err := db.QueryRowContext(ctx, `SELECT current_setting('statement_timeout')`).Scan(&setting); err != nil {
		t.Fatalf("read statement_timeout: %v", err)
	}
	if setting != "0" {
		t.Errorf("migration statement_timeout = %q, want 0 (uncapped)", setting)
	}
	// And a statement longer than the pool's cap must not be killed here.
	if _, err := db.ExecContext(ctx, `SELECT pg_sleep(0.5)`); err != nil {
		t.Fatalf("pg_sleep(0.5) on migration conn was killed: %v", err)
	}
}
