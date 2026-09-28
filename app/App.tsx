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

import { ApiError, registerDevice, unregisterDevice } from "./src/api/client";
import {
  injectNativeEvent,
  injectedBeforeContentLoaded,
  isAllowedOrigin,
  parseWebToNativeMessage,
} from "./src/bridge";
import type { PushPermissionStatus } from "./src/bridge";
import { config } from "./src/config";
import {
  clearLoginState,
  getInstallationId,
  loadLoginState,
  saveLoginState,
  type StoredLoginState,
} from "./src/installation";
import {
  buildDeviceRegistration,
  ensurePushPermission,
  getCurrentPushPermission,
  getPushTokens,
} from "./src/notifications";
import { useNotificationNavigation } from "./src/useNotificationNavigation";

// ネイティブの Splash Screen は WebView が最初のロードを終えるまで表示し続ける。
SplashScreen.preventAutoHideAsync().catch(() => {
  // すでに hide 済み等は無視してよい。
});

const platform: "ios" | "android" = Platform.OS === "ios" ? "ios" : "android";

export default function App() {
  const webViewRef = useRef<WebView>(null);
  const [installationId, setInstallationId] = useState<string | null>(null);
  const [loginState, setLoginState] = useState<StoredLoginState | null>(null);
  const [loading, setLoading] = useState(true);
  const [canGoBack, setCanGoBack] = useState(false);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [needsResync, setNeedsResync] = useState(false);

  // login/logout 処理中に最新の loginState を読みたいので ref にも保持する。
  const loginStateRef = useRef<StoredLoginState | null>(null);
  loginStateRef.current = loginState;

  useNotificationNavigation(webViewRef);

  // 初期化: installationId の確保、前回ログイン状態の復元。
  useEffect(() => {
    (async () => {
      const id = await getInstallationId();
      setInstallationId(id);
      const stored = await loadLoginState();
      if (stored) {
        setLoginState(stored);
        loginStateRef.current = stored;
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
    });
  }, [installationId, sendToWeb]);

  const registerWithBackend = useCallback(
    async (loginId: string, token: string | undefined) => {
      // state の installationId がまだ無ければ (起動直後) SecureStore から直接取る。
      const id = installationId ?? (await getInstallationId());
      try {
        const permission: PushPermissionStatus = await ensurePushPermission();
        const tokens = await getPushTokens();

        // 配送方式ごとに必要なトークンが揃っているか確認する。
        const token =
          config.pushProvider === "native" ? tokens.devicePushToken : tokens.expoPushToken;
        if (permission !== "granted" || !token) {
          throw new Error(
            `プッシュ通知の権限またはトークンが取得できません (permission=${permission}, provider=${config.pushProvider})`,
          );
        }

        const registration = buildDeviceRegistration({
          loginId,
          installationId: id,
          expoPushToken: tokens.expoPushToken,
          devicePushToken: tokens.devicePushToken,
        });
        await registerDevice(registration, token);

        const state: StoredLoginState = { loginId, token };
        await saveLoginState(state);
        setLoginState(state);
        loginStateRef.current = state;
        sendToWeb({ type: "registered", loginId });
      } catch (error) {
        console.warn("[App] registerWithBackend に失敗しました:", error);
        const message = error instanceof ApiError ? error.message : String(error);
        sendToWeb({ type: "error", message });
      }
    },
    [installationId, sendToWeb],
  );

  // 前回ログインしたままなら起動時に一度だけ再登録する。
  // アプリ停止中に push token がローテーションしていても、これでバックエンド側が最新になる。
  useEffect(() => {
    if (!needsResync || !installationId) return;
    setNeedsResync(false);
    const current = loginStateRef.current;
    if (current) {
      registerWithBackend(current.loginId, current.token);
    }
  }, [needsResync, installationId, registerWithBackend]);

  // device push token がローテーションしたら Expo Push Token も取り直して再登録する
  // (ログイン中のみ)。addPushTokenListener が返すのはネイティブの device push token。
  useEffect(() => {
    const subscription = Notifications.addPushTokenListener(() => {
      const current = loginStateRef.current;
      if (!current) return;
      registerWithBackend(current.loginId, current.token);
    });
    return () => subscription.remove();
  }, [registerWithBackend]);

  const handleLogout = useCallback(async () => {
    const current = loginStateRef.current;
    const id = installationId ?? (await getInstallationId());
    try {
      if (current) {
        await unregisterDevice(id, current.token);
      }
      await clearLoginState();
      setLoginState(null);
      loginStateRef.current = null;
    } catch (error) {
      console.warn("[App] logout に失敗しました:", error);
      const message = error instanceof ApiError ? error.message : String(error);
      sendToWeb({ type: "error", message });
    }
  }, [installationId, sendToWeb]);

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
          handleLogout();
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
  }, [sendReady]);

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
