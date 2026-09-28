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

再現:

```sh
# 別ターミナルでバックエンドを起動
API_KEY=demo-api-key PUSH_PROVIDER=log DB_AUTO_MIGRATE=true mise run backend:run
# playwright がグローバルにある前提 (npm i -g playwright && npx playwright install chromium)
NODE_PATH=$(npm root -g) node docs/demo/run.js
```
