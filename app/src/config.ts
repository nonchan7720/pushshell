import Constants from "expo-constants";

/**
 * app.config.ts の `extra` に積んだ実行時設定の型。
 */
export interface AppExtra {
  webappUrl: string;
  apiBaseUrl: string;
  allowedOrigins: string[];
  appVersion: string;
  eas?: { projectId?: string };
}

const extra = (Constants.expoConfig?.extra ?? {}) as Partial<AppExtra>;

function requireExtra<T>(value: T | undefined, name: string): T {
  if (value === undefined || value === null) {
    throw new Error(
      `Constants.expoConfig.extra.${name} が見つかりません。app.config.ts の設定を確認してください。`,
    );
  }
  return value;
}

/** アプリ全体で使う実行時設定。app.config.ts の extra を型付きで読む。 */
export const config = {
  webappUrl: requireExtra(extra.webappUrl, "webappUrl"),
  apiBaseUrl: requireExtra(extra.apiBaseUrl, "apiBaseUrl"),
  allowedOrigins: extra.allowedOrigins ?? [],
  appVersion: extra.appVersion ?? "1.0.0",
  // Expo Push Token の取得に必要な projectId。
  // extra.eas.projectId (app.config.ts 経由) か、EAS Build が埋め込む
  // Constants.easConfig のどちらかにある。
  easProjectId: extra.eas?.projectId ?? Constants.easConfig?.projectId,
} as const;
