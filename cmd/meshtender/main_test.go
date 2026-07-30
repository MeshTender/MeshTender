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

// TestPruneAuthCodesSweepsImmediately: a restart must clean up without waiting out
// a full interval, so the loop sweeps once on entry. The interval here is long
// enough that a tick can't be what we observed.
func TestPruneAuthCodesSweepsImmediately(t *testing.T) {
	t.Parallel()
	f := newFakePruner(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pruneAuthCodes(ctx, f, time.Hour, discardLogger())

	waitForCalls(t, f, 1, 2*time.Second)
}

// TestPruneAuthCodesRepeats: the ticker keeps firing, so cleanup is ongoing rather
// than startup-only.
func TestPruneAuthCodesRepeats(t *testing.T) {
	t.Parallel()
	f := newFakePruner(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pruneAuthCodes(ctx, f, 2*time.Millisecond, discardLogger())

	// One immediate sweep plus several ticks.
	waitForCalls(t, f, 4, 2*time.Second)
}

// TestPruneAuthCodesStopsOnCancel is the one that matters for shutdown: main waits
// on <-janitorDone, so a loop that ignored ctx would hang the process forever
// instead of exiting.
func TestPruneAuthCodesStopsOnCancel(t *testing.T) {
	t.Parallel()
	f := newFakePruner(nil)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		pruneAuthCodes(ctx, f, time.Millisecond, discardLogger())
	}()

	waitForCalls(t, f, 1, 2*time.Second) // running
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pruneAuthCodes did not return after ctx was cancelled — shutdown would hang")
	}
}

// TestPruneAuthCodesSurvivesErrors: a transient DB failure must not kill the
// janitor for the life of the process; it logs and tries again next tick.
func TestPruneAuthCodesSurvivesErrors(t *testing.T) {
	t.Parallel()
	f := newFakePruner(errors.New("connection refused"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go pruneAuthCodes(ctx, f, 2*time.Millisecond, discardLogger())

	// Still sweeping well after the first failure.
	waitForCalls(t, f, 4, 2*time.Second)
}

// TestAuthCodePruneIntervalOutlivesCodeTTL: the sweep only has to keep the table
// small, but an interval shorter than the code TTL would mean pointless work, and a
// wildly long one would defeat the purpose. Pin it to a sane band so a future edit
// has to think about it.
func TestAuthCodePruneIntervalOutlivesCodeTTL(t *testing.T) {
	t.Parallel()
	if authCodePruneInterval < time.Minute {
		t.Errorf("authCodePruneInterval = %v; shorter than the 60s code TTL means "+
			"sweeping rows that were never expirable", authCodePruneInterval)
	}
	if authCodePruneInterval > time.Hour {
		t.Errorf("authCodePruneInterval = %v; too long to keep the table small", authCodePruneInterval)
	}
}
