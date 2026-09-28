import type { ConfigContext, ExpoConfig } from "expo/config";

/**
 * app.config.ts
 *
 * このアプリは「WebView 表示 + プッシュ通知」だけを行うテンプレートアプリ。
 * 表示 URL・アプリ名・アイコン・バンドル ID などはビルド時に環境変数で注入する
 * (`app/.env.example` を参照。EAS Build では `eas.json` の `env` ブロックか
 * EAS の Environment Variables で渡す)。
 *
 * 実行時に (JS 側から) 参照する値は `extra` に積んでおき、`src/config.ts` から
 * 型付きで読む。
 */

const env = (name: string, fallback?: string): string | undefined => {
  const value = process.env[name];
  return value && value.length > 0 ? value : fallback;
};

const APP_NAME = env("APP_NAME", "WebApp")!;
const APP_SLUG = env("APP_SLUG", "pushshell")!;
const APP_SCHEME = env("APP_SCHEME", "pushshell")!;
const APP_VERSION = env("APP_VERSION", "1.0.0")!;

// アイコン類。プレースホルダーの PNG を assets/ に置いてあるので、
// 実際のアプリではこれらの env でファイルパスを差し替える。
const APP_ICON = env("APP_ICON", "./assets/icon.png")!;
const APP_ADAPTIVE_ICON_FOREGROUND = env(
  "APP_ADAPTIVE_ICON_FOREGROUND",
  "./assets/android-icon-foreground.png",
)!;
const APP_ADAPTIVE_ICON_BACKGROUND = env("APP_ADAPTIVE_ICON_BACKGROUND", "#FFFFFF")!;
const APP_ADAPTIVE_ICON_MONOCHROME = env(
  "APP_ADAPTIVE_ICON_MONOCHROME",
  "./assets/android-icon-monochrome.png",
);
const APP_SPLASH_IMAGE = env("APP_SPLASH_IMAGE", "./assets/splash-icon.png")!;
const APP_SPLASH_BACKGROUND = env("APP_SPLASH_BACKGROUND", "#FFFFFF")!;

const IOS_BUNDLE_ID = env("IOS_BUNDLE_ID", "com.example.pushshell")!;
const ANDROID_PACKAGE = env("ANDROID_PACKAGE", "com.example.pushshell")!;

// WebView に表示する URL とバックエンドの URL。
const WEBAPP_URL = env("WEBAPP_URL", "https://example.com")!;
const API_BASE_URL = env("API_BASE_URL", "http://localhost:8080")!;

// Expo Push Token の取得に必要 (未設定だと getExpoPushTokenAsync が失敗する)。
const EAS_PROJECT_ID = env("EAS_PROJECT_ID");

// Android WebView の描画レイヤー。none (既定, ハードウェア) / software / hardware。
// GPU が無いエミュレータや一部端末で WebView がクラッシュする場合に software を指定する。
const WEBVIEW_ANDROID_LAYER_TYPE = env("WEBVIEW_ANDROID_LAYER_TYPE", "none")!;
if (!["none", "software", "hardware"].includes(WEBVIEW_ANDROID_LAYER_TYPE)) {
  throw new Error(`WEBVIEW_ANDROID_LAYER_TYPE は none / software / hardware のいずれか (got: ${WEBVIEW_ANDROID_LAYER_TYPE})`);
}

// http:// の Web アプリ / バックエンドを使う場合 (ローカル開発、エミュレータでの検証) に true。
// 本番は https のみにして false のままにすること。
const ANDROID_USES_CLEARTEXT_TRAFFIC = env("ANDROID_USES_CLEARTEXT_TRAFFIC", "false") === "true";

// 通知の配送方式。バックエンドの PUSH_PROVIDER と揃える。
//   expo:   Expo Push Token を登録する (EAS_PROJECT_ID が必要)
//   native: FCM / APNs のネイティブトークンだけを登録する (Expo のプロジェクト不要)
const PUSH_PROVIDER = env("PUSH_PROVIDER", "expo")!;
if (PUSH_PROVIDER !== "expo" && PUSH_PROVIDER !== "native") {
  throw new Error(`PUSH_PROVIDER は expo か native を指定してください (got: ${PUSH_PROVIDER})`);
}

// google-services.json のパス (Android で FCM を使う場合)。
const GOOGLE_SERVICES_JSON = env("GOOGLE_SERVICES_JSON");

// expo-notifications プラグイン用 (任意)。未設定ならプラグイン既定値を使う。
const APP_NOTIFICATION_ICON = env("APP_NOTIFICATION_ICON");
const APP_NOTIFICATION_COLOR = env("APP_NOTIFICATION_COLOR");

// WebView がロードを許可する origin と、bridge.ts がメッセージの送信元として
// 信頼する origin。未指定なら WEBAPP_URL の origin から自動で導出する。
const originOf = (url: string): string => {
  try {
    return new URL(url).origin;
  } catch {
    return url;
  }
};

const ALLOWED_ORIGINS = (env("ALLOWED_ORIGINS") ?? originOf(WEBAPP_URL))
  .split(",")
  .map((origin) => origin.trim())
  .filter((origin) => origin.length > 0);

export default ({ config }: ConfigContext): ExpoConfig => ({
  ...config,
  name: APP_NAME,
  slug: APP_SLUG,
  version: APP_VERSION,
  scheme: APP_SCHEME,
  orientation: "portrait",
  icon: APP_ICON,
  userInterfaceStyle: "automatic",
  // appVersion をそのまま runtimeVersion として使う。EAS Update を使うなら
  // ビルドと同じ appVersion の間でのみ更新が配信される。
  runtimeVersion: { policy: "appVersion" },
  ios: {
    ...config.ios,
    bundleIdentifier: IOS_BUNDLE_ID,
    supportsTablet: true,
    infoPlist: {
      ...config.ios?.infoPlist,
      // バックグラウンドでのリモート通知受信を許可する。
      UIBackgroundModes: ["remote-notification"],
    },
  },
  android: {
    ...config.android,
    package: ANDROID_PACKAGE,
    predictiveBackGestureEnabled: false,
    permissions: ["android.permission.POST_NOTIFICATIONS"],
    adaptiveIcon: {
      foregroundImage: APP_ADAPTIVE_ICON_FOREGROUND,
      backgroundColor: APP_ADAPTIVE_ICON_BACKGROUND,
      ...(APP_ADAPTIVE_ICON_MONOCHROME ? { monochromeImage: APP_ADAPTIVE_ICON_MONOCHROME } : {}),
    },
    ...(GOOGLE_SERVICES_JSON ? { googleServicesFile: GOOGLE_SERVICES_JSON } : {}),
  },
  web: {
    ...config.web,
    favicon: "./assets/favicon.png",
  },
  plugins: [
    [
      "expo-splash-screen",
      {
        image: APP_SPLASH_IMAGE,
        backgroundColor: APP_SPLASH_BACKGROUND,
        imageWidth: 200,
      },
    ],
    [
      "expo-notifications",
      {
        ...(APP_NOTIFICATION_ICON ? { icon: APP_NOTIFICATION_ICON } : {}),
        ...(APP_NOTIFICATION_COLOR ? { color: APP_NOTIFICATION_COLOR } : {}),
      },
    ],
    [
      "expo-build-properties",
      {
        // WebView・通知・secure-store など複数のネイティブモジュールを併用するため、
        // iOS の Swift シンボル重複を避ける定番設定として static framework を使う。
        ios: { useFrameworks: "static" },
        android: { usesCleartextTraffic: ANDROID_USES_CLEARTEXT_TRAFFIC },
      },
    ],
    "expo-dev-client",
    // release ビルドの署名 (ANDROID_KEYSTORE_* が設定されているときだけ有効)
    "./plugins/withReleaseSigning.js",
  ],
  extra: {
    ...config.extra,
    webappUrl: WEBAPP_URL,
    apiBaseUrl: API_BASE_URL,
    allowedOrigins: ALLOWED_ORIGINS,
    appVersion: APP_VERSION,
    pushProvider: PUSH_PROVIDER,
    webviewAndroidLayerType: WEBVIEW_ANDROID_LAYER_TYPE,
    ...(EAS_PROJECT_ID ? { eas: { projectId: EAS_PROJECT_ID } } : {}),
  },
});
