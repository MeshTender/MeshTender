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

	"github.com/jleight/meshtender/internal/auth"
	"github.com/jleight/meshtender/internal/config"
	"github.com/jleight/meshtender/internal/core"
	"github.com/jleight/meshtender/internal/identity"
	"github.com/jleight/meshtender/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	var reset bool
	flag.BoolVar(&reset, "reset", false,
		"truncate all data except users, passkeys, sessions, and the server identity, then exit")
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
		if err := st.Reset(ctx); err != nil {
			return err
		}
		logger.Info("database reset — kept users, passkeys, sessions, and the server identity")
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
		Secure:        cfg.Secure,
	})
	if err != nil {
		return err
	}

	srv, err := core.NewServer(st, authSvc, idSvc, cfg)
	if err != nil {
		return err
	}

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
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

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}
