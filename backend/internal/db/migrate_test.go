package db_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // sqlite driver, registered as "sqlite"

	"github.com/nonchan7720/webapp-notification/backend/internal/config"
	"github.com/nonchan7720/webapp-notification/backend/internal/db"
	"github.com/nonchan7720/webapp-notification/backend/internal/ent/device"
)

// TestSQLiteMigrationMatchesEntSchema is a regression check for the committed
// migrations/sqlite/*.sql files: it applies them to a fresh in-memory sqlite
// database with plain database/sql, then opens an ent client on top of the
// same database via internal/db.Open and checks that:
//
//  1. client.Schema.Create (ent's own auto-migration) is a no-op — i.e. the
//     migration files already produce exactly the schema ent's generated code
//     (internal/ent/migrate) would build itself.
//  2. A Device row can actually be created and read back through ent.
//
// If internal/ent/schema changes without regenerating migrations/sqlite (via
// `go run ./cmd/migrate diff <name> --dialect sqlite`), this test starts
// failing.
func TestSQLiteMigrationMatchesEntSchema(t *testing.T) {
	ctx := context.Background()

	// A shared-cache in-memory database, unique per test, that both the raw
	// database/sql connection (used to apply the migration files) and the
	// ent client (opened via internal/db.Open) attach to.
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") +
		"?mode=memory&cache=shared&_pragma=foreign_keys(1)"

	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = raw.Close() }()
	// Keep this connection alive for the lifetime of the test: an in-memory
	// sqlite database is dropped once its last connection closes, even in
	// cache=shared mode.
	raw.SetMaxIdleConns(1)
	raw.SetMaxOpenConns(1)
	if err := raw.PingContext(ctx); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}

	applySQLiteMigrations(ctx, t, raw, filepath.Join("..", "..", "migrations", "sqlite"))

	client, err := db.Open(config.Config{
		DBDialect: config.SQLite,
		DBDSN:     dsn,
	})
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = client.Close() }()

	// The migration files should already match the ent schema exactly, so
	// running ent's own auto-migration on top of them must be a no-op: it
	// must not error, e.g. by trying to re-create the already-existing
	// table or add a column/index that is already there.
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatalf("Schema.Create on top of migrated db: %v\n"+
			"(migrations/sqlite is likely out of date — regenerate with:\n"+
			" go run ./cmd/migrate diff <name> --dialect sqlite)", err)
	}

	d, err := client.Device.Create().
		SetInstallationID("inst-1").
		SetPlatform(device.PlatformIos).
		SetPushToken("ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]").
		Save(ctx)
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	if _, err := client.DeviceLogin.Create().SetDeviceID(d.ID).SetLoginID("user-1").Save(ctx); err != nil {
		t.Fatalf("create device login: %v", err)
	}

	got, err := client.Device.Get(ctx, d.ID)
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if got.InstallationID != "inst-1" || got.Platform != device.PlatformIos {
		t.Fatalf("unexpected device after round-trip: %+v", got)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("expected created_at/updated_at to be set: %+v", got)
	}

	logins, err := got.QueryLogins().All(ctx)
	if err != nil {
		t.Fatalf("query logins: %v", err)
	}
	if len(logins) != 1 || logins[0].LoginID != "user-1" {
		t.Fatalf("unexpected logins: %+v", logins)
	}

	// ON DELETE CASCADE: deleting the device must remove its DeviceLogin rows too.
	if err := client.Device.DeleteOne(got).Exec(ctx); err != nil {
		t.Fatalf("delete device: %v", err)
	}
	if n := client.DeviceLogin.Query().CountX(ctx); n != 0 {
		t.Fatalf("expected device_logins to cascade-delete, got %d rows", n)
	}
}

// applySQLiteMigrations executes every *.sql file in dir (in filename order,
// as LocalDir does) against db, using database/sql directly.
func applySQLiteMigrations(ctx context.Context, t *testing.T, sqlDB *sql.DB, dir string) {
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
// text on ";" into individual statements. None of our migration DDL contains
// a ";" inside a string literal, so this simple approach is sufficient.
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
