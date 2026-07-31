package web

import (
	"testing"
	"time"
)

func TestRateLimiterBurstThenThrottle(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	l := NewRateLimiter(3, time.Second)
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

// TestKeyLimiterAllowIsIndependentOfIP: the exported Allow drives limits whose subject
// isn't the connection — password reset is keyed on the submitted identifier, so one
// address can't be mailed repeatedly just by arriving from a different IP each time.
// Buckets must therefore be per-key and unrelated to any request.
func TestKeyLimiterAllowIsIndependentOfIP(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	var l KeyLimiter = func() *rateLimiter {
		rl := NewRateLimiter(2, 10*time.Minute)
		rl.now = func() time.Time { return now }
		return rl
	}()

	for i := range 2 {
		if !l.Allow("victim@example.test") {
			t.Fatalf("request %d should be allowed within the burst", i+1)
		}
	}
	// Third request for the same identifier is refused no matter who is asking.
	if l.Allow("victim@example.test") {
		t.Error("a third request for the same identifier was allowed")
	}
	// A different identifier is unaffected.
	if !l.Allow("someone-else@example.test") {
		t.Error("a distinct identifier shares another's bucket")
	}
	// Refill is slow by design: one more only after the interval.
	now = now.Add(10 * time.Minute)
	if !l.Allow("victim@example.test") {
		t.Error("no token refilled after the interval")
	}
	if l.Allow("victim@example.test") {
		t.Error("more than one token refilled")
	}
}

func TestRateLimiterSweepReclaims(t *testing.T) {
	t.Parallel()
	now := time.Unix(0, 0)
	l := NewRateLimiter(2, time.Second)
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
