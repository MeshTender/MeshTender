// Package testdb gives each test its own throwaway Postgres database.
//
// The model (borrowed from a pattern common in .NET test suites): once per test
// process we obtain a Postgres server, build a single migrated "template"
// database, and then hand every test a fresh database cloned from that template
// via CREATE DATABASE ... TEMPLATE. Cloning copies the already-migrated schema
// and seed data, so individual tests never run migrations and never share
// state — no TRUNCATE, no ordering constraints, safe to run in parallel.
//
// The server comes from one of two places:
//
//   - If MESHTENDER_TEST_DATABASE_URL is set, that server is reused (this is how
//     CI points at its postgres service container).
//   - Otherwise a postgres:17 container is started via testcontainers, so a
//     local `go test ./...` just works as long as Docker is running.
//
// This package deliberately does not import internal/store: store's own
// (internal) tests import testdb, and the reverse dependency would be a cycle.
// Migrations are injected by the caller through the migrate callback.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// pid disambiguates database names across the separate test binaries (one per
// package) that may share a single reused server in CI.
var pid = os.Getpid()

var (
	serverOnce sync.Once
	serverErr  error
	adminDSN   string                      // points at a maintenance DB on the server
	container  *postgres.PostgresContainer // nil when reusing an external server

	templateOnce sync.Once
	templateErr  error
	templateName string
	// templateMigrate records which migrate callback built the template, so a second
	// caller passing a different one is reported instead of silently ignored. The
	// template is built once per process: without this check, a test that asks for a
	// DIFFERENT schema gets whatever the first caller created, and if it wins the race
	// instead, every other test in the package clones ITS schema. That failure lands
	// nowhere near its cause — an empty template surfaces as "relation ... does not
	// exist" in unrelated tests, only sometimes, because which parallel test calls
	// Fresh first is not deterministic.
	templateMigrate uintptr

	createMu  sync.Mutex // serializes per-test CREATE DATABASE within this process
	dbCounter atomic.Int64
)

// templateLockKey is an arbitrary, fixed advisory-lock id. It serializes
// template creation across the package test binaries that share one server (CI),
// where they would otherwise race to CREATE DATABASE from template1.
const templateLockKey int64 = 0x4d5465737444_42

// ensureServer resolves a Postgres server exactly once per process: a reused
// external one via MESHTENDER_TEST_DATABASE_URL, or a fresh container.
func ensureServer(ctx context.Context) error {
	serverOnce.Do(func() {
		if dsn := os.Getenv("MESHTENDER_TEST_DATABASE_URL"); dsn != "" {
			adminDSN = dsn
			return
		}
		c, err := postgres.Run(ctx, "postgres:17",
			postgres.WithDatabase("postgres"),
			postgres.WithUsername("meshtender"),
			postgres.WithPassword("meshtender"),
			postgres.BasicWaitStrategies(),
		)
		if err != nil {
			serverErr = fmt.Errorf("start postgres container (is Docker running?): %w", err)
			return
		}
		container = c
		dsn, err := c.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			serverErr = fmt.Errorf("container connection string: %w", err)
			return
		}
		adminDSN = dsn
	})
	return serverErr
}

// ensureTemplate creates and migrates the per-process template database exactly
// once. migrate is invoked with the template's DSN and must apply the schema and
// then release all its connections (the subsequent CREATE DATABASE ... TEMPLATE
// requires that no sessions are connected to the template).
func ensureTemplate(ctx context.Context, migrate func(dsn string) error) error {
	templateOnce.Do(func() {
		templateMigrate = reflect.ValueOf(migrate).Pointer()
		templateName = fmt.Sprintf("mt_tmpl_%d", pid)
		conn, err := pgx.Connect(ctx, adminDSN)
		if err != nil {
			templateErr = fmt.Errorf("admin connect: %w", err)
			return
		}
		defer func() { _ = conn.Close(ctx) }()
		// Hold a cross-process advisory lock only around the template DDL: this is
		// the one CREATE DATABASE that copies the shared template1, so concurrent
		// package binaries on one server must take turns. Released before migrate,
		// which runs on its own connection against the new template.
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, templateLockKey); err != nil {
			templateErr = fmt.Errorf("advisory lock: %w", err)
			return
		}
		_, dropErr := conn.Exec(ctx, `DROP DATABASE IF EXISTS `+quoteIdent(templateName)+` WITH (FORCE)`)
		var createErr error
		if dropErr == nil {
			_, createErr = conn.Exec(ctx, `CREATE DATABASE `+quoteIdent(templateName))
		}
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, templateLockKey); err != nil {
			templateErr = fmt.Errorf("advisory unlock: %w", err)
			return
		}
		if dropErr != nil {
			templateErr = fmt.Errorf("drop stale template: %w", dropErr)
			return
		}
		if createErr != nil {
			templateErr = fmt.Errorf("create template: %w", createErr)
			return
		}
		tmplDSN, err := dsnWithDB(adminDSN, templateName)
		if err != nil {
			templateErr = err
			return
		}
		if err := migrate(tmplDSN); err != nil {
			templateErr = fmt.Errorf("migrate template: %w", err)
		}
	})
	if templateErr == nil && reflect.ValueOf(migrate).Pointer() != templateMigrate {
		return fmt.Errorf("the template was already built by a different migrate " +
			"callback. One template is built per process, so every Fresh call in a package must " +
			"pass the same one — otherwise the schema a test gets depends on which test ran " +
			"first. If a test needs a different schema, take the shared template and adjust it " +
			"inside the test")
	}
	return templateErr
}

// Fresh provisions a brand-new database cloned from the migrated template and
// returns its DSN. The database is dropped when the test finishes. migrate is
// used only to build the template the first time it's called in this process;
// later calls reuse the existing template.
func Fresh(t *testing.T, migrate func(dsn string) error) string {
	t.Helper()
	ctx := context.Background()
	if err := ensureServer(ctx); err != nil {
		t.Fatalf("testdb: %v", err)
	}
	if err := ensureTemplate(ctx, migrate); err != nil {
		t.Fatalf("testdb: %v", err)
	}

	name := fmt.Sprintf("mt_test_%d_%d", pid, dbCounter.Add(1))
	createMu.Lock()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err == nil {
		_, err = conn.Exec(ctx, `CREATE DATABASE `+quoteIdent(name)+` TEMPLATE `+quoteIdent(templateName))
		_ = conn.Close(ctx)
	}
	createMu.Unlock()
	if err != nil {
		t.Fatalf("testdb: create database: %v", err)
	}

	t.Cleanup(func() {
		c, err := pgx.Connect(ctx, adminDSN)
		if err != nil {
			return
		}
		defer func() { _ = c.Close(ctx) }()
		_, _ = c.Exec(ctx, `DROP DATABASE IF EXISTS `+quoteIdent(name)+` WITH (FORCE)`)
	})

	dsn, err := dsnWithDB(adminDSN, name)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	return dsn
}

// RunMain wraps a package's tests with process-level teardown: it drops the
// template and, when we started one, terminates the container. Use it from a
// package's TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testdb.RunMain(m)) }
func RunMain(m *testing.M) int {
	code := m.Run()
	ctx := context.Background()
	if templateName != "" && adminDSN != "" {
		if conn, err := pgx.Connect(ctx, adminDSN); err == nil {
			_, _ = conn.Exec(ctx, `DROP DATABASE IF EXISTS `+quoteIdent(templateName)+` WITH (FORCE)`)
			_ = conn.Close(ctx)
		}
	}
	if container != nil {
		_ = container.Terminate(ctx)
	}
	return code
}

// dsnWithDB returns base with its database (path) replaced by dbName and a small
// pool cap applied. The cap matters under parallel tests: many tests each open a
// pool against the same server, so an uncapped default would exhaust Postgres's
// connection limit. These DSNs are only ever handed to pgxpool (store.New), so
// the pool_* parameter is safe here.
func dsnWithDB(base, dbName string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	u.Path = "/" + dbName
	q := u.Query()
	q.Set("pool_max_conns", "4")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// quoteIdent double-quotes a generated identifier. Names here are built from a
// fixed prefix plus integers, so this is just correctness, not injection
// defense.
func quoteIdent(name string) string {
	return `"` + name + `"`
}
