// Command meshtender runs the MeshTender web server.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jleight/meshtender/internal/analytics"
	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/core"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/mail"
	"github.com/jleight/meshtender/internal/seed"
	"github.com/jleight/meshtender/internal/store"
	"github.com/jleight/meshtender/internal/web"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger) // so package-level slog (e.g. the renderer) uses this handler
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	var reset bool
	flag.BoolVar(&reset, "reset", false,
		"truncate all data except users, passkeys, sessions, and the server identity, then exit")
	var runSeed bool
	flag.BoolVar(&runSeed, "seed", false,
		"populate the database with realistic fake data for local testing (additive), then exit")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	st, err := store.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	// Migrate logs each migration it actually applies, so this is just the "schema is
	// ready" checkpoint — saying "applied" here read as though work happened on every
	// boot, when the steady state is that none does.
	logger.Info("database schema ready")

	if reset {
		removed, err := st.Reset(ctx)
		if err != nil {
			return err
		}
		logger.Info("database reset — kept users with credentials, passkeys, sessions, and the server identity",
			"credential_less_users_removed", removed)
		return nil
	}

	if runSeed {
		if err := seed.Run(ctx, st, logger); err != nil {
			return err
		}
		logger.Info("database seeded")
		return nil
	}

	idSvc, err := identity.LoadOrCreate(ctx, st, cfg.MasterKey)
	if err != nil {
		return err
	}
	logger.Info("server identity ready", "pubkey", idSvc.PublicKeyHex())

	// Outbound mail is optional. Without a provider the server logs messages
	// instead of sending them, so account recovery is fully walkable in dev, and
	// cfg.MailEnabled keeps the UI from offering mail that would never arrive.
	var sender mail.Sender
	if cfg.MailEnabled {
		sender = mail.NewResend(cfg.ResendAPIKey, cfg.MailFrom, cfg.MailReplyTo)
		logger.Info("mail provider ready", "from", cfg.MailFrom)
	} else {
		sender = &mail.LogSender{Logger: logger}
		logger.Warn("no mail provider configured — recovery messages will be logged, not sent")
	}

	authSvc, err := auth.New(st, st.Pool(), auth.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     cfg.RPOrigins,
		AppHost:       cfg.PrimaryHost,
		AuthHost:      cfg.AuthHost,
		RootHost:      cfg.RootHost,
		Secure:        cfg.Secure,
		Mail:          sender,
		MailEnabled:   cfg.MailEnabled,
	})
	if err != nil {
		return err
	}

	srv, err := core.NewServer(st, authSvc, idSvc, cfg)
	if err != nil {
		return err
	}

	// First-party traffic analytics: wrap the whole dispatcher so every host is
	// captured, with a background goroutine doing the writes + rollups.
	rec := analytics.New(st, cfg)

	// Background workers run on their own context — NOT the signal context — so they
	// keep working while in-flight requests drain during shutdown; we stop and drain
	// them afterwards, before the pool closes. The analytics flusher needs this
	// because draining requests still record events; the janitor just has no reason
	// to stop early.
	bgCtx, stopBackground := context.WithCancel(context.Background())
	analyticsDone := make(chan struct{})
	go func() {
		defer close(analyticsDone)
		rec.Run(bgCtx)
	}()
	// CSP violation reports are written by their own background flusher, for the same
	// reason as analytics: the report endpoint is public, so a report storm must not
	// add request latency.
	cspDone := make(chan struct{})
	go func() {
		defer close(cspDone)
		srv.CollectCSPReports(bgCtx)
	}()
	janitorDone := make(chan struct{})
	go func() {
		defer close(janitorDone)
		runJanitor(bgCtx, janitorInterval, logger,
			janitorSweep{"auth codes", st.PruneAuthCodes},
			janitorSweep{"share links", st.PruneInvites},
			// A violation that stops recurring eventually ages out, so a fixed
			// problem leaves the admin page on its own.
			janitorSweep{"csp reports", func(ctx context.Context) (int64, error) {
				return st.PruneCSPReports(ctx, web.CSPRetention)
			}},
		)
	}()

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           rec.Handler(srv.Handler()),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	tls := cfg.TLSCert != "" && cfg.TLSKey != ""
	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr, "tls", tls)
		var err error
		if tls {
			err = httpSrv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			err = httpSrv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	var srvErr error
	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case srvErr = <-errCh:
		// Server failed to listen/serve; fall through to orderly teardown.
	}

	// Drain in-flight HTTP requests first — they may still record analytics events,
	// so the flusher must stay alive through the drain.
	if srvErr == nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		srvErr = httpSrv.Shutdown(shutdownCtx)
		cancel()
	}

	// Shutdown doesn't close hijacked/WebSocket connections, so close the active
	// console/confirm sockets and give their handlers time to unwind and stamp
	// their session's ended_at (see core.WSDrainTimeout) before we stop the flusher
	// and close the pool. Too short and drained sessions stay "in progress" forever.
	wsCtx, wsCancel := context.WithTimeout(context.Background(), core.WSDrainTimeout)
	if !srv.DrainWebSockets(wsCtx) {
		logger.Warn("shutdown: some WebSocket handlers did not finish before the deadline")
	}
	wsCancel()

	// Now stop the background workers and wait for them — the flusher's final flush
	// especially — before the deferred st.Close() closes the pool underneath them.
	stopBackground()
	<-analyticsDone
	<-cspDone
	<-janitorDone
	return srvErr
}

// janitorInterval is how often the expired-row sweeps run. It's set by the
// shortest-lived thing they clean — a handoff code (authCodeTTL, 60s) — so this
// bounds a dead code's lifetime at a few minutes, and it matches the cadence scs
// uses for its own session-table cleanup.
//
// Share links are collected on a completely different timescale: they're kept for
// store.ExpiredInviteGrace (30 days) past expiry so their owner can see and remove
// them, so nearly every sweep finds nothing to do. That's fine — the delete is one
// indexed statement, which isn't worth a second ticker to avoid.
const janitorInterval = 5 * time.Minute

// Connection timeouts. With all of these at zero (net/http's default) a slow or idle
// peer can hold a connection open indefinitely after sending headers.
//
//   - readHeaderTimeout guards the classic slowloris: headers dribbled out forever.
//   - readTimeout covers the body too. limitBody caps the SIZE at 1 MiB, but with no
//     deadline a client could still take days to deliver it.
//   - writeTimeout is deliberately generous. It has to cover a genuine download over a
//     bad link — this product's users are on rural, marginal connections, and the
//     largest asset is a few hundred KB — while still bounding a peer that stops
//     reading mid-response.
//   - idleTimeout reaps keep-alive connections between requests.
//
// The console WebSocket is unaffected. These become deadlines on the underlying
// connection, which looks like it should sever an upgraded socket — but net/http clears
// the deadline when a handler hijacks the connection (hijackLocked calls
// rwc.SetDeadline(time.Time{})), so the socket inherits nothing and is bounded instead
// by consoleIdleTimeout and the shutdown drain. That's the one thing these timeouts
// could plausibly have broken, so
// core.TestConsoleWebSocketOutlivesServerReadTimeout pins it.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 120 * time.Second
	idleTimeout       = 120 * time.Second
)

// janitorSweep is one periodic cleanup job: a name for the log, and the delete to
// run. Taking a closure rather than an interface keeps the janitor decoupled from
// the store (a store method value satisfies it directly) and lets the loop be
// exercised without a database.
type janitorSweep struct {
	name  string
	prune func(ctx context.Context) (int64, error)
}

// runJanitor runs every sweep once on entry — so a restart cleans up immediately
// instead of waiting out the first interval — then again every `every` until ctx is
// cancelled.
//
// This lives here rather than on the analytics rollup ticker, which also prunes on
// the same cadence, because analytics has no business knowing about auth codes or
// share links. A transient failure is logged and retried on the next tick, and one
// failing sweep doesn't stop the others; a failure caused by shutdown cancelling ctx
// is not an error worth reporting.
func runJanitor(ctx context.Context, every time.Duration, logger *slog.Logger, sweeps ...janitorSweep) {
	runAll := func() {
		for _, s := range sweeps {
			n, err := s.prune(ctx)
			switch {
			case ctx.Err() != nil:
				return // shutting down; not a real failure
			case err != nil:
				logger.Error("janitor sweep failed", "sweep", s.name, "err", err)
			case n > 0:
				logger.Info("janitor removed expired rows", "sweep", s.name, "count", n)
			}
		}
	}
	runAll()

	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			runAll()
		}
	}
}
