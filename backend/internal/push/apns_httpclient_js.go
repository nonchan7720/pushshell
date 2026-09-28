//go:build js && wasm

package push

import "net/http"

// apnsWasmHTTPClient overrides apns2's Client.HTTPClient with
// http.DefaultClient when built for js/wasm (Cloudflare Workers,
// cmd/worker). apns2.NewTokenClient normally builds its own
// golang.org/x/net/http2 transport that dials TCP/TLS directly, which has
// no meaning inside the Workers sandbox: there is no raw socket access, only
// the JS fetch API. In a GOOS=js build, Go's net/http default transport is
// implemented on top of fetch, so requests to APNs go out the same way
// Expo/FCM requests already do. This means APNs push from Workers rides
// HTTP/1.1-over-fetch rather than APNs' usual HTTP/2, which Apple's servers
// accept.
func apnsWasmHTTPClient() *http.Client {
	return http.DefaultClient
}
