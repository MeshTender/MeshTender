package web

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// rawAddrCtxKey keys the connection's original RemoteAddr in the request context.
type rawAddrCtxKey struct{}

// CaptureRemoteAddr stashes the connection's RemoteAddr before chi's RealIP
// middleware rewrites it from X-Forwarded-For/X-Real-IP. This preserves the true
// TCP peer so the proxy-diagnostics page can show it next to the header-derived
// client IP. Must run before RealIP.
func CaptureRemoteAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), rawAddrCtxKey{}, r.RemoteAddr)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RawRemoteAddr returns the connection's original RemoteAddr (the TCP peer),
// captured by CaptureRemoteAddr before RealIP rewrote it. Falls back to the
// current RemoteAddr if the middleware wasn't installed.
func RawRemoteAddr(r *http.Request) string {
	if v, ok := r.Context().Value(rawAddrCtxKey{}).(string); ok {
		return v
	}
	return r.RemoteAddr
}

// rateLimiter is a per-key token-bucket limiter, safe for concurrent use. It
// throttles abusive bursts (e.g. password guessing) using only in-process
// state. Each key (a client IP) gets a bucket that starts full and refills at a
// steady rate; a request costs one token.
type rateLimiter struct {
	mu         sync.Mutex
	buckets    map[string]*tokenBucket
	ratePerSec float64 // tokens replenished per second
	burst      float64 // bucket capacity (max immediate requests)
	now        func() time.Time
	lastSweep  time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// newRateLimiter builds a limiter allowing bursts up to burst requests, then
// one further request every refill interval.
func NewRateLimiter(burst float64, refill time.Duration) *rateLimiter {
	return &rateLimiter{
		buckets:    map[string]*tokenBucket{},
		ratePerSec: 1 / refill.Seconds(),
		burst:      burst,
		now:        time.Now,
	}
}

// allow reports whether a request for key may proceed, consuming a token.
func (l *rateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		// First request from this key: start full, spend one token.
		l.buckets[key] = &tokenBucket{tokens: l.burst - 1, last: now}
		return true
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.ratePerSec)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops fully-recovered buckets so memory stays bounded by the number of
// recently-active keys. A bucket back at capacity is indistinguishable from a
// fresh one, so removing it is safe. Runs at most once per minute.
func (l *rateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for key, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.ratePerSec >= l.burst {
			delete(l.buckets, key)
		}
	}
}

// KeyLimiter throttles by a caller-chosen key rather than by client IP. It exists
// for limits whose natural subject isn't the connection — password reset is keyed on
// the identifier submitted, so one address can't be mailed repeatedly from a fresh IP
// each time.
type KeyLimiter interface {
	Allow(key string) bool
}

// Allow reports whether an action for key may proceed, consuming a token. Use it for
// limits keyed on something other than the client IP (see KeyLimiter); Middleware
// remains the per-IP path.
func (l *rateLimiter) Allow(key string) bool { return l.allow(key) }

// middleware rejects requests from a client that has exceeded its rate, keyed by
// client IP.
func (l *rateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			http.Error(w, "Too many attempts. Please wait a moment and try again.", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the request's client IP without the port. The RealIP
// middleware has already resolved X-Forwarded-For / X-Real-IP into RemoteAddr.
func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// ClientIP exposes clientIP for handlers that record the caller's address
// (e.g. the username-change audit trail).
func ClientIP(r *http.Request) string { return clientIP(r) }
