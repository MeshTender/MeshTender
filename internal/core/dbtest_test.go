package core

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/testdb"
)

// coreStore returns a Store backed by a fresh, throwaway database cloned from
// the migrated template (see internal/testdb). Each call is fully isolated —
// command_catalog seeded, everything else empty — so the integration tests need
// no truncation and don't share state.
func coreStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.New(ctx, testdb.Fresh(t, coreMigrate))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(st.Close)
	return st, ctx
}

// coreMigrate applies the schema to the template database, releasing its
// connection before the template is cloned.
func coreMigrate(dsn string) error {
	ctx := context.Background()
	st, err := store.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	return st.Migrate(ctx)
}

// TestMain wires process-level setup/teardown for the testdb template/container.
// It also shortens the packet-reply wait once, before any test runs: the
// integration tests run in parallel and only ever read perTryReply, so setting
// it here (rather than mutating it per-test) keeps it race-free under -race.
func TestMain(m *testing.M) {
	perTryReply = 300 * time.Millisecond
	os.Exit(testdb.RunMain(m))
}
