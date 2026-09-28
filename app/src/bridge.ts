/**
 * src/bridge.ts
 *
 * WebView (Web アプリ) とネイティブの間の postMessage プロトコル。
 * Web アプリを実装する側はこのファイルの型とコメントだけを見れば連携できるはず。
 *
 * 端末とログイン ID は多対多 (1 端末に複数アカウントがログインでき、1 アカウントは複数端末に
 * 登録されうる)。ネイティブはこの端末に現在登録されているログイン ID の一覧を保持していて、
 * `ready` イベントの `loginIds` で Web アプリに伝える。
 *
 * ## Web → ネイティブ
 * Web アプリ側から `window.ReactNativeWebView.postMessage(JSON.stringify(msg))` で送る。
 * JSON でシリアライズした `WebToNativeMessage` を投げること。
 *
 * - `{ type: "login", loginId, token? }`
 *   ログインした時に送る。ネイティブは loginId (+ 任意の token) と端末情報を
 *   バックエンドの `POST /v1/devices` に登録し (この端末への紐付けを追加 / 更新する)、
 *   完了したら `registered` イベントを返す。
 *   `token` はバックエンドの `Authorization: Bearer <token>` にそのまま使われる
 *   (何を入れるかはバックエンドの `Authorizer` 実装次第。未指定でもよい)。
 * - `{ type: "logout", loginId? }`
 *   ログアウトした時に送る。
 *   - `loginId` を指定した場合: そのアカウントだけログアウトする。ネイティブは
 *     `DELETE /v1/devices/{installationId}/logins/{loginId}` を呼び、そのログイン ID の
 *     紐付けだけを外す (この端末の他のログイン中アカウントへの通知は継続する)。
 *   - `loginId` を省略した場合: この端末そのものをログアウトする (全アカウント分の通知を止める)。
 *     ネイティブは `DELETE /v1/devices/{installationId}` を呼び、この端末に紐付いている
 *     すべてのログイン ID を削除する。
 *   成功すると `unregistered` イベントが返る。
 * - `{ type: "openExternal", url }`
 *   システムのブラウザ (または対応アプリ) で URL を開く。
 * - `{ type: "getState" }`
 *   現在の状態を知りたい時に送る。ネイティブは `ready` イベントを返す。
 *
 * ## ネイティブ → Web
 * ネイティブは `window.dispatchEvent(new CustomEvent("nativeapp", { detail }))` で
 * `NativeToWebMessage` を投げる。Web 側はこう受け取る:
 *
 * ```js
 * window.addEventListener("nativeapp", (event) => {
 *   const message = event.detail; // NativeToWebMessage
 * });
 * ```
 *
 * - `{ type: "ready", platform, appVersion, installationId, pushPermission, loginIds }`
 *   WebView の初回ロード時、および `getState` への応答として送る。
 *   `loginIds` は現在この端末 (installationId) に登録されているログイン ID の一覧
 *   (何もログインしていなければ空配列)。
 * - `{ type: "registered", loginId }`
 *   `login` メッセージの処理 (バックエンドへの端末登録) が成功した時に送る。
 * - `{ type: "unregistered", loginId? }`
 *   `logout` メッセージの処理が成功した時に送る。`loginId` は logout メッセージで
 *   指定したものをそのまま返す (省略していれば省略する = 端末全体をログアウトした)。
 * - `{ type: "error", message }`
 *   `login` / `logout` の処理が失敗した時に送る。
 * - `{ type: "notification", data }`
 *   プッシュ通知がタップされた時に送る (`data` は送信時の `data` フィールドの中身)。
 *   `data.url` が許可された origin であれば、ネイティブは自動で WebView をその URL に
 *   遷移させた上でこのイベントも送る (詳細は `useNotificationNavigation.ts`)。
 *
 * 加えて、ページ読み込み前に `injectedJavaScriptBeforeContentLoaded` で
 * `window.NativeApp = { isNative: true, platform, appVersion }` を注入する。
 * Web アプリはこれの有無でネイティブアプリ内かどうかを判定できる。
 */

export type PushPermissionStatus = "granted" | "denied" | "undetermined";

export interface WindowNativeApp {
  isNative: true;
  platform: "ios" | "android";
  appVersion: string;
}

// ---- Web → ネイティブ ----

export interface LoginMessage {
  type: "login";
  loginId: string;
  token?: string;
}

export interface LogoutMessage {
  type: "logout";
  /** 省略時はこの端末に紐づく全アカウントをログアウトする。 */
  loginId?: string;
}

export interface OpenExternalMessage {
  type: "openExternal";
  url: string;
}

export interface GetStateMessage {
  type: "getState";
}

export type WebToNativeMessage =
  | LoginMessage
  | LogoutMessage
  | OpenExternalMessage
  | GetStateMessage;

// ---- ネイティブ → Web ----

export interface ReadyEvent {
  type: "ready";
  platform: "ios" | "android";
  appVersion: string;
  installationId: string;
  pushPermission: PushPermissionStatus;
  /** この端末 (installationId) に現在登録されているログイン ID の一覧。 */
  loginIds: string[];
}

export interface RegisteredEvent {
  type: "registered";
  loginId: string;
}

export interface UnregisteredEvent {
  type: "unregistered";
  /** logout メッセージで指定した loginId (省略していれば端末全体のログアウトなので省略する)。 */
  loginId?: string;
}

export interface ErrorEvent {
  type: "error";
  message: string;
}

export interface NotificationEvent {
  type: "notification";
  data: Record<string, unknown>;
}

export type NativeToWebMessage =
  | ReadyEvent
  | RegisteredEvent
  | UnregisteredEvent
  | ErrorEvent
  | NotificationEvent;

// ---- バリデーション (型ガード) ----
// 外部 (Web ページ) からの入力なので、素朴な手書きガードで防御的にパースする。

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null;
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === "string" && value.length > 0;
}

export function parseWebToNativeMessage(raw: string): WebToNativeMessage | null {
  let json: unknown;
  try {
    json = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!isRecord(json) || typeof json.type !== "string") return null;

  switch (json.type) {
    case "login":
      if (!isNonEmptyString(json.loginId)) return null;
      return {
        type: "login",
        loginId: json.loginId,
        token: typeof json.token === "string" ? json.token : undefined,
      };
    case "logout":
      return {
        type: "logout",
        loginId: isNonEmptyString(json.loginId) ? json.loginId : undefined,
      };
    case "openExternal":
      if (!isNonEmptyString(json.url)) return null;
      return { type: "openExternal", url: json.url };
    case "getState":
      return { type: "getState" };
    default:
      // 未知の type は無視する (将来の拡張や、想定外のページからのメッセージ)。
      return null;
  }
}

/**
 * WebView の `onMessage` イベントの送信元 URL の origin が、許可された origin
 * (app.config.ts の `ALLOWED_ORIGINS` = `extra.allowedOrigins`) に含まれるか調べる。
 */
export function isAllowedOrigin(url: string, allowedOrigins: readonly string[]): boolean {
  try {
    const origin = new URL(url).origin;
    return allowedOrigins.includes(origin);
  } catch {
    return false;
  }
}

/** ネイティブ→Web イベントを注入する JS 文字列を作る (`webViewRef.current.injectJavaScript`)。 */
export function injectNativeEvent(message: NativeToWebMessage): string {
  return `window.dispatchEvent(new CustomEvent("nativeapp", { detail: ${JSON.stringify(
    message,
  )} })); true;`;
}

/** `injectedJavaScriptBeforeContentLoaded` に渡す、`window.NativeApp` を定義する JS。 */
export function injectedBeforeContentLoaded(nativeApp: WindowNativeApp): string {
  return `window.NativeApp = ${JSON.stringify(nativeApp)}; true;`;
}
