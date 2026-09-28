//go:build js && wasm

// Command worker is the Cloudflare Workers + D1 entrypoint for this
// notification backend. It is compiled to wasm (GOOS=js GOARCH=wasm, see
// backend/worker/build.sh) and served by github.com/syumai/workers-go
// (workers.Serve). Unlike cmd/server it does not link ent, atlas or
// kin-openapi: internal/core (the business logic) is stdlib-only, and this
// entrypoint reaches D1 through internal/store/sqlstore (plain
// database/sql, hand-written SQL) instead of internal/db.Open + ent's
// SQLite dialect, and builds internal/transport/httpapi's handler without a
// Validator (internal/transport/httpapi/openapivalidate, which does depend
// on kin-openapi, is cmd/server-only).
//
// Unlike cmd/server there is no ADDR to listen on (the Workers runtime owns
// the fetch event), no os/signal-driven graceful shutdown (a Worker's
// lifetime is one request, not a long-running process) and no
// DB_AUTO_MIGRATE (D1 schema is managed out-of-band with
// `wrangler d1 migrations`, see backend/worker/migrations and
// `mise run worker:migrate:local` / `worker:migrate:remote`).
package main

import (
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	workers "github.com/syumai/workers-go"
	"github.com/syumai/workers-go/cloudflare"
	"github.com/syumai/workers-go/cloudflare/d1"

	"github.com/nonchan7720/pushshell/backend/internal/app"
	"github.com/nonchan7720/pushshell/backend/internal/config"
	"github.com/nonchan7720/pushshell/backend/internal/core"
	"github.com/nonchan7720/pushshell/backend/internal/store/sqlstore"
	"github.com/nonchan7720/pushshell/backend/internal/transport/httpapi"
)

// d1Binding is the D1 binding name this worker expects, matching
// wrangler.toml's `[[d1_databases]] binding = "DB"` (backend/worker).
const d1Binding = "DB"

// workerEnvKeys are the internal/config.Load() keys this entrypoint copies
// from the Workers environment (wrangler.toml [vars] / `wrangler secret
// put`) into os.Setenv, so config.Load() itself is reused completely
// unchanged. Left out on purpose, because they have no meaning on Workers:
//
//   - ADDR, SHUTDOWN_TIMEOUT: there is no net.Listen / graceful shutdown here.
//   - DB_DIALECT, DB_DSN, DB_AUTO_MIGRATE: D1 is opened directly (openD1
//     below) and its schema is managed with `wrangler d1 migrations`
//     instead of DB_AUTO_MIGRATE.
//   - FCM_SERVICE_ACCOUNT_FILE, APNS_KEY_FILE: Workers has no filesystem to
//     read a path from; use FCM_SERVICE_ACCOUNT_JSON / APNS_KEY (the file
//     contents, as a `wrangler secret put`) instead.
var workerEnvKeys = []string{
	"API_KEY",
	"PUSH_PROVIDER",
	"EXPO_ACCESS_TOKEN",
	"FCM_SERVICE_ACCOUNT_JSON",
	"FCM_PROJECT_ID",
	"APNS_KEY",
	"APNS_KEY_ID",
	"APNS_TEAM_ID",
	"APNS_TOPIC",
	"APNS_ENVIRONMENT",
	"LOG_LEVEL",
}

func main() {
	if err := run(); err != nil {
		serveStartupError(err)
	}
}

func run() error {
	loadEnvFromWorkersRuntime()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := app.NewLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	sqlDB, err := openD1()
	if err != nil {
		return err
	}

	sender, err := app.BuildSender(cfg, logger)
	if err != nil {
		return err
	}

	svc := core.New(sqlstore.New(sqlDB), sender, core.AllowAll{}, logger)
	mux, err := httpapi.New(httpapi.Options{Service: svc, APIKey: cfg.APIKey, Logger: logger})
	if err != nil {
		return err
	}
	if cfg.APIKey == "" {
		logger.Warn("API_KEY is empty; /v1/notifications will reject all requests")
	}

	logger.Info("worker ready", "push", cfg.PushProvider)
	workers.Serve(mux)
	return nil
}

// loadEnvFromWorkersRuntime copies the known config.Load() environment keys
// (workerEnvKeys) out of the Workers runtime (cloudflare.Getenv, which reads
// wrangler.toml [vars] and `wrangler secret put` values / .dev.vars) into
// os.Setenv, so internal/config.Load() can be called completely unchanged.
func loadEnvFromWorkersRuntime() {
	for _, key := range workerEnvKeys {
		if v := cloudflare.Getenv(key); v != "" {
			_ = os.Setenv(key, v)
		}
	}
}

// openD1 opens the D1 database bound as d1Binding in wrangler.toml
// ([[d1_databases]] binding = "DB") through database/sql — D1 speaks
// SQLite. internal/store/sqlstore binds every query argument itself as a
// string/int64/nil (never a time.Time), which are exactly the kinds
// syscall/js.Value.Call (used by d1.Stmt under the hood) can represent, so
// no adapter is needed here (a previous version of this file wrapped the
// connector to convert time.Time query arguments/results for ent's sake;
// sqlstore has no such argument to begin with).
//
// Note D1's driver.Conn does not support transactions (BeginTx returns an
// error); internal/core/service.go never opens one, so this is not a
// practical limitation for this backend.
func openD1() (*sql.DB, error) {
	connector, err := d1.OpenConnector(d1Binding)
	if err != nil {
		return nil, fmt.Errorf("worker: open D1 binding %q: %w", d1Binding, err)
	}
	return sql.OpenDB(connector), nil
}

// serveStartupError makes a startup failure visible as an HTTP 500 (and in
// `wrangler tail` / the dashboard's logs) instead of the worker silently
// never responding: there is no os.Exit/stderr-goes-to-a-terminal story on
// Workers, and workers.Serve must still be called for handleRequest to be
// wired up at all.
func serveStartupError(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	msg := err.Error()
	workers.Serve(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "worker: startup failed: "+msg, http.StatusInternalServerError)
	}))
}
