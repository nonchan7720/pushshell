// Command migrate generates ent-based versioned migration files using the
// Atlas migration engine as a Go library (ariga.io/atlas + entgo.io/ent's
// "sql/versioned-migration" feature), instead of relying on the Atlas CLI's
// `ent://` schema loader.
//
// The Atlas CLI available in this environment is a community build, which
// does not support the `ent://` loader used by `atlas migrate diff` in
// ent's official docs (https://entgo.io/docs/versioned-migrations). This
// program is ent's documented alternative: it drives
// entgo.io/ent/dialect/sql/schema.Diff (exposed on the generated
// internal/ent/migrate package as NamedDiff, requires generating ent with
// `--feature sql/versioned-migration`) directly, so it only needs the Atlas
// Go module, not the Atlas CLI's schema loaders.
//
// Usage:
//
//	go run ./cmd/migrate diff <name> --dialect sqlite|mysql|postgres [--dev-url <url>] [--dir <path>]
//
// The generated .sql files (plus the updated atlas.sum) are written to
// migrations/<dialect>/ (or --dir, if given). The resulting directory can be
// applied and inspected with the Atlas CLI, e.g.:
//
//	atlas migrate apply --env sqlite --url "sqlite://data/app.db"
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	atlasmigrate "ariga.io/atlas/sql/migrate"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	_ "github.com/go-sql-driver/mysql" // mysql driver, for the dev-url connection
	"github.com/jackc/pgx/v5/stdlib"   // pgx driver (database/sql), for the dev-url connection
	"modernc.org/sqlite"

	entmigrate "github.com/nonchan7720/webapp-notification/backend/internal/ent/migrate"
)

func init() {
	// ariga.io/atlas's built-in sqlite driver always opens the dev/target
	// database through database/sql using the driver name "sqlite3" (the
	// name historically registered by the CGO-based mattn/go-sqlite3). This
	// project never links CGO, so register the pure-Go modernc.org/sqlite
	// driver under that same name — its DSN dialect is a superset of
	// mattn/go-sqlite3's (it understands "_fk=1" etc, see modernc.org/sqlite's
	// package docs), so the URLs Atlas builds work unmodified.
	sql.Register("sqlite3", &sqlite.Driver{})
	// Likewise, ariga.io/atlas's postgres driver always opens through the
	// database/sql driver name "postgres" (historically lib/pq). Register
	// jackc/pgx's stdlib driver (already used by internal/db) under that
	// name too, so postgres dev-url connections work without adding lib/pq
	// as a dependency.
	sql.Register("postgres", stdlib.GetDefaultDriver())
}

// defaultDevURL holds the default Atlas "dev database" URL per dialect, used
// to compute the diff when --dev-url is not given. A dev database is a
// scratch database Atlas uses to compute the desired schema state; it is
// never written to outside of that computation.
var defaultDevURL = map[string]string{
	"sqlite":   "sqlite://file?mode=memory&_fk=1",
	"mysql":    "docker://mysql/8/dev",
	"postgres": "docker://postgres/16/dev?search_path=public",
}

// entDialectName maps our --dialect flag values to the ent dialect constant
// schema.WithDialect expects (this selects ent's SQL-generation flavor, and
// is not the same string as the database/sql driver name or the Atlas URL
// scheme).
var entDialectName = map[string]string{
	"sqlite":   dialect.SQLite,
	"mysql":    dialect.MySQL,
	"postgres": dialect.Postgres,
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "diff":
		err = runDiff(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "migrate: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  go run ./cmd/migrate diff <name> --dialect sqlite|mysql|postgres [--dev-url <url>] [--dir <path>]

Generates a versioned migration file for the ent schema (internal/ent/schema)
via the ent + Atlas Go libraries, without using the Atlas CLI's ent:// schema
loader. Files are written to migrations/<dialect>/ by default.`)
}

func runDiff(args []string) error {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	dialectFlag := fs.String("dialect", "sqlite", "sqlite | mysql | postgres")
	devURLFlag := fs.String("dev-url", "", "dev database URL used to compute the diff (defaults per --dialect)")
	dirFlag := fs.String("dir", "", "migration directory (defaults to migrations/<dialect>)")
	// The name is positional and documented to come first ("diff <name>
	// --dialect ..."), but the flag package only parses flags up to the
	// first non-flag argument and treats everything after as positional.
	// Pull the (single) positional argument out by hand so flags can follow
	// it, then hand the rest to fs.Parse as normal.
	var name string
	flagArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			flagArgs = append(flagArgs, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			flagArgs = append(flagArgs, a)
			// A flag of the form "-x value" (not "-x=value") consumes the
			// next argument too, unless it is a boolean flag; none of ours
			// are boolean, so always take the next token when present and
			// the flag itself has no "=".
			if !strings.Contains(a, "=") && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
		case name == "":
			name = a
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if name == "" {
		return errors.New("migration name is required: go run ./cmd/migrate diff <name> --dialect <dialect>")
	}

	d, ok := entDialectName[*dialectFlag]
	if !ok {
		return fmt.Errorf("unsupported --dialect %q (want sqlite | mysql | postgres)", *dialectFlag)
	}

	devURL := *devURLFlag
	if devURL == "" {
		devURL = defaultDevURL[*dialectFlag]
	}

	migDir := *dirFlag
	if migDir == "" {
		migDir = filepath.Join("migrations", *dialectFlag)
	}
	if err := os.MkdirAll(migDir, 0o755); err != nil {
		return fmt.Errorf("create migration dir %s: %w", migDir, err)
	}
	dir, err := atlasmigrate.NewLocalDir(migDir)
	if err != nil {
		return fmt.Errorf("open migration dir %s: %w", migDir, err)
	}
	before, err := sqlFiles(migDir)
	if err != nil {
		return fmt.Errorf("list migration dir %s: %w", migDir, err)
	}

	ctx := context.Background()
	err = entmigrate.NamedDiff(ctx, devURL, name,
		schema.WithDir(dir),
		schema.WithMigrationMode(schema.ModeReplay),
		schema.WithDialect(d),
		schema.WithFormatter(atlasmigrate.DefaultFormatter),
	)
	switch {
	case errors.Is(err, atlasmigrate.ErrNoPlan):
		fmt.Println("migrate: no schema changes detected, no migration file written")
		return nil
	case err != nil:
		return fmt.Errorf("diff (dialect=%s, dev-url=%s): %w", *dialectFlag, devURL, err)
	}
	// NamedDiff also returns a nil error (without erroring via ErrNoPlan,
	// since ent does not expose a way to opt into that from this package)
	// when the computed plan has no changes; detect that case by comparing
	// the migration dir's file list before and after.
	after, err := sqlFiles(migDir)
	if err != nil {
		return fmt.Errorf("list migration dir %s: %w", migDir, err)
	}
	if len(after) == len(before) {
		fmt.Println("migrate: no schema changes detected, no migration file written")
		return nil
	}
	fmt.Printf("migrate: wrote migration %q to %s\n", name, migDir)
	return nil
}

// sqlFiles returns the sorted list of *.sql file names directly under dir.
func sqlFiles(dir string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	return matches, nil
}
