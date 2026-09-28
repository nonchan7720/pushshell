package entstore_test

import (
	"database/sql"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"

	"github.com/nonchan7720/webapp-notification/backend/internal/core"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent/enttest"
	"github.com/nonchan7720/webapp-notification/backend/internal/store/entstore"
	"github.com/nonchan7720/webapp-notification/backend/internal/store/storetest"

	_ "modernc.org/sqlite" // cgo-free sqlite driver, registered as "sqlite"
)

func TestStore(t *testing.T) {
	storetest.RunStoreTests(t, func(t *testing.T) core.Store {
		t.Helper()
		// modernc.org/sqlite registers itself as "sqlite" (not entgo's
		// dialect.SQLite = "sqlite3"), so open it directly and hand ent the
		// *sql.DB (same pattern as the previous internal/handler tests),
		// rather than enttest.Open (which would call sql.Open("sqlite3", ...)).
		dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared&_pragma=foreign_keys(1)"
		sqlDB, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatalf("open sqlite: %v", err)
		}
		sqlDB.SetMaxOpenConns(1)
		client := enttest.NewClient(t, enttest.WithOptions(ent.Driver(entsql.OpenDB(dialect.SQLite, sqlDB))))
		t.Cleanup(func() { _ = client.Close() })
		return entstore.New(client)
	})
}
