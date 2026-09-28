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

	"github.com/nonchan7720/webapp-notification/backend/internal/config"
	"github.com/nonchan7720/webapp-notification/backend/internal/db"
	"github.com/nonchan7720/webapp-notification/backend/internal/handler"
	"github.com/nonchan7720/webapp-notification/backend/internal/push"
	"github.com/nonchan7720/webapp-notification/backend/internal/server"
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
	logger := newLogger(cfg.LogLevel)
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

	sender, err := buildSender(cfg, logger)
	if err != nil {
		return err
	}

	h := handler.New(client, sender, handler.AllowAll{}, logger)
	mux, err := server.New(server.Options{Handler: h, APIKey: cfg.APIKey, Logger: logger})
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

// buildSender は PUSH_PROVIDER に応じた push.Sender を組み立てる。
func buildSender(cfg config.Config, logger *slog.Logger) (push.Sender, error) {
	switch cfg.PushProvider {
	case "log":
		return push.LogSender{Logger: logger}, nil
	case "native":
		return buildNativeSender(cfg, logger)
	default:
		return &push.ExpoSender{AccessToken: cfg.ExpoAccessToken}, nil
	}
}

// buildNativeSender は FCM_* / APNS_* の設定から push.NativeSender を組み立てる。
// どちらか一方でも設定されていれば起動でき、設定されていないプラットフォーム
// 宛のメッセージは送信時にエラーになる (push.NativeSender を参照)。両方とも
// 未設定ならここでエラーを返す。
func buildNativeSender(cfg config.Config, logger *slog.Logger) (push.Sender, error) {
	fcmSender, err := buildFCMSender(cfg)
	if err != nil {
		return nil, err
	}
	apnsSender, err := buildAPNSSender(cfg)
	if err != nil {
		return nil, err
	}
	if fcmSender == nil && apnsSender == nil {
		return nil, fmt.Errorf("config: PUSH_PROVIDER=native requires FCM_SERVICE_ACCOUNT_FILE/FCM_SERVICE_ACCOUNT_JSON and/or APNS_KEY_FILE/APNS_KEY to be set")
	}
	if fcmSender == nil {
		logger.Warn("PUSH_PROVIDER=native: FCM is not configured; android devices will fail to receive push")
	}
	if apnsSender == nil {
		logger.Warn("PUSH_PROVIDER=native: APNs is not configured; ios devices will fail to receive push")
	}
	return push.NativeSender{Android: fcmSender, IOS: apnsSender}, nil
}

func buildFCMSender(cfg config.Config) (*push.FCMSender, error) {
	var raw []byte
	switch {
	case cfg.FCMServiceAccountJSON != "":
		raw = []byte(cfg.FCMServiceAccountJSON)
	case cfg.FCMServiceAccountFile != "":
		b, err := os.ReadFile(cfg.FCMServiceAccountFile)
		if err != nil {
			return nil, fmt.Errorf("read FCM_SERVICE_ACCOUNT_FILE: %w", err)
		}
		raw = b
	default:
		return nil, nil
	}
	sender, err := push.NewFCMSender(raw, cfg.FCMProjectID)
	if err != nil {
		return nil, fmt.Errorf("build FCM sender: %w", err)
	}
	return sender, nil
}

func buildAPNSSender(cfg config.Config) (*push.APNSSender, error) {
	var raw []byte
	switch {
	case cfg.APNSKey != "":
		raw = []byte(cfg.APNSKey)
	case cfg.APNSKeyFile != "":
		b, err := os.ReadFile(cfg.APNSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read APNS_KEY_FILE: %w", err)
		}
		raw = b
	default:
		return nil, nil
	}
	if cfg.APNSKeyID == "" || cfg.APNSTeamID == "" || cfg.APNSTopic == "" {
		return nil, fmt.Errorf("config: APNS_KEY(_FILE) is set but APNS_KEY_ID/APNS_TEAM_ID/APNS_TOPIC is missing")
	}
	env := push.APNSProduction
	if cfg.APNSEnvironment == "sandbox" {
		env = push.APNSSandbox
	}
	sender, err := push.NewAPNSSender(raw, cfg.APNSKeyID, cfg.APNSTeamID, cfg.APNSTopic, env)
	if err != nil {
		return nil, fmt.Errorf("build APNs sender: %w", err)
	}
	return sender, nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
