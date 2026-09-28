// Package db は DB 方言ごとに ent クライアントを開く。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "github.com/go-sql-driver/mysql" // mysql driver
	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver (database/sql)
	_ "modernc.org/sqlite"             // cgo-free sqlite driver

	"github.com/nonchan7720/webapp-notification/backend/internal/config"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent"
)

// Open は設定に従って ent クライアントを開く。
func Open(cfg config.Config) (*ent.Client, error) {
	var (
		driverName string
		entDialect string
	)
	switch cfg.DBDialect {
	case config.SQLite:
		driverName, entDialect = "sqlite", dialect.SQLite
		if err := ensureSQLiteDir(cfg.DBDSN); err != nil {
			return nil, err
		}
	case config.MySQL:
		driverName, entDialect = "mysql", dialect.MySQL
	case config.Postgres:
		driverName, entDialect = "pgx", dialect.Postgres
	default:
		return nil, fmt.Errorf("db: unsupported dialect %q", cfg.DBDialect)
	}

	sqlDB, err := sql.Open(driverName, cfg.DBDSN)
	if err != nil {
		return nil, fmt.Errorf("db: open %s: %w", cfg.DBDialect, err)
	}
	if cfg.DBDialect == config.SQLite {
		// SQLite は単一ライターなので接続を 1 本に絞ると "database is locked" を避けやすい。
		sqlDB.SetMaxOpenConns(1)
	}
	if err := sqlDB.PingContext(context.Background()); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("db: ping %s: %w", cfg.DBDialect, err)
	}

	drv := entsql.OpenDB(entDialect, sqlDB)
	return ent.NewClient(ent.Driver(drv)), nil
}

// ensureSQLiteDir は "file:path/to/db?..." 形式の DSN からディレクトリを作る。
func ensureSQLiteDir(dsn string) error {
	path := strings.TrimPrefix(dsn, "file:")
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	if path == "" || path == ":memory:" || strings.HasPrefix(path, ":") {
		return nil
	}
	dir := filepath.Dir(path)
	if dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("db: create dir %s: %w", dir, err)
	}
	return nil
}
