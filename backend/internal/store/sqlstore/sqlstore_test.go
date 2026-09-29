package sqlstore_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	_ "modernc.org/sqlite" // cgo-free sqlite driver, registered as "sqlite"

	"github.com/nonchan7720/pushshell/backend/internal/core"
	"github.com/nonchan7720/pushshell/backend/internal/store/sqlstore"
	"github.com/nonchan7720/pushshell/backend/internal/store/storetest"
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

// limitedDriver wraps the sqlite driver and fails any statement carrying more
// than sqlstore.MaxBindParams bound parameters, like Cloudflare D1 does
// (sqlite itself allows far more, so without this the chunking tests would
// pass even if the IN lists were not chunked).
type limitedDriver struct{ driver.Driver }

func (d limitedDriver) Open(name string) (driver.Conn, error) {
	c, err := d.Driver.Open(name)
	if err != nil {
		return nil, err
	}
	return &limitedConn{Conn: c}, nil
}

type limitedConn struct{ driver.Conn }

func checkBindLimit(n int) error {
	if n > sqlstore.MaxBindParams {
		return fmt.Errorf("too many bound parameters: %d > %d (D1 limit)", n, sqlstore.MaxBindParams)
	}
	return nil
}

func (c *limitedConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := checkBindLimit(len(args)); err != nil {
		return nil, err
	}
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *limitedConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if err := checkBindLimit(len(args)); err != nil {
		return nil, err
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func init() {
	base, err := sql.Open("sqlite", "")
	if err != nil {
		panic(err)
	}
	sql.Register("sqlite-d1-limit", limitedDriver{base.Driver()})
	_ = base.Close()
}

// newChunkTestStore returns a sqlstore.Store on a fresh in-memory sqlite
// database with the real migrations applied (like TestStore does), on a
// driver that enforces D1's bound-parameter limit.
func newChunkTestStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared&_pragma=foreign_keys(1)"
	sqlDB, err := sql.Open("sqlite-d1-limit", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	applyMigrations(context.Background(), t, sqlDB, filepath.Join("..", "..", "..", "migrations", "sqlite"))
	return sqlstore.New(sqlDB)
}

// TestFindDevicesByInstallationIDs_ManyIDs: more IDs than D1 allows as bound
// parameters per statement (sqlstore.maxBindParams = 100) are queried in
// chunks; the merged result must still be complete and ordered by device ID.
func TestFindDevicesByInstallationIDs_ManyIDs(t *testing.T) {
	ctx := context.Background()
	s := newChunkTestStore(t)

	n := 2*sqlstore.MaxBindParams + 50
	var want []string
	for i := 0; i < n; i++ {
		inst := fmt.Sprintf("inst-%04d", i)
		if _, err := s.UpsertDevice(ctx, core.DeviceInput{InstallationID: inst, Platform: "ios", PushToken: "tok"}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
		if i%10 == 0 {
			if err := s.LinkLogin(ctx, inst, "u"+inst); err != nil {
				t.Fatalf("link: %v", err)
			}
		}
		want = append(want, inst)
	}

	// Request in reverse order, interleaved with unknown IDs and a duplicate.
	var req []string
	for i := n - 1; i >= 0; i-- {
		req = append(req, want[i])
		if i%7 == 0 {
			req = append(req, fmt.Sprintf("unknown-%d", i))
		}
	}
	req = append(req, want[0])

	devices, err := s.FindDevicesByInstallationIDs(ctx, req)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(devices) != n {
		t.Fatalf("expected %d devices, got %d", n, len(devices))
	}
	for i, d := range devices {
		if d.InstallationID != want[i] {
			t.Fatalf("device %d: got %s want %s (must be ordered by device ID)", i, d.InstallationID, want[i])
		}
		if i > 0 && devices[i-1].ID >= d.ID {
			t.Fatalf("device IDs not ascending at %d", i)
		}
		if i%10 == 0 {
			if len(d.LoginIDs) != 1 || d.LoginIDs[0] != "u"+d.InstallationID {
				t.Fatalf("device %s: unexpected loginIds %v", d.InstallationID, d.LoginIDs)
			}
		} else if len(d.LoginIDs) != 0 {
			t.Fatalf("device %s: unexpected loginIds %v", d.InstallationID, d.LoginIDs)
		}
	}
}

// TestFindDevicesByLogins_ManyLoginIDs: the login IDs are queried in chunks
// of sqlstore.MaxBindParams, and a device linked to login IDs that fall in
// different chunks must still come back once, with its complete, sorted,
// deduplicated LoginIDs.
func TestFindDevicesByLogins_ManyLoginIDs(t *testing.T) {
	ctx := context.Background()
	s := newChunkTestStore(t)

	n := 2*sqlstore.MaxBindParams + 50
	loginIDs := make([]string, n)
	for i := range loginIDs {
		loginIDs[i] = fmt.Sprintf("login-%04d", i)
	}
	for _, inst := range []string{"all", "subset", "single", "unrelated"} {
		if _, err := s.UpsertDevice(ctx, core.DeviceInput{InstallationID: inst, Platform: "android", PushToken: "tok"}); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}
	link := func(inst, loginID string) {
		t.Helper()
		if err := s.LinkLogin(ctx, inst, loginID); err != nil {
			t.Fatalf("link %s/%s: %v", inst, loginID, err)
		}
	}
	// "all" is linked to every requested login ID (spans 3 chunks).
	for _, id := range loginIDs {
		link("all", id)
	}
	// "subset" is linked to IDs on both sides of each chunk boundary.
	var subset []string
	for _, i := range []int{0, sqlstore.MaxBindParams - 1, sqlstore.MaxBindParams, 2*sqlstore.MaxBindParams - 1, 2 * sqlstore.MaxBindParams, n - 1} {
		link("subset", loginIDs[i])
		subset = append(subset, loginIDs[i])
	}
	// "single" only matches an ID in the last chunk; "unrelated" matches nothing requested.
	link("single", loginIDs[n-2])
	link("unrelated", "not-requested")

	// Reverse the input so the sorted result cannot come from input order, and repeat some IDs.
	req := make([]string, 0, n+3)
	for i := n - 1; i >= 0; i-- {
		req = append(req, loginIDs[i])
	}
	req = append(req, loginIDs[0], loginIDs[sqlstore.MaxBindParams], loginIDs[n-1])

	matches, err := s.FindDevicesByLogins(ctx, req)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(matches))
	}
	for i, wantInst := range []string{"all", "subset", "single"} {
		if matches[i].Device.InstallationID != wantInst {
			t.Fatalf("match %d: got %s want %s (must be ordered by device ID)", i, matches[i].Device.InstallationID, wantInst)
		}
	}
	if !slices.Equal(matches[0].LoginIDs, loginIDs) { // loginIDs is already sorted and unique
		t.Fatalf("all: loginIds incomplete/unsorted/duplicated: got %d entries", len(matches[0].LoginIDs))
	}
	sort.Strings(subset)
	if !slices.Equal(matches[1].LoginIDs, subset) {
		t.Fatalf("subset: got %v want %v", matches[1].LoginIDs, subset)
	}
	if !slices.Equal(matches[2].LoginIDs, []string{loginIDs[n-2]}) {
		t.Fatalf("single: got %v", matches[2].LoginIDs)
	}
}
