package push

import (
	"net/http"
	"time"
)

// defaultHTTPTimeout is the per-request timeout of the HTTP client
// ExpoSender / FCMSender / APNSSender build when none is injected.
const defaultHTTPTimeout = 10 * time.Second

// HTTPDoer is the part of *http.Client the senders use for their outbound
// calls (Expo Push API, FCM HTTP v1 + Google's token endpoint, APNs).
// *http.Client satisfies it, so tests (and callers) can still inject
// httptest.Server.Client(); it is an interface rather than *http.Client so
// the js/wasm build (cmd/worker) can use a fetch-backed implementation
// without ever calling (*http.Client).Do — see httpclient_js.go for why
// that matters (it is worth ~1.1MB of gzip in the worker's wasm).
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}
