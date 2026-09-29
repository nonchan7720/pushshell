import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ActivityIndicator,
  BackHandler,
  Linking,
  Platform,
  StyleSheet,
  Text,
  TouchableOpacity,
  View,
} from "react-native";
import { SafeAreaProvider, SafeAreaView } from "react-native-safe-area-context";
import { StatusBar } from "expo-status-bar";
import * as SplashScreen from "expo-splash-screen";
import * as Notifications from "expo-notifications";
import WebView from "react-native-webview";
import type {
  WebViewErrorEvent,
  WebViewHttpErrorEvent,
  WebViewMessageEvent,
  WebViewNavigation,
} from "react-native-webview/lib/WebViewTypes";

import { ApiError, registerDevice, unregisterDevice, unregisterDeviceLogin } from "./src/api/client";
import {
  injectNativeEvent,
  injectedBeforeContentLoaded,
  isAllowedOrigin,
  parseWebToNativeMessage,
} from "./src/bridge";
import type { PushPermissionStatus } from "./src/bridge";
import { config } from "./src/config";
import {
  clearLogins,
  getInstallationId,
  loadLogins,
  removeLogin,
  upsertLogin,
  type StoredLogin,
} from "./src/installation";
import {
  buildDeviceRegistration,
  ensurePushPermission,
  getCurrentPushPermission,
  getPushTokens,
  type PushTokens,
} from "./src/notifications";
import { useDeepLinkNavigation } from "./src/useDeepLinkNavigation";
import { useNotificationNavigation } from "./src/useNotificationNavigation";

// ネイティブの Splash Screen は WebView が最初のロードを終えるまで表示し続ける。
SplashScreen.preventAutoHideAsync().catch(() => {
  // すでに hide 済み等は無視してよい。
});

const platform: "ios" | "android" = Platform.OS === "ios" ? "ios" : "android";

export default function App() {
  const webViewRef = useRef<WebView>(null);
  const [installationId, setInstallationId] = useState<string | null>(null);
  const [logins, setLogins] = useState<StoredLogin[]>([]);
  const [loading, setLoading] = useState(true);
  const [canGoBack, setCanGoBack] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [needsResync, setNeedsResync] = useState(false);

  // login/logout 処理中に最新の logins を読みたいので ref にも保持する。
  const loginsRef = useRef<StoredLogin[]>([]);
  loginsRef.current = logins;

  useNotificationNavigation(webViewRef);
  // Universal Links / App Links。コールドスタート時のリンクは初回ロード完了後に適用する。
  const { applyPendingDeepLink } = useDeepLinkNavigation(webViewRef);

  // 初期化: installationId の確保、前回ログイン状態 (複数アカウント分) の復元。
  useEffect(() => {
    (async () => {
      const id = await getInstallationId();
      setInstallationId(id);
      const stored = await loadLogins();
      if (stored.length > 0) {
        setLogins(stored);
        loginsRef.current = stored;
        setNeedsResync(true);
      }
    })();
  }, []);

  // Android のハードウェア戻るボタン: WebView が戻れるなら戻る、そうでなければ既定動作。
  useEffect(() => {
    if (Platform.OS !== "android") return;
    const subscription = BackHandler.addEventListener("hardwareBackPress", () => {
      if (canGoBack && webViewRef.current) {
        webViewRef.current.goBack();
        return true;
      }
      return false;
    });
    return () => subscription.remove();
  }, [canGoBack]);

  const sendToWeb = useCallback((message: Parameters<typeof injectNativeEvent>[0]) => {
    webViewRef.current?.injectJavaScript(injectNativeEvent(message));
  }, []);

  const sendReady = useCallback(async () => {
    if (!installationId) return;
    const pushPermission = await getCurrentPushPermission();
    sendToWeb({
      type: "ready",
      platform,
      appVersion: config.appVersion,
      installationId,
      pushPermission,
      loginIds: loginsRef.current.map((login) => login.loginId),
    });
  }, [installationId, sendToWeb]);

  // 通知権限を確認し、配送方式 (config.pushProvider) に必要な push token を取得する。
  // 権限が無い/トークンが取れない場合は例外を投げる。
  const acquirePushToken = useCallback(async (): Promise<PushTokens> => {
    const permission: PushPermissionStatus = await ensurePushPermission();
    const tokens = await getPushTokens();
    const token = config.pushProvider === "native" ? tokens.devicePushToken : tokens.expoPushToken;
    if (permission !== "granted" || !token) {
      throw new Error(
        `プッシュ通知の権限またはトークンが取得できません (permission=${permission}, provider=${config.pushProvider})`,
      );
    }
    return tokens;
  }, []);

  // 1 アカウント分の DeviceRegistration を組み立ててバックエンドに登録し、保存/通知まで行う。
  const registerLogin = useCallback(
    async (loginId: string, token: string | undefined, installId: string, tokens: PushTokens) => {
      const registration = buildDeviceRegistration({
        loginId,
        installationId: installId,
        expoPushToken: tokens.expoPushToken,
        devicePushToken: tokens.devicePushToken,
      });
      await registerDevice(registration, token);

      const next = await upsertLogin({ loginId, token });
      setLogins(next);
      loginsRef.current = next;
      sendToWeb({ type: "registered", loginId });
    },
    [sendToWeb],
  );

  // `login` メッセージ (Web アプリからの単一アカウントのログイン) を処理する。
  const registerWithBackend = useCallback(
    async (loginId: string, token: string | undefined) => {
      // state の installationId がまだ無ければ (起動直後) SecureStore から直接取る。
      const id = installationId ?? (await getInstallationId());
      try {
        const tokens = await acquirePushToken();
        await registerLogin(loginId, token, id, tokens);
      } catch (error) {
        console.warn("[App] registerWithBackend に失敗しました:", error);
        const message = error instanceof ApiError ? error.message : String(error);
        sendToWeb({ type: "error", message });
      }
    },
    [installationId, acquirePushToken, registerLogin, sendToWeb],
  );

  // 保存済みの全ログインを順番に再登録する (起動時の resync、push token ローテーション時に使う)。
  // 1 件失敗しても残りは続行し、失敗があった場合にまとめて 1 回だけ error を送る。
  const resyncLogins = useCallback(async () => {
    const stored = loginsRef.current;
    if (stored.length === 0) return;
    const id = installationId ?? (await getInstallationId());

    let tokens: PushTokens;
    try {
      tokens = await acquirePushToken();
    } catch (error) {
      console.warn("[App] resyncLogins: push token の取得に失敗しました:", error);
      const message = error instanceof ApiError ? error.message : String(error);
      sendToWeb({ type: "error", message });
      return;
    }

    let hasError = false;
    for (const login of stored) {
      try {
        await registerLogin(login.loginId, login.token, id, tokens);
      } catch (error) {
        hasError = true;
        console.warn(`[App] resyncLogins (${login.loginId}) に失敗しました:`, error);
      }
    }
    if (hasError) {
      sendToWeb({ type: "error", message: "一部のログインの再登録に失敗しました。" });
    }
  }, [installationId, acquirePushToken, registerLogin, sendToWeb]);

  // 前回ログインしたままなら起動時に一度だけ再登録する。
  // アプリ停止中に push token がローテーションしていても、これでバックエンド側が最新になる。
  useEffect(() => {
    if (!needsResync || !installationId) return;
    setNeedsResync(false);
    resyncLogins();
  }, [needsResync, installationId, resyncLogins]);

  // device push token がローテーションしたら Expo Push Token も取り直して、保存済みの
  // 全ログインを再登録する。addPushTokenListener が返すのはネイティブの device push token。
  useEffect(() => {
    const subscription = Notifications.addPushTokenListener(() => {
      resyncLogins();
    });
    return () => subscription.remove();
  }, [resyncLogins]);

  // `logout` メッセージを処理する。loginId 指定ありならそのアカウントだけ、無ければ端末全体を
  // ログアウトする。
  const handleLogout = useCallback(
    async (loginId: string | undefined) => {
      const id = installationId ?? (await getInstallationId());
      try {
        if (loginId) {
          const current = loginsRef.current.find((login) => login.loginId === loginId);
          await unregisterDeviceLogin(id, loginId, current?.token);
          const next = await removeLogin(loginId);
          setLogins(next);
          loginsRef.current = next;
        } else {
          // 端末単位の全ログアウト。認証ヘッダーには保存済みログインのいずれかの token を使う
          // (デフォルトの Authorizer はトークンを検証しないため、どれを使っても動作する)。
          const token = loginsRef.current[0]?.token;
          await unregisterDevice(id, token);
          await clearLogins();
          setLogins([]);
          loginsRef.current = [];
        }
        sendToWeb({ type: "unregistered", loginId });
      } catch (error) {
        console.warn("[App] logout に失敗しました:", error);
        const message = error instanceof ApiError ? error.message : String(error);
        sendToWeb({ type: "error", message });
      }
    },
    [installationId, sendToWeb],
  );

  const handleMessage = useCallback(
    (event: WebViewMessageEvent) => {
      // 許可された origin から送られたメッセージだけを扱う (postMessage はどの origin
      // にいる JS からも送信可能なため、ここで送信元を確認する)。
      if (!isAllowedOrigin(event.nativeEvent.url, config.allowedOrigins)) {
        console.warn("[App] 許可されていない origin からのメッセージを無視:", event.nativeEvent.url);
        return;
      }

      const message = parseWebToNativeMessage(event.nativeEvent.data);
      if (!message) return;

      switch (message.type) {
        case "login":
          registerWithBackend(message.loginId, message.token);
          break;
        case "logout":
          handleLogout(message.loginId);
          break;
        case "openExternal":
          Linking.openURL(message.url).catch((error) => {
            console.warn("[App] openExternal に失敗しました:", error);
          });
          break;
        case "getState":
          sendReady();
          break;
      }
    },
    [handleLogout, registerWithBackend, sendReady],
  );

  const handleNavigationStateChange = useCallback((navState: WebViewNavigation) => {
    setCanGoBack(navState.canGoBack);
  }, []);

  const handleLoadEnd = useCallback(async () => {
    setLoading(false);
    setLoadError(null);
    await SplashScreen.hideAsync().catch(() => {});
    await sendReady();
    applyPendingDeepLink();
  }, [sendReady, applyPendingDeepLink]);

  const handleError = useCallback((event: WebViewErrorEvent) => {
    setLoading(false);
    setLoadError(event.nativeEvent.description || "読み込みに失敗しました");
    SplashScreen.hideAsync().catch(() => {});
  }, []);

  const handleHttpError = useCallback((event: WebViewHttpErrorEvent) => {
    setLoading(false);
    setLoadError(
      `HTTP ${event.nativeEvent.statusCode}: ${event.nativeEvent.description || "読み込みに失敗しました"}`,
    );
    SplashScreen.hideAsync().catch(() => {});
  }, []);

  const retry = useCallback(() => {
    setLoadError(null);
    setLoading(true);
    setReloadKey((key) => key + 1);
  }, []);

  const originWhitelist = useMemo(
    () => (config.allowedOrigins.length > 0 ? config.allowedOrigins.map((o) => `${o}/*`) : ["*"]),
    [],
  );

  const beforeContentLoadedScript = useMemo(
    () => injectedBeforeContentLoaded({ isNative: true, platform, appVersion: config.appVersion }),
    [],
  );

  return (
    <SafeAreaProvider>
      <SafeAreaView style={styles.container} edges={["top", "bottom"]}>
        <StatusBar style="auto" />
        {loadError ? (
          <View style={styles.errorContainer}>
            <Text style={styles.errorTitle}>読み込みに失敗しました</Text>
            <Text style={styles.errorMessage}>{loadError}</Text>
            <TouchableOpacity style={styles.retryButton} onPress={retry}>
              <Text style={styles.retryButtonText}>再読み込み</Text>
            </TouchableOpacity>
          </View>
        ) : (
          <WebView
            key={reloadKey}
            ref={webViewRef}
            source={{ uri: config.webappUrl }}
            style={styles.webview}
            originWhitelist={originWhitelist}
            androidLayerType={config.webviewAndroidLayerType}
            injectedJavaScriptBeforeContentLoaded={beforeContentLoadedScript}
            onMessage={handleMessage}
            onNavigationStateChange={handleNavigationStateChange}
            onLoadEnd={handleLoadEnd}
            onError={handleError}
            onHttpError={handleHttpError}
            allowsBackForwardNavigationGestures
            pullToRefreshEnabled
            startInLoadingState={false}
          />
        )}
        {loading && !loadError ? (
          <View style={styles.loadingOverlay} pointerEvents="none">
            <ActivityIndicator size="large" />
          </View>
        ) : null}
      </SafeAreaView>
    </SafeAreaProvider>
  );
}

const styles = StyleSheet.create({
  container: {
    flex: 1,
    backgroundColor: "#fff",
  },
  webview: {
    flex: 1,
  },
  loadingOverlay: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    alignItems: "center",
    justifyContent: "center",
    backgroundColor: "#fff",
  },
  errorContainer: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    padding: 24,
    gap: 12,
  },
  errorTitle: {
    fontSize: 18,
    fontWeight: "600",
  },
  errorMessage: {
    fontSize: 14,
    color: "#666",
    textAlign: "center",
  },
  retryButton: {
    marginTop: 12,
    paddingVertical: 10,
    paddingHorizontal: 24,
    backgroundColor: "#111",
    borderRadius: 8,
  },
  retryButtonText: {
    color: "#fff",
    fontWeight: "600",
  },
});
