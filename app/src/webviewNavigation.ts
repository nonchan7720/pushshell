import type WebView from "react-native-webview";

import { isAllowedOrigin } from "./bridge";

/**
 * `url` の origin が許可されていれば WebView をその URL に遷移させる。
 * 遷移させたら true、WebView が無い / origin が許可されていない場合は何もせず false を返す。
 * 通知タップ (`useNotificationNavigation`) とディープリンク (`useDeepLinkNavigation`) で共有する。
 */
export function navigateWebViewTo(
  webView: Pick<WebView, "injectJavaScript"> | null | undefined,
  url: string,
  allowedOrigins: readonly string[],
): boolean {
  if (!webView) return false;
  if (!isAllowedOrigin(url, allowedOrigins)) return false;
  webView.injectJavaScript(`window.location.href = ${JSON.stringify(url)}; true;`);
  return true;
}
