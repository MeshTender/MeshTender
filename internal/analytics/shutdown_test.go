package analytics

import (
	"context"
	"testing"
	"time"

	"github.com/MeshTender/MeshTender/internal/config"
	"github.com/MeshTender/MeshTender/internal/store"
	"github.com/MeshTender/MeshTender/internal/testdb"
)

func analyticsMigrate(dsn string) error {
	ctx := context.Background()
	s, err := store.New(ctx, dsn)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Migrate(ctx)
}

// TestRunFlushesQueuedEventsOnShutdown: when Run's context is canceled, it drains
// events still queued in the channel and persists them in the final flush, rather
// than dropping them. This is what lets main.go stop the flusher after the HTTP
// drain without losing the events recorded during that window.
func TestRunFlushesQueuedEventsOnShutdown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.New(ctx, testdb.Fresh(t, analyticsMigrate))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer st.Close()

	rec := New(st, &config.Config{PrimaryHost: "app.x", RootHost: "x", AuthHost: "auth.x", WWWHost: "www.x"})

	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		rec.Run(runCtx)
	}()

	// Queue events (below flushBatch, so nothing is written until shutdown), then
	// cancel. The final drain+flush must persist all of them.
	const n = 5
	for i := 0; i < n; i++ {
		rec.ch <- store.AnalyticsEvent{
			Ts: time.Now(), Surface: "app", Host: "app.x", Path: "/dashboard",
			Method: "GET", Status: 200, Visitor: "v",
		}
	}
	cancel()
	<-done

	var got int
	if err := st.Pool().QueryRow(ctx, `SELECT count(*) FROM analytics_events`).Scan(&got); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if got != n {
		t.Fatalf("persisted %d events on shutdown, want %d (queued events were dropped)", got, n)
	}
}
