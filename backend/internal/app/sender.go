// Package app has the small bits of wiring shared by every entrypoint of
// this backend (cmd/server, cmd/worker, ...): building a push.Sender from
// config.Config, and setting up the *slog.Logger. Keeping it here (instead
// of duplicating it in each cmd/*/main.go) is what lets cmd/worker reuse the
// exact same PUSH_PROVIDER wiring as cmd/server without forking the logic.
package app

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/nonchan7720/pushshell/backend/internal/config"
	"github.com/nonchan7720/pushshell/backend/internal/push"
)

// NewLogger builds the process-wide *slog.Logger from LOG_LEVEL
// (config.Config.LogLevel): debug | info | warn | error, default info.
func NewLogger(level string) *slog.Logger {
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

// BuildSender builds the push.Sender to use for cfg.PushProvider
// (expo | native | log). It is the single place that turns config.Config
// into a push.Sender, shared by cmd/server and cmd/worker.
func BuildSender(cfg config.Config, logger *slog.Logger) (push.Sender, error) {
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
