# app - WebView + プッシュ通知アプリ

このディレクトリは、指定した URL を WebView で表示し、プッシュ通知の受信・登録だけを行う
**テンプレートアプリ** (Expo SDK 57 / React Native) です。アプリ名・アイコン・表示 URL・
バンドル ID は全てビルド時の環境変数で注入するので、この `app/` をコピー (または fork) して
env を変えるだけで別サービス用のアプリを作れます。

UI やログインなどのロジックは一切持たず、すべて WebView の中の Web アプリ (`WEBAPP_URL`) が
担当します。ネイティブ側がやるのは:

- Web アプリを WebView で表示する
- Web アプリがログインしたら `postMessage` でログイン ID を受け取り、端末情報と一緒に
  バックエンドへ登録する (`POST /v1/devices`)
- プッシュ通知を受信・表示し、タップされたら Web アプリを対応する URL に遷移させる

## 1. 環境変数で設定する

`.env.example` をコピーして使います。

```sh
cp .env.example .env
# .env を編集
```

`app.config.ts` が起動時に読む変数一覧 (詳細はコメント付きで `.env.example` を参照):

| 変数 | 説明 | デフォルト |
|---|---|---|
| `APP_NAME` | アプリ名 | `WebApp` |
| `APP_SLUG` | Expo の slug | `webapp-notification` |
| `APP_SCHEME` | カスタム URL スキーム | `webappnotification` |
| `APP_VERSION` | アプリバージョン (`runtimeVersion` にも使う) | `1.0.0` |
| `APP_ICON` | アプリアイコン画像のパス | `./assets/icon.png` |
| `APP_ADAPTIVE_ICON_FOREGROUND` | Android adaptive icon の前景画像 | `./assets/android-icon-foreground.png` |
| `APP_ADAPTIVE_ICON_BACKGROUND` | Android adaptive icon の背景色 | `#FFFFFF` |
| `APP_ADAPTIVE_ICON_MONOCHROME` | Android 13+ monochrome icon | `./assets/android-icon-monochrome.png` |
| `APP_SPLASH_IMAGE` | スプラッシュ画像 | `./assets/splash-icon.png` |
| `APP_SPLASH_BACKGROUND` | スプラッシュ背景色 | `#FFFFFF` |
| `IOS_BUNDLE_ID` | iOS bundle identifier | `com.example.webappnotification` |
| `ANDROID_PACKAGE` | Android package name | `com.example.webappnotification` |
| `WEBAPP_URL` | WebView に表示する Web アプリの URL | `https://example.com` |
| `API_BASE_URL` | バックエンド (Go) の URL | `http://localhost:8080` |
| `ALLOWED_ORIGINS` | WebView / bridge が許可する origin (カンマ区切り) | `WEBAPP_URL` の origin |
| `EAS_PROJECT_ID` | EAS の projectId (Expo Push Token の取得に必須) | なし |
| `GOOGLE_SERVICES_JSON` | Android で FCM を使う場合の `google-services.json` パス | なし |

`assets/` に置いてある PNG (icon.png, android-icon-*.png, splash-icon.png, favicon.png) は
**テンプレート用のプレースホルダー**です。実際のアプリでは、このファイル自体を差し替えるか、
上記の env でパスを別ファイルに向けてください。

`app.json` は使わず `app.config.ts` に一本化しています。設定の妥当性は次で確認できます。

```sh
npx expo config --type public
npx expo config --type prebuild
```

## 2. 開発ビルドで実行する (dev client)

**Expo Go は SDK 53 以降、Android でリモートプッシュ通知を受信できません**
(`expo-notifications` の `getExpoPushTokenAsync` / リモート通知は Expo Go では動作しない、
という制限が Expo 側にあります)。そのためこのアプリは `expo-dev-client` を使う前提です。
プッシュ通知の動作確認をするには、必ず開発ビルド (dev client) か実機ビルドを使ってください。

```sh
# ネイティブプロジェクトを生成 (android/ , ios/) - env を変えたら毎回やり直す
npx expo prebuild --clean

# 実機 / エミュレータで起動 (ネイティブ SDK が必要)
npx expo run:android
npx expo run:ios

# 以後は Metro だけ再起動すればよい
npx expo start --dev-client
```

型チェック:

```sh
npm run typecheck
```

### Android SDK を mise で用意する

JDK 17 は `mise install` で入ります。Android SDK (platform 36 / build-tools 36.0.0 / NDK 27.1 / cmake)
はリポジトリ直下で次を実行すると `~/.android-sdk` にインストールされ、`ANDROID_HOME` と `PATH` は
`.mise.toml` が設定します。

```sh
mise run android:sdk                 # SDK 本体
mise run app:build:android:local     # prebuild + gradlew assembleDebug で debug APK を作る
mise run android:avd:create          # エミュレータ + system image + AVD (任意)
mise run android:emulator            # エミュレータ起動 (KVM が無ければソフトウェアエミュレーション)
mise run app:run:android             # Metro 付きで実行
```

## 3. EAS Build

```sh
npm install -g eas-cli   # もしくは npx eas-cli
eas login
eas init                 # EAS_PROJECT_ID が発行される (.env / eas.json に設定する)

eas build --platform android --profile preview
eas build --platform ios --profile preview
```

`eas.json` の `build.<profile>.env` にビルド時の環境変数を書けます (このリポジトリでは
サンプル値のみ入れてあります)。実際の値は秘密にしたいものも多いので、コミットせず
[EAS の Environment Variables](https://docs.expo.dev/eas/environment-variables/) で
プロジェクトに登録するか、CI 上で `.env` を生成することを推奨します。

## 4. Web アプリ ⇔ ネイティブの postMessage プロトコル (bridge)

プロトコルの正式な定義とコメントは [`src/bridge.ts`](./src/bridge.ts) にあります。
Web アプリ側の実装者はこのファイルだけ読めば連携できるはずです。要約:

**Web → ネイティブ** (`window.ReactNativeWebView.postMessage(JSON.stringify(msg))`):

- `{ type: "login", loginId, token? }` — ログイン時。ネイティブがバックエンドに端末を登録する
- `{ type: "logout" }` — ログアウト時。ネイティブがバックエンドの登録を解除する
- `{ type: "openExternal", url }` — システムブラウザで URL を開く
- `{ type: "getState" }` — 現在の状態を問い合わせる (`ready` が返る)

**ネイティブ → Web** (`window.addEventListener("nativeapp", (e) => { e.detail })`):

- `{ type: "ready", platform, appVersion, installationId, pushPermission }`
- `{ type: "registered", loginId }` — 端末登録が成功した
- `{ type: "error", message }` — login/logout の処理が失敗した
- `{ type: "notification", data }` — 通知がタップされた (`data.url` が許可 origin なら自動遷移もする)

さらに、ページ読み込み前に `window.NativeApp = { isNative: true, platform, appVersion }` が
注入されるので、Web アプリはこれの有無でネイティブアプリ内かどうかを判定できます。

**動作確認用ページ**: [`examples/webapp/index.html`](../examples/webapp/index.html)
(リポジトリルート) はビルド不要の静的 HTML で、login/logout/openExternal/getState を送信する
ボタンと、受信した `nativeapp` イベントのログ表示があります。これを配信するサーバーの URL を
`WEBAPP_URL` に設定して dev client で開けば、bridge の動作を手軽に確認できます。

## 5. バックエンドの呼び出し

`src/api/client.ts` が [`../openapi/openapi.yaml`](../openapi/openapi.yaml) から生成した型
(`src/api/schema.d.ts`, `npm run gen:api` で再生成) を使って `POST /v1/devices` /
`DELETE /v1/devices/{installationId}` を呼びます。`login` メッセージの `token` は、そのまま
`Authorization: Bearer <token>` としてバックエンドに送られます
(バックエンドの `Authorizer` 実装次第で検証されるかどうかが決まる。デフォルトは検証なし)。

端末登録に使う値の組み立ては [`src/notifications.ts`](./src/notifications.ts)
(`buildDeviceRegistration`) を参照してください。

## 6. バックエンドからテスト通知を送る

バックエンドを起動しておきます (詳細は `backend/README.md` 等を参照):

```sh
API_KEY=dev PUSH_PROVIDER=log DB_AUTO_MIGRATE=true mise run backend:run
```

アプリでログインして端末登録が終わったら、そのログイン ID 宛に通知を送れます
(`API_KEY` はバックエンドの `X-API-Key` 用の環境変数と同じ値):

```sh
curl -X POST http://localhost:8080/v1/notifications \
  -H 'X-API-Key: dev' \
  -H 'Content-Type: application/json' \
  -d '{
    "loginIds": ["user-123"],
    "title": "テスト通知",
    "body": "こんにちは",
    "url": "https://example.com/inbox"
  }'
```

`PUSH_PROVIDER=log` の場合は実際には送信されず、送信内容がバックエンドのログに出力されます。
実機でプッシュ通知を受け取って確認したい場合は `PUSH_PROVIDER=expo` にしてください
(必要なら `EXPO_ACCESS_TOKEN` も設定)。

## ディレクトリ構成

```
app.config.ts            アプリ設定 (env を読んで expo config を組み立てる)
src/config.ts             extra の型付きアクセサ
src/api/schema.d.ts        openapi-typescript の生成物 (npm run gen:api で再生成)
src/api/client.ts          openapi-fetch クライアント + registerDevice / unregisterDevice
src/bridge.ts               Web ⇔ ネイティブの postMessage プロトコル定義
src/installation.ts         installationId / ログイン状態の永続化 (expo-secure-store)
src/notifications.ts         通知権限・push token 取得・DeviceRegistration の組み立て
src/useNotificationNavigation.ts  通知タップ時の WebView 遷移 hook
App.tsx                     WebView 画面本体
eas.json                    EAS Build のプロファイル
```
