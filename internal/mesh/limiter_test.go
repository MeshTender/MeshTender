package mesh

import (
	"context"
	"testing"
	"time"
)

func TestRateLimiterSpacesSends(t *testing.T) {
	l := NewRateLimiter(50 * time.Millisecond)
	ctx := context.Background()

	start := time.Now()
	for i := 0; i < 4; i++ { // first is immediate, then 3 spaced waits
		if err := l.Wait(ctx); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}
	// 4 sends at 50ms spacing → ~150ms minimum elapsed.
	if elapsed := time.Since(start); elapsed < 140*time.Millisecond {
		t.Fatalf("4 sends took %v, expected >= ~150ms", elapsed)
	}
}

func TestRateLimiterFirstSendImmediate(t *testing.T) {
	l := NewRateLimiter(time.Second)
	start := time.Now()
	if err := l.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Fatalf("first send waited %v, expected immediate", elapsed)
	}
}

func TestRateLimiterCancelled(t *testing.T) {
	l := NewRateLimiter(time.Hour)
	_ = l.Wait(context.Background()) // consume the immediate slot
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Wait(ctx); err == nil {
		t.Fatal("expected error from canceled context")
	}
}
