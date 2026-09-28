import * as Crypto from "expo-crypto";
import * as SecureStore from "expo-secure-store";

/**
 * 端末インストールごとに安定した ID (installationId) と、
 * この端末で現在ログイン中のアカウント一覧 (loginId / token) を SecureStore に保存する。
 *
 * installationId はバックエンドの `POST /v1/devices` の upsert キーになるので、
 * アプリを再インストールしない限り変わらない値である必要がある。
 *
 * 端末とログイン ID は多対多 (1 端末に複数アカウント、1 アカウントに複数端末) なので、
 * 単一の login 情報ではなく一覧として保持する。
 */

const INSTALLATION_ID_KEY = "webapp_notification.installation_id";
const LOGINS_KEY = "webapp_notification.logins";
// 旧バージョン (単一ログインのみ保持していた頃) のキー。移行用に読むだけ。
const LEGACY_LOGIN_STATE_KEY = "webapp_notification.login_state";

let cachedInstallationId: string | null = null;

/** installationId を取得する。初回起動時は生成して保存する。 */
export async function getInstallationId(): Promise<string> {
  if (cachedInstallationId) return cachedInstallationId;

  const existing = await SecureStore.getItemAsync(INSTALLATION_ID_KEY);
  if (existing) {
    cachedInstallationId = existing;
    return existing;
  }

  const generated = Crypto.randomUUID();
  await SecureStore.setItemAsync(INSTALLATION_ID_KEY, generated);
  cachedInstallationId = generated;
  return generated;
}

export interface StoredLogin {
  loginId: string;
  token?: string;
}

function isStoredLogin(value: unknown): value is StoredLogin {
  if (typeof value !== "object" || value === null) return false;
  const candidate = value as Partial<StoredLogin>;
  return (
    typeof candidate.loginId === "string" &&
    (candidate.token === undefined || typeof candidate.token === "string")
  );
}

async function saveLogins(logins: StoredLogin[]): Promise<void> {
  await SecureStore.setItemAsync(LOGINS_KEY, JSON.stringify(logins));
}

/**
 * 現在この端末に登録されているログイン一覧を読み込む。
 * 新形式のデータが無い場合、旧形式 (単一ログイン) のデータが残っていれば best-effort で移行する。
 */
export async function loadLogins(): Promise<StoredLogin[]> {
  const raw = await SecureStore.getItemAsync(LOGINS_KEY);
  if (raw) {
    try {
      const parsed: unknown = JSON.parse(raw);
      if (Array.isArray(parsed)) {
        return parsed.filter(isStoredLogin);
      }
    } catch {
      // 壊れたデータは無視して空扱いにする。
    }
    return [];
  }

  // 新形式のキーが無ければ旧形式 (単一ログイン) からの移行を試す。無ければそのまま空を返す。
  const legacyRaw = await SecureStore.getItemAsync(LEGACY_LOGIN_STATE_KEY);
  if (!legacyRaw) return [];

  await SecureStore.deleteItemAsync(LEGACY_LOGIN_STATE_KEY);
  try {
    const parsed: unknown = JSON.parse(legacyRaw);
    if (isStoredLogin(parsed)) {
      await saveLogins([parsed]);
      return [parsed];
    }
  } catch {
    // 壊れたデータは無視する。
  }
  return [];
}

/** ログインを追加する。既に同じ loginId があれば上書き (token 更新など) する。更新後の一覧を返す。 */
export async function upsertLogin(login: StoredLogin): Promise<StoredLogin[]> {
  const current = await loadLogins();
  const next = [...current.filter((stored) => stored.loginId !== login.loginId), login];
  await saveLogins(next);
  return next;
}

/** 指定した loginId のログインだけを削除する。更新後の一覧を返す。 */
export async function removeLogin(loginId: string): Promise<StoredLogin[]> {
  const current = await loadLogins();
  const next = current.filter((stored) => stored.loginId !== loginId);
  await saveLogins(next);
  return next;
}

/** この端末のログインをすべて削除する (端末単位の全ログアウト時)。 */
export async function clearLogins(): Promise<void> {
  await SecureStore.deleteItemAsync(LOGINS_KEY);
}
