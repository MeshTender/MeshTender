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
	// captured, with a background goroutine doing the writes + rollups. It runs on
	// its own context — NOT the signal context — so it keeps consuming events while
	// in-flight requests drain during shutdown; we stop and drain it afterwards.
	rec := analytics.New(st, cfg)
	analyticsCtx, stopAnalytics := context.WithCancel(context.Background())
	analyticsDone := make(chan struct{})
	go func() {
		defer close(analyticsDone)
		rec.Run(analyticsCtx)
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

	// Now stop the flusher and wait for its final flush to complete before the
	// deferred st.Close() closes the pool underneath it.
	stopAnalytics()
	<-analyticsDone
	return srvErr
}
