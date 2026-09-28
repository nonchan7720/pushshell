# backend

WebView + プッシュ通知アプリ向けの通知バックエンド。Go 製、API は OpenAPI スキーマ
(`../openapi/openapi.yaml`) から生成、DB は ent + Atlas (sqlite / mysql / postgres)。

## 構成

```
backend/
├── cmd/
│   ├── server/           # サーバー本体 (main)。net/http + sqlite/mysql/postgres
│   ├── worker/           # Cloudflare Workers + D1 向け entrypoint (js/wasm 専用。後述)
│   └── migrate/          # migration ファイル生成 CLI (ent + Atlas を Go ライブラリとして呼ぶ)
├── internal/
│   ├── app/                # cmd/server・cmd/worker 共通の組み立て (push.Sender / logger)
│   ├── config/            # 環境変数 → Config
│   ├── db/                # dialect ごとに ent.Client を開く (cmd/server 専用。cmd/worker は D1 を直接開く)
│   ├── ent/                # ent (生成コード)。スキーマは internal/ent/schema/{device,device_login}.go
│   ├── api/                # oapi-codegen 生成コード (../openapi/openapi.yaml から)
│   ├── handler/            # api.StrictServerInterface の実装 (ビジネスロジック)
│   ├── push/                # プッシュ通知の送信 (Sender インターフェース。既定は Expo Push API)
│   └── server/              # http.Handler の組み立て (OpenAPI バリデーション・認証・ロギング)
├── migrations/
│   ├── sqlite/             # versioned migration (.sql + atlas.sum)
│   ├── mysql/
│   └── postgres/
├── worker/                  # cmd/worker を Cloudflare Workers + D1 にデプロイする wrangler プロジェクト (後述)
├── atlas.hcl                # atlas CLI 用の env 定義 (apply / status / validate 用)
└── .env.example
```

`cmd/server` (net/http + sqlite/mysql/postgres、通常のデプロイ先) と
`cmd/worker` (Cloudflare Workers + D1) はどちらも `internal/handler` /
`internal/server` / `internal/push` / `internal/api` / `internal/ent` を
まったく同じコードで使う (フォークしていない)。両者が異なるのは DB の
開き方 (`internal/db.Open` vs D1) と `push.Sender` の組み立て方の
入口 (`internal/app.BuildSender` は共通) だけ。詳細は
「[Cloudflare Workers + D1 で動かす](#cloudflare-workers--d1-で動かす)」を参照。

データモデルは `Device` と `DeviceLogin` の 2 テーブル
(`internal/ent/schema/device.go`, `internal/ent/schema/device_login.go`)。
`Device` は `installation_id` (アプリインストール単位、unique) で upsert される
端末そのもの (トークン・端末情報)。`DeviceLogin` は `Device` と Web アプリの
ログイン ID を結ぶ中間テーブルで、**端末とログイン ID は多対多**
(1 端末に複数アカウントがログインしていてもよいし、1 アカウントが複数端末
[スマホ + タブレットなど] を持っていてもよい)。`Device` の削除は
`DeviceLogin` を `ON DELETE CASCADE` で道連れにする。通知はログイン ID から
`DeviceLogin` 経由で端末を引く (`POST /v1/notifications`)。

- `POST /v1/devices` (`registerDevice`): `installationId` で端末を upsert し、
  `loginId` をその端末に**追加で**紐付ける (既存の紐付けは消えない。同じ
  `installationId` に 2 つ目、3 つ目の `loginId` を登録すると、その端末は
  複数アカウント分の通知を受け取るようになる)。
- `DELETE /v1/devices/{installationId}` (`unregisterDevice`): 端末ごと削除
  (紐付いていた `loginId` も全部消える)。アプリが自分の端末を完全に解除する
  とき (アンインストール相当) に使う。
- `DELETE /v1/devices/{installationId}/logins/{loginId}` (`unregisterDeviceLogin`):
  端末はそのまま残し、指定した `loginId` との紐付けだけを外す。**通常の
  ログアウトはこちら** — 端末自体・他のログイン ID の紐付け・トークンは
  影響を受けない。アプリ本体から呼ぶほか、Web アプリのバックエンドが
  セッション失効を検知したときに API キーで呼んでもよい (`installationId`
  は bridge の `ready` イベントで Web アプリ側に渡る)。
- `DELETE /v1/logins/{loginId}` (`unregisterLogin`、`apiKeyAuth` のみ):
  そのログイン ID を**全端末から**外す (サーバー間)。Web アプリ側でセッション
  が失効・無効化された (全端末ログアウト、退会など) ときに Web アプリの
  バックエンドから呼ぶ。応答は `{ "removed": <紐付けを外した端末数> }`。

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

`--drop-column` / `--drop-index` (既定は両方 false。ent 自体の既定と同じ)
を渡さない限り、ent スキーマから消したフィールド・インデックスがあっても
生成される migration はそれに対応する列・インデックスを DB から
消さない (フィールドをうっかり消しても既存データが暗黙に失われないための
安全策)。フィールドを本当に削除する migration を作るときだけ明示的に
両方 (または該当する方) を付ける。

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
  `GET /v1/devices/{installationId}`, `DELETE /v1/logins/{loginId}`。
  `DELETE /v1/devices/{installationId}/logins/{loginId}` は Bearer との
  どちらでも可)
- `Authorization: Bearer <token>` — 端末登録・解除
  (`POST /v1/devices`, `DELETE /v1/devices/{installationId}`,
  `DELETE /v1/devices/{installationId}/logins/{loginId}`)。既定の
  `handler.AllowAll` はトークンを検証しないが、`handler.Authorizer` を
  実装すれば Web アプリのセッションを検証できる (下記)

```sh
# ヘルスチェック
curl localhost:8080/healthz

# 端末登録 (ログイン + プッシュ通知の許可後に呼ぶ想定)。
# 同じ installationId に別の loginId を登録すると「追加」される (多対多)。
curl -X POST localhost:8080/v1/devices \
  -H 'Content-Type: application/json' \
  -d '{
    "loginId": "u1",
    "installationId": "install-uuid-1",
    "platform": "ios",
    "pushToken": "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"
  }'

# 通知送信 (loginId 単位。同じ loginId に複数端末が登録されていれば全部に送る。
# 1 端末が複数の指定 loginId を持っていても通知は 1 通にまとめられる)
curl -X POST localhost:8080/v1/notifications \
  -H 'X-API-Key: dev' \
  -H 'Content-Type: application/json' \
  -d '{
    "loginIds": ["u1"],
    "title": "お知らせ",
    "body": "新着メッセージがあります",
    "url": "https://example.com/messages/1"
  }'

# ログアウト (このアカウントの紐付けだけ外す。端末自体・他のアカウントの紐付けは残る)
curl -X DELETE localhost:8080/v1/devices/install-uuid-1/logins/u1

# 端末を丸ごと解除 (アプリのアンインストール相当。紐付いていた全 loginId ごと消える)
curl -X DELETE localhost:8080/v1/devices/install-uuid-1

# Web アプリ側のセッション失効時にバックエンドから呼ぶ (全端末からそのログイン ID を外す)
curl -X DELETE localhost:8080/v1/logins/u1 -H 'X-API-Key: dev'
```

## 差し替えポイント

- **`push.Sender`** (`internal/push/push.go`): プッシュ送信の抽象。標準で
  `push.ExpoSender` (Expo Push API, `expo.go`)、`push.FCMSender` (FCM HTTP
  v1, `fcm.go`)、`push.APNSSender` (APNs token 認証, `apns.go`)、その二つを
  `Message.Platform` で振り分ける `push.NativeSender` (`native.go`) を持つ。
  独自のバックエンドに送りたい場合やテストで送信を記録したい場合は
  `Sender` を実装して `handler.New(..., sender, ...)` に渡す
  (`push.LogSender` は開発用の実装例)。`internal/app.BuildSender`
  (`internal/app/sender.go`。`cmd/server`・`cmd/worker` 共通) が
  `PUSH_PROVIDER` (`expo`/`native`/`log`) に応じて組み立てる。
- **`handler.Authorizer`** (`internal/handler/auth.go`): 端末登録・解除の
  認可。既定の `handler.AllowAll` は何も検証しない。Web アプリのセッション
  Cookie/JWT などを検証したい場合は `AuthorizeDevice(ctx, loginID, token
  string) error` を実装し、`handler.New(db, sender, myAuthorizer, logger)`
  に渡す。`token` は `Authorization: Bearer <token>` から取り出した値
  (`handler.BearerFromRequest` / `handler.WithBearer`)。

## Cloudflare Workers + D1 で動かす

`cmd/server` と全く同じ `internal/handler` / `internal/server` /
`internal/push` / `internal/api` / `internal/ent` を、`cmd/worker`
(`//go:build js && wasm`) から Cloudflare Workers + D1 の上で動かせる。
ビジネスロジックは 1 つも fork していない — 差分は「DB の開き方」
(`internal/db.Open` の代わりに D1 を `database/sql` 越しに直接開く) と
「エントリポイント」(`net/http.Server` の代わりに
[`github.com/syumai/workers-go`](https://pkg.go.dev/github.com/syumai/workers-go)
の `workers.Serve`) だけ。

### アーキテクチャ

```
backend/worker/                  # wrangler プロジェクト (npm)
├── wrangler.toml                 # name / D1 binding / [vars]
├── package.json                  # wrangler (devDependency) + npm scripts
├── build.sh                      # ../cmd/worker を wasm にビルド → build/
├── sync-migrations.sh            # ../migrations/sqlite/*.sql → migrations/NNNN_*.sql
├── migrations/                   # wrangler d1 migrations 用 (コミット済み)
├── .dev.vars.example             # `wrangler dev` のローカル secrets のひな形
└── build/                        # build.sh の生成物 (gitignore。worker.mjs / app.wasm)

backend/cmd/worker/
├── doc.go     (!(js && wasm))    # go build ./... が全 GOOS で通るためのスタブ main
├── main.go    (js && wasm)       # 本体: config.Load → D1 open → handler.New → server.New → workers.Serve
└── d1time.go  (js && wasm)       # D1 ドライバの time.Time 対応 (後述)
```

`go run github.com/syumai/workers-go/cmd/workers-assets-gen -mode=go
-runtime=cloudflare` が `build/worker.mjs` (Workers の fetch イベントを
wasm に転送する JS グルー) と `wasm_exec.js` を生成し、続けて
`GOOS=js GOARCH=wasm go build -o build/app.wasm ./cmd/worker` で本体を wasm
にビルドする (`backend/worker/build.sh`、`mise run worker:build`)。Workers
はリクエストごとに wasm インスタンスを新規生成して `main()` を実行する
(`workers-go` の `worker.mjs` テンプレートの仕様。グローバル状態はリクエスト
間で共有されない) ため、`cmd/worker/main.go` は D1 接続や push.Sender の
組み立てを含め `cmd/server` の `run()` 相当を毎リクエスト実行する。

### D1 まわりの既知の制約と対応

- **DB_AUTO_MIGRATE は使えない**: `cmd/worker` はスキーマの自動作成をしない。
  D1 のスキーマは `wrangler d1 migrations` (`backend/worker/migrations/`、
  下記) で管理する。
- **D1 に対話的トランザクションはない**
  (`d1.Conn.BeginTx` は常にエラーを返す)。`internal/handler` はそもそも
  `client.Tx(ctx)` を呼んでいないので、このバックエンドにとっては実害がない。
- **`time.Time` の変換が必要**: D1 ドライバ
  (`github.com/syumai/workers-go/cloudflare/d1`) はクエリ引数を
  `syscall/js.Value.Call` でそのまま JS に渡すが、`js.ValueOf` は
  `time.Time` (ent の `created_at`/`updated_at`、`internal/ent/schema` の
  `field.Time`) を扱えず、`mise run worker:dev` + `POST /v1/devices` で
  実際に `panic: ValueOf: invalid value` になることを確認した。
  `backend/cmd/worker/d1time.go` が `driver.NamedValueChecker`
  (書き込み時: `time.Time` → RFC3339Nano 文字列) と `driver.Rows` の
  ラッパー (読み込み時: 文字列 → `time.Time`) を挟んで対応している。
  `internal/ent` 側は無変更。
- **サイズ上限**: `mise run worker:build` が実行時に表示する通り、
  `app.wasm` は **約 37MB (raw) / 約 8.2MB (gzip)**。Cloudflare Workers の
  デプロイ上限は無料プランが **3MB (gzip 後)**、有料 (Workers Paid) プランが
  **10MB (gzip 後)**。したがってこの実装は **Workers Paid プランが必須**
  (無料プランでは `wrangler deploy` が上限超過で失敗する)。kin-openapi
  (OpenAPI バリデーション) / ent / apns2 / golang.org/x/oauth2 など依存が
  多く、これは Go (TinyGo ではない) wasm バイナリとしては妥当なサイズ。
  さらに縮めたい場合は TinyGo への移植が考えられるが、`golang.org/x/net/http2`
  や `reflect` を多用する依存 (kin-openapi, ent) との相性次第で別途検証が
  必要 (未検証)。

### 環境変数のマッピング

`cmd/worker` は `internal/config.Load()` を無改造で使う (環境変数は
`os.Getenv` で読む)。Workers ランタイムの `env` オブジェクト
(`wrangler.toml` の `[vars]` / `wrangler secret put` / ローカルの
`.dev.vars`) には `cloudflare.Getenv` でしかアクセスできないので、
`cmd/worker/main.go` の起動時に次の環境変数だけを `os.Setenv` へコピーする
(`workerEnvKeys`)。それ以外の `internal/config` の変数は Workers 上では
意味を持たない (下表)。

| `internal/config` の変数 | Workers 上での扱い |
|---|---|
| `API_KEY` | コピーされる。`wrangler secret put API_KEY` (本番) / `.dev.vars` (ローカル) |
| `PUSH_PROVIDER` | コピーされる。既定は `wrangler.toml` の `[vars]` で `log` |
| `EXPO_ACCESS_TOKEN` | コピーされる (`wrangler secret put`) |
| `FCM_SERVICE_ACCOUNT_JSON` | コピーされる (`wrangler secret put`。中身をそのまま) |
| `FCM_SERVICE_ACCOUNT_FILE` | **使えない** (Workers にファイルシステムはない)。`FCM_SERVICE_ACCOUNT_JSON` を使う |
| `FCM_PROJECT_ID` | コピーされる |
| `APNS_KEY` | コピーされる (`wrangler secret put`。`.p8` の中身をそのまま) |
| `APNS_KEY_FILE` | **使えない**。`APNS_KEY` を使う |
| `APNS_KEY_ID` / `APNS_TEAM_ID` / `APNS_TOPIC` / `APNS_ENVIRONMENT` | コピーされる |
| `LOG_LEVEL` | コピーされる。既定は `wrangler.toml` の `[vars]` で `info` |
| `ADDR` / `SHUTDOWN_TIMEOUT` | コピーされない (Workers はリスンしない。`net/http.Server` を使わない) |
| `DB_DIALECT` / `DB_DSN` / `DB_AUTO_MIGRATE` | コピーされない (D1 を `[[d1_databases]]` binding 経由で直接開く。スキーマは `wrangler d1 migrations` で管理) |

APNs は wasm ビルドでは `apns2` の HTTP/2 直結トランスポートの代わりに
`github.com/syumai/workers-go/cloudflare/fetch` 経由の `*http.Client` を使う
(`internal/push/httpclient_js.go`)。Expo/FCM も同様 — 詳細は次項。

### push.Sender と fetch (重要な実装上の注意)

タスクの前提「Go の `net/http` クライアントは js/wasm では JS の `fetch`
API を使うので、Expo/FCM/APNs はそのまま動くはず」は **実機の Cloudflare
Workers ランタイム (workerd, `wrangler dev` で確認) では成り立たなかった**。
Go 標準の `net/http/roundtrip_js.go` は `fetch(url, options)` の 2 引数形式
で `globalThis.fetch` を呼ぶが、workerd はこれを
`JavaScript error: Illegal invocation` で拒否する (`PUSH_PROVIDER=expo` +
ダミーの `ExponentPushToken[x]` で `POST /v1/notifications` を叩いて実際に
再現・確認した)。`github.com/syumai/workers-go/cloudflare/fetch`
(このライブラリ自身の D1/KV/R2 バインディングが内部で使っているのと同じ
パッケージ) は `Request` オブジェクトを組み立てて `fetch(request, init)`
の形で呼ぶため、これは workerd が受け付ける。そこで
`internal/push/httpclient_js.go` (`//go:build js && wasm`。`_default.go`
側は非 wasm では nil を返す no-op) が `wasmTransport()` として
`cloudflare/fetch` ベースの `http.RoundTripper` を提供し、

- `push.ExpoSender.sendBatch` (`expo.go`)
- `push.FCMSender.sendOne` (`fcm.go`。送信自体だけでなく、
  `NewFCMSender` が作る OAuth2 トークン取得の HTTP 呼び出しにも
  `oauth2.HTTPClient` context 経由で同じ fix を適用している — ここを
  見落とすとトークン取得自体が同じエラーで失敗する)
- `push.APNSSender` (`apns.go`。`apns2.NewTokenClient` の
  `http2.Transport` を差し替え)

の 3 箇所すべてで使っている。`internal/push` の公開 API・cmd/server 側の
挙動は変わらない (`wasmTransport()` は非 wasm ビルドでは常に `nil` を返し、
各 Sender は今まで通り `http.DefaultTransport` を使う)。

### セットアップ

```sh
# 1. wrangler など npm 依存をインストール
mise run worker:install

# 2. D1 データベースを作成 (初回のみ。表示される database_id を
#    backend/worker/wrangler.toml の [[d1_databases]] database_id に貼る)
cd backend/worker && npx wrangler login && npx wrangler d1 create webapp-notification
cd ../..

# 3. secrets (本番)。PUSH_PROVIDER=log 以外を使うなら必要な分だけ
cd backend/worker
npx wrangler secret put API_KEY
# PUSH_PROVIDER=expo なら (任意): npx wrangler secret put EXPO_ACCESS_TOKEN
# PUSH_PROVIDER=native なら (FCM/APNs いずれか、または両方):
#   npx wrangler secret put FCM_SERVICE_ACCOUNT_JSON
#   npx wrangler secret put FCM_PROJECT_ID
#   npx wrangler secret put APNS_KEY
#   npx wrangler secret put APNS_KEY_ID
#   npx wrangler secret put APNS_TEAM_ID
#   npx wrangler secret put APNS_TOPIC
#   npx wrangler secret put APNS_ENVIRONMENT
cd ../..

# 4. PUSH_PROVIDER=native (Expo Push を使わない) にする場合、非秘密値は
#    wrangler.toml の [vars] を編集 (既定は "log")

# 5. D1 migration を本番に適用してデプロイ
mise run worker:migrate:remote
mise run worker:deploy
```

### ローカル開発 (`wrangler dev`)

```sh
mise run worker:install
mise run worker:migrations:sync   # migrations/sqlite/*.sql に変更があれば
mise run worker:migrate:local     # ローカル D1 (Miniflare/workerd) にスキーマ適用
cp backend/worker/.dev.vars.example backend/worker/.dev.vars   # API_KEY=dev など
mise run worker:build
mise run worker:dev               # npx wrangler dev --port 8787
```

`.dev.vars` は `wrangler dev` 用のローカル secrets ファイル
(`wrangler secret put` のローカル版、gitignore 済み)。`PUSH_PROVIDER` /
`LOG_LEVEL` の既定値は `wrangler.toml` の `[vars]` (`log` / `info`) にある
ので、ローカルでも上書きしない限りそのまま使われる。

`mise run worker:build` は `../cmd/worker` を Go の通常ツールチェーンで
wasm ビルドする ([TinyGo ではない](https://github.com/syumai/workers-go) —
`workers-assets-gen -mode=go` を使う)。ent / kin-openapi / apns2 /
golang.org/x/oauth2 などの依存を含むため `app.wasm` は約 37MB (raw) /
約 8.2MB (gzip) になる (ビルドのたびにサイズを表示する) — 上記「サイズ上限」
参照。

### D1 migration (`backend/worker/migrations/`)

D1 は `wrangler d1 migrations` で管理する専用の migration 形式
(`migrations_dir` 配下の `NNNN_<name>.sql`) を使う。手で書く代わりに、
`backend/migrations/sqlite/*.sql` (通常の Atlas 製 migration。
「[Migration (ent + Atlas)](#migration-ent--atlas)」参照) から
`backend/worker/sync-migrations.sh` (`mise run worker:migrations:sync`) で
生成し直し、生成物 (`backend/worker/migrations/*.sql`) はコミットする。
主な書き換え:

- ファイル名をタイムスタンプ付きから `NNNN_<name>.sql` の連番に変える。
- Atlas が生成する `PRAGMA foreign_keys = off;` / `= on;` を D1 が受け付ける
  `PRAGMA defer_foreign_keys = true;` / `= false;` に置き換える (D1 は
  裸の `PRAGMA foreign_keys = ...` を migration 内で拒否するが、
  `defer_foreign_keys` は受け付ける。効果は「そのトランザクション内では
  外部キー制約チェックをコミット時まで遅延する」で、テーブル再構築を
  1 migration = 1 トランザクションで行う Atlas の生成パターンに対しては
  実質同じ意味になる)。

`internal/ent/schema` を変更したときのフロー:
`mise run gen:ent` → `mise run db:diff <name> --dialect sqlite` →
`mise run worker:migrations:sync` → (ローカルで試すなら)
`mise run worker:migrate:local`。

### ローカルでの動作確認 (実施内容)

この実装は `wrangler dev` (workerd) に対して実際に一通り確認済み:

- `GET /healthz` → `200 {"status":"ok"}`
- `POST /v1/devices` (`loginId`+`installationId`+`pushToken`) → `200`、
  `loginIds` に登録した ID が入る
- 同じ `installationId` に 2 つ目の `loginId` を登録 → `loginIds` が
  `["u1","u2"]` に増える (置き換わらない)
- `POST /v1/notifications` を `X-API-Key` なしで叩く → `401`
- `X-API-Key: dev` (`.dev.vars` の `API_KEY=dev`) 付き、
  `PUSH_PROVIDER=log` → `200` + ワーカーのコンソールに
  `"msg":"push (log provider)"` の JSON ログ
- `DELETE /v1/devices/{installationId}/logins/{loginId}` → `204`
- `DELETE /v1/logins/{loginId}` (`X-API-Key`) → `200 {"removed":1}`
- `DELETE /v1/devices/{installationId}` → `204`
- `PUSH_PROVIDER=expo` + ダミーの `ExponentPushToken[x]` で
  `POST /v1/notifications` → Expo API からの **アプリケーションレベルの
  エラー** (`"DeviceNotRegistered: \"ExponentPushToken[x]\" is not a valid
  Expo push token"`) が返ることを確認 (= ワーカーから外部への outbound
  fetch 自体は正常に届いている。トランスポート層のエラーではないことの
  確認が目的)。

未検証: FCM (`PUSH_PROVIDER=native`) の実サービスアカウントを使った送信、
APNs の実鍵を使った送信 (どちらも認証情報が必要なため)。ただし FCM の
OAuth2 トークン取得 / APNs の HTTP クライアントは前項「push.Sender と
fetch」の fix を経由するので、原理的には同じ経路 (cloudflare/fetch) を
通る。`wrangler deploy` による実際の Cloudflare へのデプロイも未検証
(D1 データベース作成や `wrangler login` などアカウント操作が要るため)。
