//go:build !(js && wasm)

package push

import "net/http"

// wasmTransport returns nil on every platform except js/wasm (see
// httpclient_js.go), leaving ExpoSender/FCMSender/APNSSender's HTTP clients
// built exactly as they always were: a plain *http.Client (or apns2's own
// http2.Transport-backed one), i.e. no change in behavior for cmd/server.
func wasmTransport() http.RoundTripper {
	return nil
}
