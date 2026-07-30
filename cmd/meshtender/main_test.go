package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

// fakePruner records each sweep on a channel, so assertions never race with the
// janitor goroutine. err, when set, is returned every call.
type fakePruner struct {
	calls chan context.Context
	err   error
}

func newFakePruner(err error) *fakePruner {
	return &fakePruner{calls: make(chan context.Context, 64), err: err}
}

func (f *fakePruner) PruneAuthCodes(ctx context.Context) (int64, error) {
	select {
	case f.calls <- ctx:
	default: // never block the loop under test
	}
	if f.err != nil {
		return 0, f.err
	}
	return 1, nil
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// waitForCalls waits for at least n sweeps, failing if they don't arrive.
func waitForCalls(t *testing.T, f *fakePruner, n int, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for i := 0; i < n; i++ {
		select {
		case <-f.calls:
		case <-deadline:
			t.Fatalf("only %d of %d expected sweeps happened within %v", i, n, within)
		}
	}
}

// TestJanitorSweepsImmediately: a restart must clean up without waiting out
// a full interval, so the loop sweeps once on entry. The interval here is long
// enough that a tick can't be what we observed.
func TestJanitorSweepsImmediately(t *testing.T) {
	t.Parallel()
	f := newFakePruner(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runJanitor(ctx, time.Hour, discardLogger(), janitorSweep{"test", f.PruneAuthCodes})

	waitForCalls(t, f, 1, 2*time.Second)
}

// TestJanitorRepeats: the ticker keeps firing, so cleanup is ongoing rather
// than startup-only.
func TestJanitorRepeats(t *testing.T) {
	t.Parallel()
	f := newFakePruner(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runJanitor(ctx, 2*time.Millisecond, discardLogger(), janitorSweep{"test", f.PruneAuthCodes})

	// One immediate sweep plus several ticks.
	waitForCalls(t, f, 4, 2*time.Second)
}

// TestJanitorStopsOnCancel is the one that matters for shutdown: main waits
// on <-janitorDone, so a loop that ignored ctx would hang the process forever
// instead of exiting.
func TestJanitorStopsOnCancel(t *testing.T) {
	t.Parallel()
	f := newFakePruner(nil)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		runJanitor(ctx, time.Millisecond, discardLogger(), janitorSweep{"test", f.PruneAuthCodes})
	}()

	waitForCalls(t, f, 1, 2*time.Second) // running
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runJanitor did not return after ctx was cancelled — shutdown would hang")
	}
}

// TestJanitorSurvivesErrors: a transient DB failure must not kill the
// janitor for the life of the process; it logs and tries again next tick.
func TestJanitorSurvivesErrors(t *testing.T) {
	t.Parallel()
	f := newFakePruner(errors.New("connection refused"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go runJanitor(ctx, 2*time.Millisecond, discardLogger(), janitorSweep{"test", f.PruneAuthCodes})

	// Still sweeping well after the first failure.
	waitForCalls(t, f, 4, 2*time.Second)
}

// TestJanitorRunsEverySweep: the janitor drives several cleanups, so each one must
// run on every pass — and a sweep that fails must not stop the sweeps after it, or a
// single sick table would silently stall unrelated cleanup for the whole process
// lifetime.
func TestJanitorRunsEverySweep(t *testing.T) {
	t.Parallel()
	broken := newFakePruner(errors.New("relation does not exist"))
	healthy := newFakePruner(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Broken first, so the healthy one only runs if a failure doesn't abort the pass.
	go runJanitor(ctx, 2*time.Millisecond, discardLogger(),
		janitorSweep{"broken", broken.PruneAuthCodes},
		janitorSweep{"healthy", healthy.PruneAuthCodes},
	)

	waitForCalls(t, broken, 2, 2*time.Second)
	waitForCalls(t, healthy, 2, 2*time.Second)
}

// TestJanitorIntervalOutlivesCodeTTL: the sweep only has to keep the table
// small, but an interval shorter than the code TTL would mean pointless work, and a
// wildly long one would defeat the purpose. Pin it to a sane band so a future edit
// has to think about it.
func TestJanitorIntervalOutlivesCodeTTL(t *testing.T) {
	t.Parallel()
	if janitorInterval < time.Minute {
		t.Errorf("janitorInterval = %v; shorter than the 60s code TTL means "+
			"sweeping rows that were never expirable", janitorInterval)
	}
	if janitorInterval > time.Hour {
		t.Errorf("janitorInterval = %v; too long to keep the table small", janitorInterval)
	}
}
