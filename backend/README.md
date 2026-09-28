# backend

WebView + プッシュ通知アプリ向けの通知バックエンド。Go 製、API は OpenAPI スキーマ
(`../openapi/openapi.yaml`) から生成、DB は ent + Atlas (sqlite / mysql / postgres)。

## 構成

```
backend/
├── cmd/
│   ├── server/           # サーバー本体 (main)
│   └── migrate/          # migration ファイル生成 CLI (ent + Atlas を Go ライブラリとして呼ぶ)
├── internal/
│   ├── config/            # 環境変数 → Config
│   ├── db/                # dialect ごとに ent.Client を開く
│   ├── ent/                # ent (生成コード)。スキーマは internal/ent/schema/device.go
│   ├── api/                # oapi-codegen 生成コード (../openapi/openapi.yaml から)
│   ├── handler/            # api.StrictServerInterface の実装 (ビジネスロジック)
│   ├── push/                # プッシュ通知の送信 (Sender インターフェース。既定は Expo Push API)
│   └── server/              # http.Handler の組み立て (OpenAPI バリデーション・認証・ロギング)
├── migrations/
│   ├── sqlite/             # versioned migration (.sql + atlas.sum)
│   ├── mysql/
│   └── postgres/
├── atlas.hcl                # atlas CLI 用の env 定義 (apply / status / validate 用)
└── .env.example
```

データモデルは `Device` 1 テーブルのみ (`internal/ent/schema/device.go`)。
`installation_id` (アプリインストール単位、unique) で upsert し、`login_id`
(Web アプリのログイン ID) で通知の宛先を引く。

## セットアップ

リポジトリルートの mise が唯一の入口。

```sh
mise install
mise run gen            # ent + OpenAPI (Go/TS) のコード生成
mise run backend:test
```

Go の `go`/`go generate` は `GOFLAGS=-mod=mod` 前提 ([env] で設定済み)。
`ent` / `oapi-codegen` の CLI は `go.mod` の `tool (...)` ディレクティブで
ピン留めされていて、`go generate` は内部で `go tool ent` / `go tool oapi-codegen`
を呼ぶ (バージョンを変えたいときは `go get -tool <pkg>@<version>`)。

## 環境変数

`.env.example` を参照 (`cp .env.example .env.local` で mise が自動読込)。

| 変数 | 既定値 | 説明 |
|---|---|---|
| `ADDR` | `:8080` | listen アドレス |
| `DB_DIALECT` | `sqlite` | `sqlite` \| `mysql` \| `postgres` |
| `DB_DSN` | `file:data/app.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)` | `database/sql` に渡す DSN (dialect ごとに書式が違う。`.env.example` に例あり) |
| `DB_AUTO_MIGRATE` | `false` | 起動時に `client.Schema.Create` で自動作成する (開発用。本番は Atlas を使う) |
| `API_KEY` | (空) | `X-API-Key` で守られた API (`POST /v1/notifications`, `GET /v1/devices/{id}`) のキー。空だと常に 401 |
| `PUSH_PROVIDER` | `expo` | `expo` \| `native` \| `log` (下記「プッシュ通知プロバイダ」参照) |
| `EXPO_ACCESS_TOKEN` | (空) | Expo Push API のアクセストークン (任意。`PUSH_PROVIDER=expo`) |
| `FCM_SERVICE_ACCOUNT_FILE` | (空) | FCM サービスアカウント JSON のパス (`PUSH_PROVIDER=native`。`FCM_SERVICE_ACCOUNT_JSON` と排他) |
| `FCM_SERVICE_ACCOUNT_JSON` | (空) | FCM サービスアカウント JSON の中身 (同上) |
| `FCM_PROJECT_ID` | (空) | 省略時はサービスアカウント JSON 内の `project_id` を使う |
| `APNS_KEY_FILE` | (空) | APNs 用 `.p8` Auth Key のパス (`PUSH_PROVIDER=native`。`APNS_KEY` と排他) |
| `APNS_KEY` | (空) | APNs 用 `.p8` の中身 (PEM。同上) |
| `APNS_KEY_ID` | (空) | `.p8` に対応する Key ID |
| `APNS_TEAM_ID` | (空) | Apple Developer の Team ID |
| `APNS_TOPIC` | (空) | `apns-topic` に使う値 (通常はアプリのバンドル ID) |
| `APNS_ENVIRONMENT` | `production` | `sandbox` \| `production` |
| `SHUTDOWN_TIMEOUT` | `10s` | graceful shutdown の待ち時間 |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

### プッシュ通知プロバイダ (`PUSH_PROVIDER`)

端末はアプリ登録時に Expo Push Token (`pushToken`) と、取得できればネイティブ
のデバイストークン (`deviceToken`: Android は FCM registration token、iOS は
APNs device token の hex) の両方 (もしくはどちらか一方) を送ってくる
(`POST /v1/devices`。少なくとも一方が必須で、両方とも空だと 400
`invalid_request`)。バックエンドは `PUSH_PROVIDER` に応じてどちらのトークン
を使って送信するかを切り替える。両方登録されている端末はどちらのモードでも
送信できる。

- **`expo` (既定)**: [Expo Push API](https://docs.expo.dev/push-notifications/sending-notifications/)
  を経由して送信する (`internal/push/expo.go`, `push.ExpoSender`)。
  `pushToken` を使う。アプリ側は Expo Application Services (EAS) の push
  token を取得して送ればよく、**Expo Push 自体は無料** (Expo アカウントも
  無料枠で足りる)。バックエンド側の追加の認証情報は不要 (`EXPO_ACCESS_TOKEN`
  は任意)。
- **`native`**: Android は FCM HTTP v1、iOS は APNs に直接送信する
  (`internal/push/fcm.go`, `internal/push/apns.go`, `internal/push/native.go`
  の `push.NativeSender`)。`deviceToken` を使う。**FCM/APNs 自体の利用料は
  無料**だが、それぞれ自分の Firebase プロジェクト / Apple Developer
  Program のアカウントと認証情報が必要になる (下記)。`FCM_*` /
  `APNS_*` のどちらか一方だけ設定してもよい (その OS のみネイティブ送信可能
  になり、もう一方の OS 向けメッセージはエラーになる。両方とも未設定だと
  起動時エラーで落ちる)。
- **`log`**: 送信せずログに出すだけ (`push.LogSender`)。ローカル開発・smoke
  test 向け。

**FCM のサービスアカウントの取得方法**: Firebase コンソール →
対象プロジェクトを開く → 右上の歯車アイコン → 「プロジェクトの設定」→
「サービス アカウント」タブ → 「新しい秘密鍵の生成」。ダウンロードされる
JSON ファイルを `FCM_SERVICE_ACCOUNT_FILE` (ファイルパス) か
`FCM_SERVICE_ACCOUNT_JSON` (中身をそのまま環境変数に) で渡す。

**APNs の鍵の取得方法**: [Apple Developer](https://developer.apple.com/account) →
Certificates, Identifiers & Profiles → Keys → 「+」→ 「Apple Push
Notifications service (APNs)」にチェックを入れて鍵を作成 → ダウンロード
(`.p8`、**ダウンロードできるのは一度だけ**)。`.p8` の中身を `APNS_KEY_FILE`
(ファイルパス) か `APNS_KEY` (PEM をそのまま環境変数に) で渡し、鍵作成画面に
表示される Key ID を `APNS_KEY_ID`、Apple Developer の Membership に表示さ
れる Team ID を `APNS_TEAM_ID`、アプリのバンドル ID を `APNS_TOPIC` に設定
する。開発ビルド (Xcode から直接インストールしたもの、TestFlight/App Store
配布ではないもの) に送る場合は `APNS_ENVIRONMENT=sandbox` にする。

アプリ側は `PUSH_PROVIDER` (app 側の環境変数。バックエンドの
`PUSH_PROVIDER` と揃える) で `pushToken`/`deviceToken` のどちらを取得して
送るかを決める。

## 実行

```sh
mise run backend:run
# もしくは
cd backend && go run ./cmd/server
```

ローカルで一通り動かす例 (sqlite, 自動マイグレーション, push は log 出力):

```sh
API_KEY=dev PUSH_PROVIDER=log DB_AUTO_MIGRATE=true \
  DB_DSN='file:/tmp/app.db?_pragma=foreign_keys(1)' \
  go run ./cmd/server
```

## コード生成

`mise run gen` で ent + OpenAPI (Go/TS) をまとめて生成する。個別には:

```sh
mise run gen:ent          # internal/ent/schema/*.go → internal/ent/*.go
mise run gen:openapi:go   # openapi/openapi.yaml → internal/api/api.gen.go
mise run gen:openapi:ts   # openapi/openapi.yaml → app/src/api/schema.d.ts
```

`internal/ent/schema/device.go` を変更したら `mise run gen:ent` を実行し、
**続けて migration も再生成する** (下記)。CI は `mise run gen:ent && mise run
gen:openapi:go` を実行して `git diff` が空であることを確認するので、生成し
忘れるとビルドが落ちる。

## Migration (ent + Atlas)

ここで使える Atlas CLI (コミュニティビルド) は、ent の公式ドキュメントが使う
`src = "ent://..."` ローダーに対応していない
(`ent:// scheme is not supported by the community version`)。そのため
migration ファイルの **生成** は Atlas CLI ではなく `cmd/migrate`
(ent + Atlas を Go ライブラリとして呼ぶ内製プログラム、
`entgo.io/ent/dialect/sql/schema.NamedDiff` を使う。
[ent の versioned migration ガイド](https://entgo.io/docs/versioned-migrations)
と同じ考え方) で行う。**適用・検証** は通常どおり Atlas CLI (`atlas.hcl`) を使う。

sqlite は CGO なしの `modernc.org/sqlite` を使うため、`cmd/migrate` は
Atlas のドライバが固定で開く `database/sql` の登録名 `sqlite3`
(歴史的に mattn/go-sqlite3 が使っていた名前) に modernc のドライバを
登録して橋渡ししている (`sql.Register("sqlite3", &sqlite.Driver{})`)。
同様に postgres 側も `postgres` という名前に `jackc/pgx/v5/stdlib` を登録する。

### migration ファイルの生成 (`go run ./cmd/migrate`)

```sh
mise run db:diff init --dialect sqlite
# 実体:
go run ./cmd/migrate diff init --dialect sqlite
```

`--dialect sqlite|mysql|postgres` を取り、`migrations/<dialect>/` に
`<timestamp>_<name>.sql` と `atlas.sum` を書き出す (差分がなければ何も
書き出さずにその旨を表示する)。`--dev-url` で diff 計算に使う dev
database を指定できる (既定値は dialect ごとに以下):

| dialect | 既定の dev-url |
|---|---|
| sqlite | `sqlite://file?mode=memory&_fk=1` |
| mysql | `docker://mysql/8/dev` |
| postgres | `docker://postgres/16/dev?search_path=public` |

**sqlite** はメモリ上で完結するのでこの環境だけで生成・検証できる
(`migrations/sqlite` は実際に生成・確認済み)。**mysql / postgres の
既定 dev-url は Docker (`docker://...`) が必要**。Docker が使えない場合は
`--dev-url` で手元の mysql/postgres インスタンスを指す URL を渡す
(`mysql://user:pass@127.0.0.1:3306/dbname` /
`postgres://user:pass@127.0.0.1:5432/dbname?sslmode=disable&search_path=public`)。
本リポジトリの `migrations/postgres/*.sql` はローカルの PostgreSQL に対して
実際に生成・適用まで確認済み。`migrations/mysql/*.sql` は Docker が使えない
環境で作業したため、ent/Atlas のソースコードを読んで手で書き起こしたもので
**未検証** — mysql 環境がある場合は
`go run ./cmd/migrate diff <name> --dialect mysql --dev-url mysql://...`
を実行し、`migrations/mysql/` を実際の出力で上書き・確認することを推奨する
(既存のダミーファイルは削除してから)。

全 dialect まとめて (mysql/postgres は Docker が要る):

```sh
mise run db:diff:all <name>
```

### 適用・検証 (Atlas CLI, `atlas.hcl`)

```sh
mise run db:apply             # DB_DIALECT (既定 sqlite) / ATLAS_URL で指定した DB に適用
mise run db:status            # 適用状況
mise run db:validate          # migrations/ の atlas.sum 整合性チェック (Atlas Pro 不要)
mise run db:hash              # atlas.sum を再計算 (手で migration ファイルを足した後など)
```

`ATLAS_URL` は `DB_DSN` とは書式が異なる Atlas 用の URL (`.env.example` に
dialect ごとの例あり)。例えば真新しい sqlite ファイルに適用するには:

```sh
ATLAS_URL="sqlite://data/app.db" mise run db:apply
```

CI ではまっさらな一時ファイルに対して次のように適用している
(`.github/workflows/ci.yml` 参照):

```sh
DB_DIALECT=sqlite ATLAS_URL="sqlite://$RUNNER_TEMP/ci.db" mise run db:apply
```

`atlas migrate lint` (`mise run db:lint`) は Atlas v0.38 以降 **Atlas Pro への
ログインが必須**になった機能で (`atlas login` が要る)、このリポジトリの CI
には含めていない。手元で Atlas Pro アカウントがあるなら任意で
`mise run db:lint` を実行できる。

### 生成した migration の検証

`internal/db/migrate_test.go` の `TestSQLiteMigrationMatchesEntSchema` が、
`migrations/sqlite/*.sql` を素の `database/sql` で適用したあと ent
(`client.Schema.Create`) を no-op として実行できること、実際に `Device` を
insert/get できることを検証している。`internal/ent/schema` を変更したら
`go test ./internal/db/...` が (migration を再生成するまで) 失敗するので、
スキーマと migration のズレに気づける。

## API の使い方

OpenAPI 定義は `../openapi/openapi.yaml`。認証は 2 種類:

- `X-API-Key: <API_KEY>` — サーバー間 API (`POST /v1/notifications`,
  `GET /v1/devices/{installationId}`)
- `Authorization: Bearer <token>` — 端末登録・解除 (`POST`/`DELETE
  /v1/devices`)。既定の `handler.AllowAll` はトークンを検証しないが、
  `handler.Authorizer` を実装すれば Web アプリのセッションを検証できる
  (下記)

```sh
# ヘルスチェック
curl localhost:8080/healthz

# 端末登録 (ログイン + プッシュ通知の許可後に呼ぶ想定)
curl -X POST localhost:8080/v1/devices \
  -H 'Content-Type: application/json' \
  -d '{
    "loginId": "u1",
    "installationId": "install-uuid-1",
    "platform": "ios",
    "pushToken": "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"
  }'

# 通知送信 (loginId 単位。同じ loginId に複数端末が登録されていれば全部に送る)
curl -X POST localhost:8080/v1/notifications \
  -H 'X-API-Key: dev' \
  -H 'Content-Type: application/json' \
  -d '{
    "loginIds": ["u1"],
    "title": "お知らせ",
    "body": "新着メッセージがあります",
    "url": "https://example.com/messages/1"
  }'

# 端末解除 (ログアウト時)
curl -X DELETE localhost:8080/v1/devices/install-uuid-1
```

## 差し替えポイント

- **`push.Sender`** (`internal/push/push.go`): プッシュ送信の抽象。標準で
  `push.ExpoSender` (Expo Push API, `expo.go`)、`push.FCMSender` (FCM HTTP
  v1, `fcm.go`)、`push.APNSSender` (APNs token 認証, `apns.go`)、その二つを
  `Message.Platform` で振り分ける `push.NativeSender` (`native.go`) を持つ。
  独自のバックエンドに送りたい場合やテストで送信を記録したい場合は
  `Sender` を実装して `handler.New(..., sender, ...)` に渡す
  (`push.LogSender` は開発用の実装例)。`cmd/server/main.go` の
  `buildSender` が `PUSH_PROVIDER` (`expo`/`native`/`log`) に応じて組み立てる。
- **`handler.Authorizer`** (`internal/handler/auth.go`): 端末登録・解除の
  認可。既定の `handler.AllowAll` は何も検証しない。Web アプリのセッション
  Cookie/JWT などを検証したい場合は `AuthorizeDevice(ctx, loginID, token
  string) error` を実装し、`handler.New(db, sender, myAuthorizer, logger)`
  に渡す。`token` は `Authorization: Bearer <token>` から取り出した値
  (`handler.BearerFromRequest` / `handler.WithBearer`)。
