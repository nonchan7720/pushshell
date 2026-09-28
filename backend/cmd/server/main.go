// Command server は WebView ラッパーアプリ向けの通知バックエンド。
//
// 環境変数は internal/config を参照。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nonchan7720/pushshell/backend/internal/app"
	"github.com/nonchan7720/pushshell/backend/internal/config"
	"github.com/nonchan7720/pushshell/backend/internal/core"
	"github.com/nonchan7720/pushshell/backend/internal/db"
	"github.com/nonchan7720/pushshell/backend/internal/store/entstore"
	"github.com/nonchan7720/pushshell/backend/internal/transport/httpapi"
	"github.com/nonchan7720/pushshell/backend/internal/transport/httpapi/openapivalidate"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := app.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	client, err := db.Open(cfg)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := client.Close(); cerr != nil {
			logger.Warn("close db client", "err", cerr)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.DBAutoMigrate {
		logger.Warn("DB_AUTO_MIGRATE is enabled; use Atlas migrations in production")
		if err := client.Schema.Create(ctx); err != nil {
			return fmt.Errorf("auto migrate: %w", err)
		}
	}

	sender, err := app.BuildSender(cfg, logger)
	if err != nil {
		return err
	}

	svc := core.New(entstore.New(client), sender, core.AllowAll{}, logger)
	validator, err := openapivalidate.Middleware(cfg.APIKey)
	if err != nil {
		return err
	}
	mux, err := httpapi.New(httpapi.Options{Service: svc, APIKey: cfg.APIKey, Logger: logger, Validator: validator})
	if err != nil {
		return err
	}
	if cfg.APIKey == "" {
		logger.Warn("API_KEY is empty; /v1/notifications will reject all requests")
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Addr, "db", cfg.DBDialect, "push", cfg.PushProvider)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}
