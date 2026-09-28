# デモ (ブリッジ + バックエンドの動作証跡)

`examples/webapp/index.html` を Chromium (Playwright) で開き、**ネイティブ側 (app/App.tsx) の
処理を Node で模倣**して、実際に起動したバックエンドと通信させたもの。
実機やエミュレーターのスクリーンショットではない。

| ファイル | 内容 |
|---|---|
| `01-ready.png` | `window.NativeApp` 検出と `getState` → `ready` イベント |
| `02-login-registered.png` | `login` 送信 → バックエンド `POST /v1/devices` 200 → `registered` イベント |
| `03-notification-tapped.png` | `POST /v1/notifications` 送信後、通知タップ相当の `notification` イベント |
| `evidence.json` | 各 API 呼び出しのステータスとレスポンス |
| `04-emulator-dev-client.png` | **エミュレータ (Android 16 x86_64, KVM なし) で debug APK を起動**した実画面。env で注入したアプリ名 `WebApp` と runtime 1.0.0 が Expo 開発者メニューに出ている。この直後に WebView の初期化で libwebviewchromium が CHECK 失敗 (SIGTRAP) し、ページは表示できなかった (ソフトウェアエミュレーション起因と推定。詳細は PR 参照) |
| `06-ci-emulator-webview.png` | **GitHub Actions (KVM 有効の ubuntu ランナー) のエミュレータで release APK を起動**した実画面 (`android-emulator.yml`)。WebView に `examples/webapp` が表示され、`window.NativeApp` を検出している。被さっているダイアログはエミュレータの SystemUI の ANR で、アプリのものではない (以後のスモークテストではエラーダイアログを非表示にしている) |
| `05-emulator-network-error.png` | 同エミュレータで dev client が Metro (10.0.2.2:8081) に到達できなかった際のエラー画面。`adb reverse` で回避した |

再現:

```sh
# 別ターミナルでバックエンドを起動
API_KEY=demo-api-key PUSH_PROVIDER=log DB_AUTO_MIGRATE=true mise run backend:run
# playwright がグローバルにある前提 (npm i -g playwright && npx playwright install chromium)
NODE_PATH=$(npm root -g) node docs/demo/run.js
```
