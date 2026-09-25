// Command neolib runs the self-hosted book reader server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lassegit/neolib/internal/auth"
	"github.com/lassegit/neolib/internal/config"
	"github.com/lassegit/neolib/internal/database"
	"github.com/lassegit/neolib/internal/store"
	"github.com/lassegit/neolib/internal/web"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("neolib stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}

	for _, dir := range []string{cfg.DataDir, cfg.BooksDir(), cfg.TempDir()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	st := store.New(db)
	if n, err := st.DeleteExpiredSessions(ctx, time.Now().Unix()); err != nil {
		logger.Warn("could not prune expired sessions", "error", err)
	} else if n > 0 {
		logger.Info("pruned expired sessions", "count", n)
	}

	authManager := auth.NewManager(st, auth.Options{
		SecureCookies: cfg.CookieSecure,
		SessionTTL:    30 * 24 * time.Hour,
	})

	handler, err := web.New(web.Deps{
		Config: cfg,
		Store:  st,
		Auth:   authManager,
		Logger: logger,
	})
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
		}
	}()

	logger.Info("neolib listening", "addr", cfg.Addr, "data_dir", cfg.DataDir)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-shutdownDone
	return nil
}
