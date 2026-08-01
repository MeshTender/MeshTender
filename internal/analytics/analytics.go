// Package analytics is a lightweight, first-party request tracker: a middleware
// records each (non-asset) request onto a buffered channel, a background goroutine
// batch-writes them to the raw events table, and a ticker rolls them up into the
// daily aggregate tables the admin screen reads. No third-party services and no
// PII: visitors are counted via a daily-rotating salted hash of IP+User-Agent.
package analytics

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

const (
	bufferSize  = 2048            // dropped (not blocked) when full
	flushEvery  = 2 * time.Second // max latency before a batch is written
	flushBatch  = 200             // write early once this many are queued
	rollupEvery = 5 * time.Minute // aggregate-table refresh cadence
)

// RetentionDays is how long raw request events are kept before the sweep deletes
// them. Exported because the privacy page states this window and a test binds the
// two together — the published figure must not drift from the one enforced here.
const RetentionDays = 90

// Recorder buffers request events and persists them in the background.
type Recorder struct {
	st   *store.Store
	cfg  *config.Config
	salt []byte
	ch   chan store.AnalyticsEvent
}

// New builds a Recorder. The visitor-hash salt is derived from the server master
// key so it's stable across restarts (no double-counting) but not guessable.
func New(st *store.Store, cfg *config.Config) *Recorder {
	salt := append([]byte("meshtender-analytics|"), cfg.MasterKey[:]...)
	return &Recorder{st: st, cfg: cfg, salt: salt, ch: make(chan store.AnalyticsEvent, bufferSize)}
}

// Run owns the write + rollup loops until ctx is cancelled. Start it in a
// goroutine. A nil Recorder's Run is a no-op.
func (rec *Recorder) Run(ctx context.Context) {
	if rec == nil {
		return
	}
	flush := time.NewTicker(flushEvery)
	defer flush.Stop()
	rollup := time.NewTicker(rollupEvery)
	defer rollup.Stop()
	rec.rollup(ctx) // prime the aggregate tables at startup

	var batch []store.AnalyticsEvent
	write := func(c context.Context) {
		if len(batch) == 0 {
			return
		}
		if err := rec.st.InsertAnalyticsEvents(c, batch); err != nil {
			slog.Error("analytics: insert events", "count", len(batch), "err", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case <-ctx.Done():
			// Drain anything still queued (e.g. events recorded while in-flight
			// requests were draining) into the batch, then final-flush on a fresh
			// context since ctx is already cancelled.
			for drained := true; drained; {
				select {
				case e := <-rec.ch:
					batch = append(batch, e)
				default:
					drained = false
				}
			}
			fc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			write(fc)
			cancel()
			return
		case e := <-rec.ch:
			batch = append(batch, e)
			if len(batch) >= flushBatch {
				write(ctx)
			}
		case <-flush.C:
			write(ctx)
		case <-rollup.C:
			write(ctx)
			rec.rollup(ctx)
		}
	}
}

func (rec *Recorder) rollup(ctx context.Context) {
	if err := rec.st.RollupAnalytics(ctx); err != nil {
		slog.Error("analytics: rollup", "err", err)
	}
	if err := rec.st.PruneAnalytics(ctx, RetentionDays); err != nil {
		slog.Error("analytics: prune", "err", err)
	}
}

// Handler wraps a handler, recording each request that isn't filtered out. A nil
// Recorder returns next unchanged.
func (rec *Recorder) Handler(next http.Handler) http.Handler {
	if rec == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if skip(r) {
			next.ServeHTTP(w, r)
			return
		}
		sw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		rec.record(r, sw.status)
	})
}

// record builds an event and enqueues it without blocking the request — if the
// buffer is full the event is dropped rather than slowing the response.
func (rec *Recorder) record(r *http.Request, status int) {
	host := web.HostWithoutPort(r.Host)
	ev := store.AnalyticsEvent{
		Ts:      time.Now(),
		Surface: rec.surface(host),
		Host:    host,
		Path:    web.RedactPath(r.URL.Path),
		Method:  r.Method,
		Status:  status,
		Visitor: rec.visitor(r),
	}
	select {
	case rec.ch <- ev:
	default: // buffer full — drop
	}
}

// surface classifies a request host into one of the known surfaces.
func (rec *Recorder) surface(host string) string {
	switch {
	case strings.EqualFold(host, rec.cfg.AuthHost):
		return "auth"
	case strings.EqualFold(host, rec.cfg.RootHost),
		rec.cfg.WWWHost != "" && strings.EqualFold(host, rec.cfg.WWWHost):
		return "root"
	case strings.EqualFold(host, rec.cfg.PrimaryHost):
		return "app"
	default:
		return "custom"
	}
}

// visitor is a daily-rotating, non-reversible hash of the client IP + User-Agent.
// The date in the input rotates it every day; no IP is ever stored.
func (rec *Recorder) visitor(r *http.Request) string {
	ip := web.ClientIPFrom(web.RawRemoteAddr(r),
		r.Header.Get("X-Forwarded-For"), r.Header.Get("X-Real-IP"), rec.cfg.TrustedProxies)
	day := time.Now().UTC().Format("2006-01-02")
	h := sha256.New()
	h.Write(rec.salt)
	h.Write([]byte("|" + day + "|" + ip + "|" + r.UserAgent()))
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// skip drops health checks, static assets, websockets, preflight/HEAD, and bots —
// "people visiting", not infrastructure noise.
func skip(r *http.Request) bool {
	if r.Method == http.MethodOptions || r.Method == http.MethodHead {
		return true
	}
	p := r.URL.Path
	switch {
	case p == "/healthz",
		p == "/favicon.svg", p == "/favicon.ico",
		// Browser-generated violation reports are infrastructure, not a visit —
		// and counting them would let one noisy extension inflate the traffic
		// figures.
		p == web.CSPReportPath,
		strings.HasPrefix(p, "/static/"),
		strings.HasSuffix(p, "/ws"):
		return true
	}
	return isBot(r.UserAgent())
}

func isBot(ua string) bool {
	ua = strings.ToLower(ua)
	if ua == "" {
		return true
	}
	for _, s := range []string{"bot", "crawl", "spider", "slurp", "headless", "preview", "monitor"} {
		if strings.Contains(ua, s) {
			return true
		}
	}
	return false
}

// statusRecorder captures the response status while passing through the optional
// Flusher/Hijacker interfaces the underlying writer may implement.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := s.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}
