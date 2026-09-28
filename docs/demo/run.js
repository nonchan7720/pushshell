// examples/webapp/index.html を Chromium で開き、ネイティブ側 (App.tsx の役割) をモックして
// 実際のバックエンドと通信させるデモ。スクリーンショットを docs/demo/ に保存する。
const { chromium } = require("playwright");
const fs = require("fs");
const path = require("path");

const API = "http://localhost:8080";
const API_KEY = "demo-api-key";
const OUT = __dirname;
const PAGE = "file://" + path.resolve(__dirname, "../../examples/webapp/index.html");
const installationId = "demo-installation-0001";
const evidence = [];
const log = (title, body) => evidence.push({ title, body });

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({
    viewport: { width: 390, height: 844 },
    deviceScaleFactor: 2,
    isMobile: true,
    hasTouch: true,
  });

  const dispatch = (detail) =>
    page.evaluate((d) => window.dispatchEvent(new CustomEvent("nativeapp", { detail: d })), detail);

  // ネイティブ側の振る舞い (app/App.tsx) を Node 側で模倣する
  await page.exposeFunction("__nativeReceive", async (raw) => {
    const msg = JSON.parse(raw);
    switch (msg.type) {
      case "getState":
        await dispatch({ type: "ready", platform: "android", appVersion: "1.0.0", installationId, pushPermission: "granted" });
        break;
      case "login": {
        const body = {
          loginId: msg.loginId, installationId, platform: "android",
          pushToken: "ExponentPushToken[demo-xxxxxxxxxxxxxxxxxxxx]",
          appId: "com.example.pushshell", appVersion: "1.0.0", buildNumber: "1",
          osVersion: "14", deviceModel: "Pixel 8", locale: "ja-JP",
        };
        const res = await fetch(`${API}/v1/devices`, {
          method: "POST",
          headers: { "Content-Type": "application/json", ...(msg.token ? { Authorization: `Bearer ${msg.token}` } : {}) },
          body: JSON.stringify(body),
        });
        const json = await res.json();
        log(`POST /v1/devices → ${res.status}`, JSON.stringify(json, null, 2));
        if (res.ok) await dispatch({ type: "registered", loginId: msg.loginId });
        else await dispatch({ type: "error", message: json.message });
        break;
      }
      case "logout": {
        const res = await fetch(`${API}/v1/devices/${installationId}`, { method: "DELETE" });
        log(`DELETE /v1/devices/${installationId} → ${res.status}`, "");
        break;
      }
      case "openExternal":
        log(`openExternal`, `Linking.openURL(${msg.url}) (モック: 何もしない)`);
        break;
    }
  });
  await page.addInitScript(() => {
    window.NativeApp = { isNative: true, platform: "android", appVersion: "1.0.0" };
    window.ReactNativeWebView = { postMessage: (raw) => window.__nativeReceive(raw) };
  });

  await page.goto(PAGE);
  await page.click("#getStateBtn");
  await page.waitForTimeout(500);
  await page.screenshot({ path: path.join(OUT, "01-ready.png"), fullPage: true });

  await page.fill("#loginId", "user-123");
  await page.fill("#token", "session-token-abc");
  await page.click("#loginBtn");
  await page.waitForFunction(() => document.querySelector("#log").textContent.includes("registered"));
  await page.waitForTimeout(300);
  await page.screenshot({ path: path.join(OUT, "02-login-registered.png"), fullPage: true });

  // サーバー間 API から通知を送る (バックエンドは PUSH_PROVIDER=log なので Expo には投げずログ出力)
  const send = await fetch(`${API}/v1/notifications`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-API-Key": API_KEY },
    body: JSON.stringify({ loginIds: ["user-123"], title: "新着メッセージ", body: "田中さんからメッセージが届きました", url: "https://example.com/inbox/42", data: { kind: "message", id: 42 } }),
  });
  const sendJson = await send.json();
  log(`POST /v1/notifications (X-API-Key) → ${send.status}`, JSON.stringify(sendJson, null, 2));
  const unauth = await fetch(`${API}/v1/notifications`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ loginIds: ["user-123"], title: "x" }) });
  log(`POST /v1/notifications (API キーなし) → ${unauth.status}`, JSON.stringify(await unauth.json()));

  // 通知タップ時にネイティブが Web へ流すイベントを再現
  await dispatch({ type: "notification", data: { url: "https://example.com/inbox/42", kind: "message", id: 42 } });
  await page.waitForTimeout(300);
  await page.screenshot({ path: path.join(OUT, "03-notification-tapped.png"), fullPage: true });

  await page.click("#logoutBtn");
  await page.waitForTimeout(500);
  const after = await fetch(`${API}/v1/devices/${installationId}`, { headers: { "X-API-Key": API_KEY } });
  log(`GET /v1/devices/${installationId} (logout 後) → ${after.status}`, JSON.stringify(await after.json()));

  fs.writeFileSync(path.join(__dirname, "evidence.json"), JSON.stringify(evidence, null, 2));
  await browser.close();
})().catch((e) => { console.error(e); process.exit(1); });
