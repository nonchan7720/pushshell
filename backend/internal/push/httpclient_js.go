//go:build js && wasm

package push

import (
	"net/http"

	"github.com/syumai/workers-go/cloudflare/fetch"
)

// wasmTransport backs ExpoSender/FCMSender/APNSSender's outbound HTTP calls
// with github.com/syumai/workers-go/cloudflare/fetch instead of Go's stock
// net/http default transport, when built for js/wasm (cmd/worker).
//
// Both ultimately reach the JS fetch API in a js/wasm build (net/http's own
// RoundTripper is net/http/roundtrip_js.go there), so in principle the
// stock transport should already have worked — but confirmed empirically
// against real Cloudflare Workers (`mise run worker:dev`, PUSH_PROVIDER=expo,
// POST /v1/notifications with a dummy ExponentPushToken), it throws
// "JavaScript error: Illegal invocation" from inside
// net/http.(*Transport).RoundTrip (net/http/roundtrip_js.go calls the
// global fetch as fetch(url, options), which workerd's fetch implementation
// rejects). github.com/syumai/workers-go/cloudflare/fetch (used by this
// library's own D1/KV/R2 bindings under the hood) instead builds a proper
// Request object and calls fetch(request, init), which workerd accepts —
// this is the one change to internal/push cmd/worker actually needs beyond
// what internal/db.Open+config already provide (see cmd/worker/main.go).
func wasmTransport() http.RoundTripper {
	return fetch.NewClient().HTTPClient(fetch.RedirectModeFollow).Transport
}
