import { useCallback, useEffect, useRef } from "react";
import * as Linking from "expo-linking";
import type { RefObject } from "react";
import type WebView from "react-native-webview";

import { isAllowedOrigin } from "./bridge";
import { config } from "./config";
import { navigateWebViewTo } from "./webviewNavigation";

/**
 * Universal Links (iOS) / App Links (Android) のハンドリング。
 * - コールドスタート (リンクをタップしてアプリが起動された) は `Linking.getInitialURL()`
 * - 起動中に受け取ったリンクは `Linking.addEventListener("url", ...)`
 * のどちらでも同じ処理をする: `http(s)` かつ許可された origin の URL だけ WebView を遷移させ、
 * それ以外 (`APP_SCHEME://...` などのカスタムスキーム、未知の origin) は警告を出して無視する。
 *
 * コールドスタート時は、`getInitialURL()` が解決した時点で WebView がまだ最初のページ
 * (`WEBAPP_URL`) を読み込み終えていないことがある。その状態で遷移させると、進行中の初回
 * ロードに上書きされて無視されてしまう。そこで初回ロードが終わるまでは URL を `pendingUrl` に
 * 保持しておき、App.tsx の WebView `onLoadEnd` から返り値の `applyPendingDeepLink` を呼んで
 * 適用する。初回ロード後に届いたリンクは即座に遷移させる。
 */
export function useDeepLinkNavigation(webViewRef: RefObject<WebView | null>): {
  applyPendingDeepLink: () => void;
} {
  // WebView が最初のロードを終えたか。
  const loadedRef = useRef(false);
  // 初回ロード待ちの (許可済みの) URL。
  const pendingUrlRef = useRef<string | null>(null);

  const applyPendingDeepLink = useCallback(() => {
    loadedRef.current = true;
    const url = pendingUrlRef.current;
    if (!url) return;
    if (navigateWebViewTo(webViewRef.current, url, config.allowedOrigins)) {
      pendingUrlRef.current = null;
    }
  }, [webViewRef]);

  useEffect(() => {
    let cancelled = false;

    function handleUrl(url: string | null) {
      if (!url) return;
      if (!/^https?:\/\//i.test(url) || !isAllowedOrigin(url, config.allowedOrigins)) {
        console.warn("[DeepLink] 対象外の URL を無視:", url);
        return;
      }
      pendingUrlRef.current = url;
      if (loadedRef.current) applyPendingDeepLink();
    }

    Linking.getInitialURL()
      .then((url) => {
        if (cancelled) return;
        handleUrl(url);
      })
      .catch((error) => {
        console.warn("[DeepLink] getInitialURL に失敗しました:", error);
      });

    const subscription = Linking.addEventListener("url", (event) => {
      handleUrl(event.url);
    });

    return () => {
      cancelled = true;
      subscription.remove();
    };
  }, [applyPendingDeepLink]);

  return { applyPendingDeepLink };
}
