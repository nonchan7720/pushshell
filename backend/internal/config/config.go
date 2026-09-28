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
	// PushProvider は expo | log。
	PushProvider string
	// ExpoAccessToken は Expo Push API のアクセストークン (任意)。
	ExpoAccessToken string
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
		ShutdownTimeout: getenvDuration("SHUTDOWN_TIMEOUT", 10*time.Second),
		LogLevel:        strings.ToLower(getenv("LOG_LEVEL", "info")),
	}
	switch c.DBDialect {
	case SQLite, MySQL, Postgres:
	default:
		return c, fmt.Errorf("config: unsupported DB_DIALECT %q (sqlite|mysql|postgres)", c.DBDialect)
	}
	switch c.PushProvider {
	case "expo", "log":
	default:
		return c, fmt.Errorf("config: unsupported PUSH_PROVIDER %q (expo|log)", c.PushProvider)
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
