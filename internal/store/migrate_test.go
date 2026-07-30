package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pressly/goose/v3/lock"

	"github.com/jleight/meshtender/internal/testdb"
)

// emptySchema drops everything in the database behind st, making it genuinely
// unmigrated.
//
// This is necessary because testdb.Fresh clones a shared template and runs its migrate
// callback only on the FIRST call in a process — so passing a no-op callback does not get
// you an empty database once any other test has built the template. Without emptying it,
// both tests below "migrate" an already-migrated database, which proves nothing:
// Provider.Up returns early when nothing is pending and never touches the lock. (That is
// exactly why an earlier version of these tests passed with the locker removed.)
func emptySchema(t *testing.T, st *Store) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatalf("empty schema: %v", err)
	}
	// Guard the guard: if tables remain, the assertions below are meaningless.
	var tables int
	if err := st.pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables WHERE table_schema = 'public'`).Scan(&tables); err != nil {
		t.Fatalf("verify empty schema: %v", err)
	}
	if tables != 0 {
		t.Fatalf("database still has %d table(s) after emptying it", tables)
	}
}

// TestMigrateIsIdempotent: every replica calls Migrate on every boot, so running it
// against an already-migrated database must be a silent no-op rather than an error.
func TestMigrateIsIdempotent(t *testing.T) {
	t.Parallel()
	st, ctx := orgTestStore(t) // already migrated by the harness
	for i := 0; i < 3; i++ {
		if err := st.Migrate(ctx); err != nil {
			t.Fatalf("Migrate on an up-to-date database (attempt %d): %v", i+1, err)
		}
	}
}

// TestMigrateConcurrentlyFromManyConnections runs several independent Stores (standing in
// for replicas) against one BRAND-NEW database at once, and checks they all succeed with
// the schema applied exactly once.
//
// Honest scope: this does NOT prove the session lock works — it passes with the locker
// removed, even against a genuinely empty database. goose's Provider is resilient on its
// own (each migration runs in a transaction, and version-table creation is retried), so
// four replicas over fast migrations don't reliably produce an observable failure. The
// lock earns its place against the harder cases this can't stage: long-running
// migrations, more replicas, and a contended version table.
//
// TestMigrateWaitsForTheAdvisoryLock is the deterministic proof. This one is the safety
// net for Migrate becoming outright unsafe to call concurrently — duplicate version rows,
// an error, or a half-applied schema.
func TestMigrateConcurrentlyFromManyConnections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	dsn := testdb.Fresh(t, func(string) error { return nil })

	const replicas = 4
	stores := make([]*Store, replicas)
	for i := range stores {
		st, err := New(ctx, dsn)
		if err != nil {
			t.Fatalf("store %d: %v", i, err)
		}
		t.Cleanup(st.Close)
		stores[i] = st
	}
	emptySchema(t, stores[0])

	// Release them together so the calls genuinely overlap.
	var start sync.WaitGroup
	start.Add(1)
	errs := make([]error, replicas)
	var done sync.WaitGroup
	for i, st := range stores {
		done.Add(1)
		go func(i int, st *Store) {
			defer done.Done()
			start.Wait()
			errs[i] = st.Migrate(ctx)
		}(i, st)
	}
	start.Done()
	done.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("replica %d failed to migrate concurrently: %v", i, err)
		}
	}

	// The schema must be complete and applied exactly once. A double-apply would have
	// errored above; this checks the end state is actually usable.
	st := stores[0]
	var applied int
	if err := st.pool.QueryRow(ctx, `SELECT count(*) FROM goose_db_version`).Scan(&applied); err != nil {
		t.Fatalf("read goose version table: %v", err)
	}
	if applied < 2 { // the zero row plus at least one migration
		t.Errorf("goose_db_version has %d rows; migrations don't appear to have run", applied)
	}
	// Duplicate version rows are the signature of a double-apply.
	var dupes int
	if err := st.pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT version_id FROM goose_db_version GROUP BY version_id HAVING count(*) > 1
		) d`).Scan(&dupes); err != nil {
		t.Fatalf("check for duplicate versions: %v", err)
	}
	if dupes != 0 {
		t.Errorf("%d migration version(s) recorded more than once — concurrent runs double-applied", dupes)
	}
	// And the schema is genuinely there: a table from the last migration is queryable.
	if _, err := st.pool.Exec(ctx, `SELECT 1 FROM repeater_invites WHERE false`); err != nil {
		t.Errorf("schema incomplete after concurrent migration: %v", err)
	}
}

// TestMigrateWaitsForTheAdvisoryLock is the deterministic proof for audit P3.
//
// Rather than trying to win a race, it takes goose's advisory lock FIRST from a connection
// of its own, then calls Migrate with a short deadline. If Migrate honours the lock it
// blocks and the deadline expires; if it ignores it, the migrations run and Migrate
// returns nil. Releasing the lock and retrying then has to succeed, so this pins both
// halves: it waits, and it isn't permanently wedged.
//
// The lock is session-scoped, which is why this pins a single *sql.Conn — the same reason
// goose's own locker does, and the reason a hand-rolled pg_advisory_lock over a pool would
// have protected nothing.
func TestMigrateWaitsForTheAdvisoryLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dsn := testdb.Fresh(t, func(string) error { return nil })

	st, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)
	emptySchema(t, st)

	// Hold the lock goose will ask for, on a dedicated connection.
	holder := st.migrationDB()
	defer func() { _ = holder.Close() }()
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatalf("hold conn: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lock.DefaultLockID); err != nil {
		t.Fatalf("take advisory lock: %v", err)
	}

	blocked, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err = st.Migrate(blocked)
	if err == nil {
		t.Fatal("Migrate completed while another session held goose's advisory lock — " +
			"concurrent replicas are not serialized")
	}
	// It must have been the deadline, not some unrelated failure.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Migrate failed for the wrong reason while blocked: %v", err)
	}
	// No MIGRATION may have been applied while it was waiting. Checking for a table a
	// migration creates, rather than for zero tables: goose creates its own
	// goose_db_version table BEFORE taking the lock (with retries, precisely because
	// HasPending doesn't respect the locker), so its presence here is expected and isn't
	// migration work.
	var migrated bool
	if err := st.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = 'public' AND table_name = 'users'
		)`).Scan(&migrated); err != nil {
		t.Fatalf("check for migrated schema: %v", err)
	}
	if migrated {
		t.Error("migrations were applied while another session held the lock")
	}

	// Release and confirm it isn't wedged.
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lock.DefaultLockID); err != nil {
		t.Fatalf("release advisory lock: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate after the lock was released: %v", err)
	}
}
