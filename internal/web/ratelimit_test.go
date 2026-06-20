package web

import (
	"testing"
	"time"
)

func TestRateLimiterBurstThenThrottle(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(3, time.Second)
	l.now = func() time.Time { return now }

	// Burst of 3 is allowed from a cold start.
	for i := 0; i < 3; i++ {
		if !l.allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed within burst", i)
		}
	}
	// 4th in the same instant is denied.
	if l.allow("1.2.3.4") {
		t.Fatal("4th request should be throttled")
	}
	// A different key has its own bucket.
	if !l.allow("5.6.7.8") {
		t.Fatal("distinct key should not be throttled")
	}
	// After the refill interval, one more token is available.
	now = now.Add(time.Second)
	if !l.allow("1.2.3.4") {
		t.Fatal("request should be allowed after refill")
	}
	if l.allow("1.2.3.4") {
		t.Fatal("only one token should have refilled")
	}
}

func TestRateLimiterSweepReclaims(t *testing.T) {
	now := time.Unix(0, 0)
	l := newRateLimiter(2, time.Second)
	l.now = func() time.Time { return now }

	l.allow("1.2.3.4")
	if len(l.buckets) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(l.buckets))
	}
	// Advance well past full recovery + the sweep interval; the next call sweeps
	// the now-recovered bucket before creating a fresh one.
	now = now.Add(2 * time.Minute)
	l.allow("9.9.9.9")
	if _, ok := l.buckets["1.2.3.4"]; ok {
		t.Fatal("recovered bucket should have been swept")
	}
}
