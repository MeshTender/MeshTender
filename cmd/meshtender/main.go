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
	"github.com/jleight/meshtender/internal/seed"
	"github.com/jleight/meshtender/internal/store"
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
	logger.Info("migrations applied")

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

	authSvc, err := auth.New(st, st.Pool(), auth.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     cfg.RPOrigins,
		AppHost:       cfg.PrimaryHost,
		AuthHost:      cfg.AuthHost,
		RootHost:      cfg.RootHost,
		Secure:        cfg.Secure,
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
	janitorDone := make(chan struct{})
	go func() {
		defer close(janitorDone)
		pruneAuthCodes(bgCtx, st, authCodePruneInterval, logger)
	}()

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           rec.Handler(srv.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
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
	<-janitorDone
	return srvErr
}

// authCodePruneInterval is how often expired cross-host handoff codes are swept.
// They live for authCodeTTL (60s), so this bounds a dead row's lifetime at a few
// minutes — the table stays small, which is all that's needed. It matches the
// cadence scs uses for its own session-table cleanup.
const authCodePruneInterval = 5 * time.Minute

// authCodePruner is the slice of the store the janitor needs. Declared at the point
// of use so the loop can be exercised without a database.
type authCodePruner interface {
	PruneAuthCodes(ctx context.Context) (int64, error)
}

// pruneAuthCodes deletes expired handoff codes every `every` until ctx is cancelled.
// ConsumeAuthCode only removes codes that are actually redeemed, so abandoned ones
// (a sign-in whose tab was closed mid-handoff) would otherwise accumulate forever
// holding a user_id and login_id. It sweeps once on entry so a restart cleans up
// immediately rather than waiting out the first interval.
//
// This lives here rather than on the analytics rollup ticker — which also prunes,
// on the same cadence — because analytics has no business knowing about auth codes.
// A transient failure is logged and retried on the next tick; a failure caused by
// shutdown cancelling ctx is not an error worth reporting.
func pruneAuthCodes(ctx context.Context, p authCodePruner, every time.Duration, logger *slog.Logger) {
	sweep := func() {
		n, err := p.PruneAuthCodes(ctx)
		switch {
		case ctx.Err() != nil:
			return // shutting down; not a real failure
		case err != nil:
			logger.Error("prune auth codes", "err", err)
		case n > 0:
			logger.Info("pruned expired auth codes", "count", n)
		}
	}
	sweep()

	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep()
		}
	}
}
