# Contributing to pushshell

Thanks for your interest. Issues and pull requests are welcome in **English or Japanese**.

## Ways to help

- Report bugs or propose features through the
  [issue templates](https://github.com/nonchan7720/pushshell/issues/new/choose).
- Pick something from [`docs/ROADMAP.md`](docs/ROADMAP.md). Items marked
  `good first issue` on the issue tracker are a good place to start.
- Improve documentation. `app/README.md` and `backend/README.md` are currently Japanese only,
  and English versions are wanted.
- Try the template for your own web app and tell us what was confusing.

Before starting a large change (new API endpoints, bridge protocol changes, new push
providers), please open an issue first so we can agree on the design.

## Development setup

Only [mise](https://mise.jdx.dev) is required. It installs Node, Go, Atlas and golangci-lint.

```sh
mise install
mise run setup        # go mod download + npm install
mise run gen          # ent / OpenAPI code generation
mise run check        # vet + test + typecheck
```

See the [README](README.md) for running the backend and the app, and
[`backend/README.md`](backend/README.md) / [`app/README.md`](app/README.md) for details.

## Repository layout

| Path | What lives there |
|---|---|
| `openapi/openapi.yaml` | The API contract. Change this first; Go and TS code are generated from it |
| `app/src/bridge.ts` | The `postMessage` protocol between the web app and the native layer |
| `backend/internal/core` | Use cases (stdlib only, shared by `cmd/server` and `cmd/worker`) |
| `backend/internal/push` | Push providers (`Sender` interface: Expo, FCM, APNs, log) |
| `backend/internal/store` | `core.Store` implementations (ent for the server, `database/sql` for Workers / D1) |
| `backend/internal/ent/schema` | Database schema. Migrations are generated with Atlas |

## Pull request checklist

- **Generated code is committed.** After touching `openapi/openapi.yaml` or the ent schema, run
  `mise run gen` and commit the result. CI fails if the generated code is stale.
- **Migrations.** After changing the ent schema, generate migrations for every dialect with
  `mise run db:diff <name> --dialect sqlite|mysql|postgres` (mysql / postgres need Docker), and
  mirror the sqlite migration into `backend/worker/migrations` with
  `mise run worker:migrations:sync`.
- **Checks pass locally.** `mise run check` at minimum; `mise run backend:lint` for Go changes.
- **The worker still builds.** Changes under `backend/internal` must not pull ent, atlas or
  kin-openapi into `cmd/worker`. CI enforces this and the 3 MB gzipped wasm size gate.
- **Keep the template promise.** A user of the template should be able to ship their app by
  changing environment variables only. Prefer new `.env` variables over hard-coded values.
- **Bridge / API compatibility.** Additive changes to the bridge protocol and the HTTP API are
  preferred. Call out breaking changes clearly in the PR description.
- **Pin GitHub Actions to commit SHAs** with the tag in a trailing comment, as the existing
  workflows do.

## Commit messages and PR titles

Write a short summary line describing the change. Either English or Japanese is fine. PRs are
squash-merged, so the PR title becomes the commit message on `main`.

## Reporting security issues

Please do not open a public issue. See [SECURITY.md](SECURITY.md).

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).

## License

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
