# pushshell (プッシェル)

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

## テンプレートとして使う

このリポジトリは GitHub の Template repository です。自分のアプリを作るときは
fork ではなく **「Use this template」** で新しいリポジトリを作ってください
(履歴を引き継がず、private にもできます。fork は本リポジトリへの貢献用です)。

作成後に差し替えるのは次の 4 か所だけです。コードの変更は要りません。

1. **アプリの設定** (`app/.env`、`app/.env.example` からコピー): 下表の変数。
   アイコン画像は `app/assets/` に置いて `APP_ICON` などで指す。
2. **バックエンドの設定**: `backend/.env` (`backend/.env.example` から) の
   `API_KEY` / `DB_*` / `PUSH_PROVIDER` と FCM / APNs の資格情報。
   Cloudflare Workers で動かすなら `backend/worker/wrangler.toml` の
   `database_id` と `wrangler secret put` (詳細は
   [`backend/README.md`](backend/README.md))。
3. **GitHub Actions の Secrets** (release ビルドを CI で作る場合):
   `ANDROID_KEYSTORE_BASE64` / `ANDROID_KEYSTORE_PASSWORD` /
   `ANDROID_KEY_ALIAS` / `ANDROID_KEY_PASSWORD`、FCM を使うなら
   `GOOGLE_SERVICES_JSON_BASE64`。
4. **識別子の置換** (任意): Go のモジュールパス
   `github.com/nonchan7720/pushshell/backend` はモノレポ内でしか import
   しないのでそのままでもビルド・動作しますが、自分の名前に揃えるなら
   `pushshell` / `com.example.pushshell` / `nonchan7720/pushshell` を
   一括置換して `mise run gen` を実行してください (ent の生成コードにも
   モジュールパスが入っています)。

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

## Android をローカルでビルド・実行する

JDK 17 は mise が入れます。Android SDK は mise のプラグインで管理できないため、
`mise run android:sdk` が `$ANDROID_HOME` (既定 `~/.android-sdk`) に必要なパッケージ
(platform-tools, platform 36, build-tools 36.0.0, NDK 27.1, cmake 3.22.1) をインストールします。
環境変数 (`ANDROID_HOME`, `PATH`) は `.mise.toml` の `[env]` で設定済みです。

```sh
mise install                        # JDK 17 など
mise run android:sdk                # Android SDK 本体
mise run app:build:android:local    # expo prebuild + gradlew assembleDebug → app/android/app/build/outputs/apk/debug/

# エミュレータで動かす場合
mise run android:avd:create         # emulator + system image + AVD 作成
mise run android:emulator           # 起動 (KVM が無ければソフトウェアエミュレーション)
mise run app:install:android        # APK をインストール
mise run app:run:android            # Metro 付きで実行 (expo run:android)
```

実機なら USB デバッグを有効にして `mise run android:devices` で見えることを確認し、
`mise run app:run:android` を実行してください。

エミュレータ関連の環境変数 (`.mise.toml` の `[env]`、`.env` で上書き可):

| 変数 | 内容 | 既定 |
|---|---|---|
| `ANDROID_SYSTEM_IMAGE_TAG` | `google_apis` (Play services あり。FCM の確認に必要) か `default` (AOSP のみ。軽い) | `google_apis` |
| `ANDROID_AVD_NAME` | AVD 名 | `pushshell` |
| `ANDROID_EMULATOR_EXTRA_ARGS` | `emulator` に渡す追加引数 (例: `-cores 2 -memory 3072`) | なし |

KVM が使えない環境 (CI コンテナなど) ではソフトウェアエミュレーションになり、起動に 20〜30 分かかることがあります。
その場合は `ANDROID_SYSTEM_IMAGE_TAG=default` と `-cores 2` 程度に抑えると安定します。

## 開発タスク

| タスク | 内容 |
|---|---|
| `mise run gen` | ent + OpenAPI (Go / TS) 生成 |
| `mise run check` | vet + test + typecheck |
| `mise run backend:run` / `backend:test` / `backend:lint` | バックエンド |
| `mise run db:diff <name> --dialect sqlite\|mysql\|postgres` | ent スキーマから migration 生成 |
| `mise run db:apply` / `db:status` / `db:lint` | Atlas で migration 適用・確認 |
| `mise run app:start` / `app:typecheck` / `app:build:android` / `app:build:ios` | アプリ (EAS Build) |
| `mise run android:sdk` / `android:avd:create` / `android:emulator` / `android:devices` | Android SDK とエミュレータ |
| `mise run app:build:android:local` / `app:build:android:bundle` / `app:install:android` / `app:run:android` | ローカル Android ビルド (APK / AAB)・実行 |

DB は `DB_DIALECT` (sqlite / mysql / postgres) と `DB_DSN`、Atlas 用に `ATLAS_URL` で切り替えます。
mysql / postgres の migration 生成には Atlas の dev database として Docker が必要です。
詳細は [`backend/README.md`](backend/README.md)。

## CI

| ワークフロー | トリガー | 内容 |
|---|---|---|
| `ci.yml` | PR / main への push | `backend`: 生成コードが最新か、vet / lint / test、CGO なしビルド、migration の整合。`worker`: wasm ビルド、ent / atlas / kin-openapi 非リンクの確認、ビルド済み wasm を wrangler dev (workerd) + ローカル D1 で起動して API を叩くスモークテスト、gzip 3MB の size gate。`app`: 生成型が最新か、typecheck |
| `android-apk.yml` (Android Build) | 手動 (`workflow_dispatch`) | input で `APP_NAME` / `ANDROID_PACKAGE` / `WEBAPP_URL` / `API_BASE_URL` / `PUSH_PROVIDER` などと成果物の種類 (`apk` / `aab` / `both`) を指定してビルドし Artifact に保存。Google Play の新規アプリは AAB 必須。Secrets に `ANDROID_KEYSTORE_BASE64` 等があれば release 署名、`GOOGLE_SERVICES_JSON_BASE64` があれば FCM 設定を同梱 |
| `android-emulator.yml` | 手動 (`workflow_dispatch`) | KVM を有効化した ubuntu ランナーでエミュレータを起動し、release APK (x86_64) を入れて起動。ランナー上のバックエンド (`PUSH_PROVIDER=log`) と `examples/webapp` に `10.0.2.2` で接続し、スクリーンショットと logcat を Artifact に保存 |

`android-apk.yml` は `workflow_call` でも呼べるので、他のワークフローから再利用できます。
アクションはすべてコミット SHA で固定しています。

エミュレータのスモークテスト本体は `scripts/android/emulator-smoke.sh` で、ローカルでも
`mise run android:emulator` でエミュレータを起動したあと `mise run android:smoke` で同じ確認ができます。
