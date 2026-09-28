import createClient from "openapi-fetch";

import { config } from "../config";
import type { components, paths } from "./schema";

/**
 * OpenAPI (../../openapi/openapi.yaml) から生成した型を使う API クライアント。
 * `npm run gen:api` で schema.d.ts を再生成できる (openapi.yaml を変更したら実行すること)。
 */

export type DeviceRegistration = components["schemas"]["DeviceRegistration"];
export type Device = components["schemas"]["Device"];
export type ApiErrorBody = components["schemas"]["Error"];

/** バックエンドが返した Error レスポンスをそのまま持つ例外。 */
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;

  constructor(status: number, body: ApiErrorBody) {
    super(body.message);
    this.name = "ApiError";
    this.code = body.code;
    this.status = status;
  }
}

const client = createClient<paths>({ baseUrl: config.apiBaseUrl });

/** login 時に postMessage で受け取った任意の bearer トークンをヘッダーに付ける。 */
function authHeaders(token?: string): Record<string, string> {
  return token ? { Authorization: `Bearer ${token}` } : {};
}

/**
 * 端末を登録 / 更新する (installationId で upsert)。
 * Web アプリのログイン時、および push token がローテーションした時に呼ぶ。
 */
export async function registerDevice(
  registration: DeviceRegistration,
  token?: string,
): Promise<Device> {
  const { data, error, response } = await client.POST("/v1/devices", {
    body: registration,
    headers: authHeaders(token),
  });
  if (error) throw new ApiError(response.status, error);
  return data;
}

/** 端末の登録を解除する (ログアウト時に呼ぶ)。存在しなくても成功扱い。 */
export async function unregisterDevice(installationId: string, token?: string): Promise<void> {
  const { error, response } = await client.DELETE("/v1/devices/{installationId}", {
    params: { path: { installationId } },
    headers: authHeaders(token),
  });
  if (error) throw new ApiError(response.status, error);
}
