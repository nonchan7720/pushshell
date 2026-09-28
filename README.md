# webapp-notification

Web アプリを WebView で表示し、プッシュ通知だけをネイティブ側で担うアプリのテンプレートと、
その通知バックエンドのモノレポです。

```
.
├── app/        React Native (Expo) アプリ。URL / 名前 / アイコンはビルド時に env で注入
├── backend/    Go の通知バックエンド (ent + Atlas, sqlite / mysql / postgres)
├── openapi/    アプリとバックエンドの契約 (ここから両方のコードを生成)
├── examples/   bridge の動作確認用の静的ページ
└── .mise.toml  ツールチェーンとタスク定義 (開発環境の入口)
```

## 仕組み

1. アプリは `WEBAPP_URL` を WebView で表示するだけ。基本操作はすべて Web アプリ側。
2. Web アプリがログインしたら `window.ReactNativeWebView.postMessage(JSON.stringify({ type: "login", loginId }))` を送る。
3. アプリは通知権限を取り、Expo Push Token と端末情報をまとめて `POST /v1/devices` に登録する。
4. 任意のサーバーが `POST /v1/notifications` に `{ loginIds, title, body, url }` を投げると、
   バックエンドがそのログイン ID に紐づく端末へ Expo Push API 経由で通知する。
5. 通知をタップすると、アプリは `url` を WebView で開く。

postMessage のプロトコルは [`app/src/bridge.ts`](app/src/bridge.ts) に、API は
[`openapi/openapi.yaml`](openapi/openapi.yaml) に定義があります。

## セットアップ

[mise](https://mise.jdx.dev) だけ入れてください。Node / Go / Atlas / golangci-lint は mise が揃えます。

```sh
mise install          # ツールチェーン
mise run setup        # go mod download + npm install
mise run gen          # ent / OpenAPI (Go, TS) のコード生成
mise tasks            # タスク一覧
```

## 動かす

```sh
# バックエンド (sqlite, 通知はログ出力のみ)
cp backend/.env.example backend/.env   # 必要に応じて編集
API_KEY=dev PUSH_PROVIDER=log DB_AUTO_MIGRATE=true mise run backend:run

# アプリ (dev client が必要。Expo Go は Android のリモート通知非対応)
cp app/.env.example app/.env           # WEBAPP_URL, API_BASE_URL, EAS_PROJECT_ID など
mise run app:prebuild
mise run app:run:android   # or app:run:ios
```

通知を送る:

```sh
curl -X POST http://localhost:8080/v1/notifications \
  -H 'Content-Type: application/json' -H 'X-API-Key: dev' \
  -d '{"loginIds":["user-1"],"title":"こんにちは","body":"新しいお知らせがあります","url":"https://example.com/inbox"}'
```

## 別サービス用のアプリを作る

`app/` はテンプレートです。`app/.env` (または EAS の環境変数) で次を差し替えるだけで
別のアプリになります。詳細は [`app/README.md`](app/README.md)。

| 変数 | 内容 |
|---|---|
| `APP_NAME` / `APP_SLUG` / `APP_SCHEME` | アプリ名・slug・URL スキーム |
| `APP_ICON` / `APP_ADAPTIVE_ICON_*` / `APP_SPLASH_*` | アイコンとスプラッシュ |
| `IOS_BUNDLE_ID` / `ANDROID_PACKAGE` | バンドル ID |
| `WEBAPP_URL` / `ALLOWED_ORIGINS` | 表示する URL と許可 origin |
| `API_BASE_URL` | バックエンド URL |
| `EAS_PROJECT_ID` | Expo Push Token に必要 |

## 開発タスク

| タスク | 内容 |
|---|---|
| `mise run gen` | ent + OpenAPI (Go / TS) 生成 |
| `mise run check` | vet + test + typecheck |
| `mise run backend:run` / `backend:test` / `backend:lint` | バックエンド |
| `mise run db:diff <name> --dialect sqlite\|mysql\|postgres` | ent スキーマから migration 生成 |
| `mise run db:apply` / `db:status` / `db:lint` | Atlas で migration 適用・確認 |
| `mise run app:start` / `app:typecheck` / `app:build:android` / `app:build:ios` | アプリ |

DB は `DB_DIALECT` (sqlite / mysql / postgres) と `DB_DSN`、Atlas 用に `ATLAS_URL` で切り替えます。
mysql / postgres の migration 生成には Atlas の dev database として Docker が必要です。
詳細は [`backend/README.md`](backend/README.md)。

## CI

`.github/workflows/ci.yml` が PR ごとに、生成コードが最新か・vet / lint / test・
CGO なしビルド・migration の整合・アプリの typecheck を検証します。
