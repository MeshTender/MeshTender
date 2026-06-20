package mesh

import (
	"context"
	"sync"
	"time"
)

// RateLimiter paces transmissions within a session so we don't flood the shared
// LoRa mesh. Each Wait reserves the next available slot spaced at least
// `interval` apart and blocks until that slot arrives (or ctx is cancelled).
//
// It is a slot-reservation limiter rather than a leaky check: concurrent or
// back-to-back callers queue up in order, each waiting for its own slot, so a
// burst of N sends is spread over N*interval rather than all firing at once.
type RateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

// NewRateLimiter returns a limiter allowing one send per interval.
func NewRateLimiter(interval time.Duration) *RateLimiter {
	return &RateLimiter{interval: interval}
}

// Wait blocks until this caller's send slot is reached, or returns ctx.Err() if
// the context is cancelled first.
func (l *RateLimiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	at := l.next
	now := time.Now()
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.interval)
	l.mu.Unlock()

	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
