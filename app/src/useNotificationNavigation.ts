import { useEffect } from "react";
import * as Notifications from "expo-notifications";
import type { RefObject } from "react";
import type WebView from "react-native-webview";

import { injectNativeEvent, isAllowedOrigin } from "./bridge";
import { config } from "./config";

/**
 * 通知タップのハンドリング。
 * - コールドスタート (通知タップでアプリが起動された) は `getLastNotificationResponseAsync`
 * - 起動中のタップは `addNotificationResponseReceivedListener`
 * のどちらでも同じ処理をする: `data.url` が許可された origin ならその URL に WebView を
 * 遷移させ、いずれにせよ Web ページへ `notification` イベントを送る。
 */
export function useNotificationNavigation(webViewRef: RefObject<WebView | null>): void {
  useEffect(() => {
    let cancelled = false;

    function handleData(data: Record<string, unknown> | undefined) {
      if (!data) return;
      const webView = webViewRef.current;
      if (!webView) return;

      const url = typeof data.url === "string" ? data.url : undefined;
      if (url && isAllowedOrigin(url, config.allowedOrigins)) {
        webView.injectJavaScript(`window.location.href = ${JSON.stringify(url)}; true;`);
      }
      webView.injectJavaScript(injectNativeEvent({ type: "notification", data }));
    }

    Notifications.getLastNotificationResponseAsync().then((response) => {
      if (cancelled || !response) return;
      handleData(response.notification.request.content.data);
    });

    const subscription = Notifications.addNotificationResponseReceivedListener((response) => {
      handleData(response.notification.request.content.data);
    });

    return () => {
      cancelled = true;
      subscription.remove();
    };
  }, [webViewRef]);
}
