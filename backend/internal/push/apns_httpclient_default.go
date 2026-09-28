//go:build !(js && wasm)

package push

import "net/http"

// apnsWasmHTTPClient returns nil on every platform except js/wasm, so
// NewAPNSSender keeps using apns2's own http2.Transport-backed client
// unchanged (see apns_httpclient_js.go for the js/wasm override).
func apnsWasmHTTPClient() *http.Client {
	return nil
}
