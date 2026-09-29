# ロードマップ

[English](ROADMAP.md) | 日本語

pushshell が今後備えていくべき機能を、おおよその優先度順に並べたものです。Issue が集まるに
つれて順番は入れ替わります。取り組みたい項目があれば、着手前に Issue を立てる (既存のものが
あればコメントする) ことで、形を揃えてから進められます。追跡 Issue がまだない項目は誰でも
着手できます。

凡例: ✅ 完了 · 🚧 進行中 · ⬜ 未着手

## まず効く 5 つ

かけた手間に対して利用者が増える効果が大きい順です。

1. ✅ **英語 README と OSS の基本ファイル**。英語の `README.md` を正とし、日本語は
   `README.ja.md` に。`CONTRIBUTING.md` / `CODE_OF_CONDUCT.md` / `SECURITY.md` / Issue・PR
   テンプレート / Dependabot を追加。残り: `app/README.md` と `backend/README.md` の英語版。
2. ⬜ **Web 側 SDK を npm で配布** (仮称 `@pushshell/web`)。今は Web アプリが
   `window.ReactNativeWebView.postMessage` を直接叩き、`nativeapp` CustomEvent を自前で待つ
   必要があります。`app/src/bridge.ts` から生成した薄い型付きラッパーで、`registered` で
   resolve し `error` で reject する `pushshell.login(id, token)`、`isNative()` 判定、型付き
   イベント購読を提供します。同時に `ready` イベントに `protocolVersion` を入れて互換性を
   管理できるようにします。
3. ✅ **Universal Links / App Links 対応**。メールや SNS の `https://example.com/...` リンク
   からアプリが立ち上がり、WebView 内でそのページを開くようになりました。`ASSOCIATED_DOMAINS`
   を env で受けて (opt-in) `ios.associatedDomains` と Android の `intentFilters` を設定し、
   指定ドメインは `ALLOWED_ORIGINS` にも自動で追加されます。`apple-app-site-association` /
   `assetlinks.json` のテンプレートと公開手順は `examples/well-known/` にあります。
4. ⬜ **通知の宛先とオプションの拡張**。現状は `loginIds` 宛のみで、TTL / 優先度 / collapse
   key / 画像 / サイレント (data-only) / iOS の `threadId` や `interruptionLevel` がありません。
   `installationIds` 直指定、platform や locale による絞り込み、全端末ブロードキャスト、topic
   購読を追加します。
5. ⬜ **Web Push (VAPID) を第 3 のプラットフォームに**。`Platform` に `web` を足し、Web Push
   の `push.Sender` を追加して、同じ `loginId` API で PWA とネイティブアプリの両方に届くように
   します。

## アプリ側 (`app/`)

- ⬜ **外部 origin の遷移制御**。今は `originWhitelist` 外はそのまま拒否されます。
  `onShouldStartLoadWithRequest` で他 origin を `expo-web-browser` (SFSafariViewController /
  Custom Tabs) に飛ばす、`target=_blank` を扱う、OAuth のコールバック origin を許可リストに
  追加しやすくする。
- ⬜ **WebView の権限まわり**。ファイルアップロード、カメラ、位置情報、マイクは Android で
  権限要求の実装が、iOS で `Info.plist` の説明文が必要です。env で有効化できるようにして
  「コード変更なし」の方針を守ります。
- ⬜ **bridge メッセージの追加**。Web → ネイティブ: `requestPushPermission` (ログインとは別
  タイミングで権限を求めたい需要は多い)、権限拒否時に設定画面を開く `openSettings`、
  `setBadge` / `clearBadge`、`share`、`haptic`。ネイティブ → Web: `appState` (foreground /
  background) と `pushPermissionChanged`。
- ⬜ **ネイティブ UI 文言の i18n**。「読み込みに失敗しました」「再読み込み」などが日本語固定
  です。最低限 `en` / `ja` を `expo-localization` で切り替えます。
- ⬜ **通知アイコン・色・チャンネル定義を env で**。Android の小アイコンや色、複数チャンネル
  (お知らせ / メッセージなど) を `app.config.ts` から注入できるようにします。
- ⬜ **Cookie / セッション維持**。`sharedCookiesEnabled` と再起動後のログイン維持の挙動、
  `ready` で返す `loginIds` との関係を文書化します。
- ⬜ **テスト**。app は CI で typecheck のみです。`bridge.ts` と `installation.ts` は純粋関数が
  多く Jest で固めやすく、Maestro を既存の emulator ワークフローに載せれば E2E も狙えます。
  iOS ビルドの CI (macOS ランナー) も未整備です。
- ⬜ **`expo-updates` による OTA** (任意。テンプレート利用者には喜ばれます)。

## バックエンド側 (`backend/`)

- ⬜ **Expo のレシート確認**。Expo Push はチケットが同期で返り、`DeviceNotRegistered` は後で
  レシートを取りにいかないと分かりません。レシート取得ジョブがないと Expo 経路の無効トークン
  掃除が漏れます。
- ⬜ **非同期送信とジョブ状態**。1000 件の `loginIds` を同期で送ると HTTP タイムアウトに触れ
  ます。`202` で `notificationId` を返し、`GET /v1/notifications/{id}` で結果を引く形、429 /
  5xx へのバックオフ付きリトライ、`Idempotency-Key` ヘッダの対応。
- ⬜ **配信履歴テーブルと管理 API**。`GET /v1/logins/{loginId}/devices`、端末一覧、送信ログ。
  今は「届かない理由」を調べる手段がプロセスのログしかありません。
- ⬜ **Authorizer の設定駆動実装**。README では「自分で実装して」となっています。JWT (JWKS URL
  指定) 検証と、Web アプリのバックエンドへ問い合わせる `AUTH_VERIFY_URL` 方式を同梱すれば、
  多くの利用者は Go を書かずに済みます。
- ⬜ **マルチアプリ / マルチテナント**。API キーを複数持ち、キーごとに app を分けて 1 つの
  バックエンドで複数サービスを扱えるようにします。キーはハッシュ保存とローテーション対応に。
- ⬜ **Webhook**。トークン失効で端末が削除された、登録が増えた、といったイベントを Web
  アプリ側へ通知し、双方向の連携を完成させます。
- ⬜ **運用系**。レート制限、CORS 設定、DB 接続確認付きの `/readyz`、Prometheus /
  OpenTelemetry メトリクス、`last_seen_at` による古い端末の掃除、同じ push token が別
  `installationId` で再登録された場合 (再インストール) の重複整理。
- ⬜ **配布**。`Dockerfile` と GHCR イメージ、`docker compose up` 一発の開発環境、goreleaser の
  バイナリ配布、Fly.io / Railway / Render のデプロイテンプレート。Cloudflare Workers 対応は
  すでにある強い差別化なので前面に出します。
- ⬜ **クライアント SDK と CLI**。OpenAPI から Go / TypeScript / Python クライアントを生成して
  公開し、`pushshell send --login user-1 --title ...` のような CLI で動作確認とデモを速くします。

## プロジェクト運営

- ⬜ **ホスティングされたデモ**。公開デモページと配布用 APK、README に短い GIF。
- ⬜ **位置づけ**。Median / GoNative、Capacitor、Hotwire Native、OneSignal との比較表と、
  pushshell が違う点。
- ⬜ **フレームワーク別の導入ガイド**。Next.js / Rails / Laravel / Django のスニペットを README
  から辿れるように。
- ⬜ **リリース自動化**。release-please など、タグ、`CHANGELOG.md`。貢献者が入ってきたときに
  リリースの手間を小さくします。
- ⬜ **`app/README.md` と `backend/README.md` の英語版**。
- ⬜ **ラベル**。`good first issue` / `help wanted` をこの一覧の項目に対応させます。
