import Constants from "expo-constants";

/**
 * app.config.ts の `extra` に積んだ実行時設定の型。
 */
export interface AppExtra {
  webappUrl: string;
  apiBaseUrl: string;
  allowedOrigins: string[];
  associatedDomains: string[];
  appVersion: string;
  pushProvider: PushProvider;
  webviewAndroidLayerType?: WebViewLayerType;
  eas?: { projectId?: string };
}

/** Android WebView の描画レイヤー (react-native-webview の androidLayerType)。 */
export type WebViewLayerType = "none" | "software" | "hardware";

/** 通知の配送方式 (バックエンドの PUSH_PROVIDER と対応)。 */
export type PushProvider = "expo" | "native";

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
  // Universal Links / App Links として受け付けるホスト (未設定なら空)。
  associatedDomains: extra.associatedDomains ?? [],
  appVersion: extra.appVersion ?? "1.0.0",
  pushProvider: (extra.pushProvider === "native" ? "native" : "expo") as PushProvider,
  webviewAndroidLayerType: (extra.webviewAndroidLayerType ?? "none") as WebViewLayerType,
  // Expo Push Token の取得に必要な projectId。
  // extra.eas.projectId (app.config.ts 経由) か、EAS Build が埋め込む
  // Constants.easConfig のどちらかにある。
  easProjectId: extra.eas?.projectId ?? Constants.easConfig?.projectId,
} as const;
