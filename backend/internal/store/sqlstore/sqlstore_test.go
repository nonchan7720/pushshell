package sqlstore_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // cgo-free sqlite driver, registered as "sqlite"

	"github.com/nonchan7720/webapp-notification/backend/internal/core"
	"github.com/nonchan7720/webapp-notification/backend/internal/store/sqlstore"
	"github.com/nonchan7720/webapp-notification/backend/internal/store/storetest"
)

func TestStore(t *testing.T) {
	storetest.RunStoreTests(t, func(t *testing.T) core.Store {
		t.Helper()
		dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared&_pragma=foreign_keys(1)"
		sqlDB, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		// Keep the sole connection alive for the test's lifetime: an
		// in-memory sqlite database is dropped once its last connection
		// closes, even in cache=shared mode.
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
		if err := sqlDB.PingContext(context.Background()); err != nil {
			t.Fatalf("ping sqlite: %v", err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })

		applyMigrations(context.Background(), t, sqlDB, filepath.Join("..", "..", "..", "migrations", "sqlite"))
		return sqlstore.New(sqlDB)
	})
}

// applyMigrations applies every *.sql file in dir (in filename order) to
// sqlDB, the same way internal/db/migrate_test.go does for the ent-based
// path — used here to prove sqlstore works against the exact schema
// migrations/sqlite produces (and, via internal/db/migrate_test.go, that
// entstore does too), not just a schema sqlstore happens to expect.
func applyMigrations(ctx context.Context, t *testing.T, sqlDB *sql.DB, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir %s: %v", dir, err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		found = true
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read migration %s: %v", e.Name(), err)
		}
		for _, stmt := range sqlStatements(string(b)) {
			if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
				t.Fatalf("exec statement from %s: %v\n%s", e.Name(), err, stmt)
			}
		}
	}
	if !found {
		t.Fatalf("no *.sql migration files found in %s", dir)
	}
}

// sqlStatements strips Atlas's "-- comment" lines and splits the remaining
// text on ";" into individual statements (same approach as
// internal/db/migrate_test.go; none of our migration DDL contains a ";"
// inside a string literal).
func sqlStatements(sqlText string) []string {
	var body strings.Builder
	for _, line := range strings.Split(sqlText, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	var stmts []string
	for _, s := range strings.Split(body.String(), ";") {
		s = strings.TrimSpace(s)
		if s != "" {
			stmts = append(stmts, s)
		}
	}
	return stmts
}
