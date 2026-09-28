import * as Crypto from "expo-crypto";
import * as SecureStore from "expo-secure-store";

/**
 * 端末インストールごとに安定した ID (installationId) と、
 * 直近ログインしていた loginId / token を SecureStore に保存する。
 *
 * installationId はバックエンドの `POST /v1/devices` の upsert キーになるので、
 * アプリを再インストールしない限り変わらない値である必要がある。
 */

const INSTALLATION_ID_KEY = "webapp_notification.installation_id";
const LOGIN_STATE_KEY = "webapp_notification.login_state";

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

export interface StoredLoginState {
  loginId: string;
  token?: string;
}

/**
 * 直近の login 情報を保存する。アプリ再起動後や push token ローテーション後に
 * バックエンドへ再登録するために使う。
 */
export async function saveLoginState(state: StoredLoginState): Promise<void> {
  await SecureStore.setItemAsync(LOGIN_STATE_KEY, JSON.stringify(state));
}

export async function loadLoginState(): Promise<StoredLoginState | null> {
  const raw = await SecureStore.getItemAsync(LOGIN_STATE_KEY);
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (
      typeof parsed === "object" &&
      parsed !== null &&
      typeof (parsed as StoredLoginState).loginId === "string"
    ) {
      return parsed as StoredLoginState;
    }
    return null;
  } catch {
    return null;
  }
}

export async function clearLoginState(): Promise<void> {
  await SecureStore.deleteItemAsync(LOGIN_STATE_KEY);
}
