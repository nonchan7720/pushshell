// Package config は環境変数からサーバー設定を読み込む。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Dialect は対応する DB の種類。
type Dialect string

// 対応 DB 方言。
const (
	SQLite   Dialect = "sqlite"
	MySQL    Dialect = "mysql"
	Postgres Dialect = "postgres"
)

// Config はサーバーの実行時設定。
type Config struct {
	// Addr は listen アドレス (例: ":8080")。
	Addr string
	// DBDialect は sqlite | mysql | postgres。
	DBDialect Dialect
	// DBDSN は database/sql に渡す DSN。
	//   sqlite:   file:data/app.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)
	//   mysql:    user:pass@tcp(127.0.0.1:3306)/notify?parseTime=true
	//   postgres: postgres://user:pass@127.0.0.1:5432/notify?sslmode=disable
	DBDSN string
	// DBAutoMigrate が true なら起動時に ent のスキーマ作成を行う (開発用)。
	// 本番では Atlas の versioned migration (mise run db:apply) を使う。
	DBAutoMigrate bool
	// APIKey は /v1/notifications など server-to-server API の保護に使う。
	// 空の場合は API キー必須のエンドポイントは 401 を返す。
	APIKey string
	// PushProvider は expo | native | log。
	PushProvider string
	// ExpoAccessToken は Expo Push API のアクセストークン (任意)。
	ExpoAccessToken string

	// --- PUSH_PROVIDER=native (FCM / APNs) ---

	// FCMServiceAccountFile は FCM HTTP v1 用の Google サービスアカウント
	// JSON ファイルのパス (Firebase コンソール → プロジェクトの設定 →
	// サービスアカウント → 新しい秘密鍵の生成)。
	FCMServiceAccountFile string
	// FCMServiceAccountJSON は上記 JSON の中身を直接渡す場合 (どちらか一方)。
	FCMServiceAccountJSON string
	// FCMProjectID は省略時サービスアカウント JSON の project_id を使う。
	FCMProjectID string

	// APNSKeyFile は APNs 用 .p8 Auth Key ファイルのパス。
	APNSKeyFile string
	// APNSKey は上記 .p8 の中身 (PEM) を直接渡す場合 (どちらか一方)。
	APNSKey string
	// APNSKeyID は .p8 に対応する Key ID (Apple Developer の Keys ページ)。
	APNSKeyID string
	// APNSTeamID は Apple Developer の Team ID。
	APNSTeamID string
	// APNSTopic は apns-topic ヘッダに使う値 (通常はアプリのバンドル ID)。
	APNSTopic string
	// APNSEnvironment は sandbox | production (既定 production)。
	APNSEnvironment string
	// ShutdownTimeout は graceful shutdown の待ち時間。
	ShutdownTimeout time.Duration
	// LogLevel は debug | info | warn | error。
	LogLevel string
}

// Load は環境変数から Config を組み立てる。
func Load() (Config, error) {
	c := Config{
		Addr:            getenv("ADDR", ":8080"),
		DBDialect:       Dialect(strings.ToLower(getenv("DB_DIALECT", string(SQLite)))),
		DBDSN:           getenv("DB_DSN", "file:data/app.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"),
		DBAutoMigrate:   getenvBool("DB_AUTO_MIGRATE", false),
		APIKey:          os.Getenv("API_KEY"),
		PushProvider:    strings.ToLower(getenv("PUSH_PROVIDER", "expo")),
		ExpoAccessToken: os.Getenv("EXPO_ACCESS_TOKEN"),

		FCMServiceAccountFile: os.Getenv("FCM_SERVICE_ACCOUNT_FILE"),
		FCMServiceAccountJSON: os.Getenv("FCM_SERVICE_ACCOUNT_JSON"),
		FCMProjectID:          os.Getenv("FCM_PROJECT_ID"),

		APNSKeyFile:     os.Getenv("APNS_KEY_FILE"),
		APNSKey:         os.Getenv("APNS_KEY"),
		APNSKeyID:       os.Getenv("APNS_KEY_ID"),
		APNSTeamID:      os.Getenv("APNS_TEAM_ID"),
		APNSTopic:       os.Getenv("APNS_TOPIC"),
		APNSEnvironment: strings.ToLower(getenv("APNS_ENVIRONMENT", "production")),

		ShutdownTimeout: getenvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		LogLevel:        strings.ToLower(getenv("LOG_LEVEL", "info")),
	}
	switch c.DBDialect {
	case SQLite, MySQL, Postgres:
	default:
		return c, fmt.Errorf("config: unsupported DB_DIALECT %q (sqlite|mysql|postgres)", c.DBDialect)
	}
	switch c.PushProvider {
	case "expo", "native", "log":
	default:
		return c, fmt.Errorf("config: unsupported PUSH_PROVIDER %q (expo|native|log)", c.PushProvider)
	}
	switch c.APNSEnvironment {
	case "sandbox", "production":
	default:
		return c, fmt.Errorf("config: unsupported APNS_ENVIRONMENT %q (sandbox|production)", c.APNSEnvironment)
	}
	return c, nil
}

func getenv(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func getenvBool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getenvDuration(key string, def time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
