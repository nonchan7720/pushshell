//go:build !(js && wasm)

package push

import (
	"net/http"
	"time"
)

// newHTTPClient returns the HTTPDoer used when none is injected: on every
// platform except js/wasm (see httpclient_js.go) a plain *http.Client using
// net/http's default transport (which negotiates HTTP/2 over TLS on its
// own, as APNs requires), exactly as cmd/server has always done.
func newHTTPClient(timeout time.Duration) HTTPDoer {
	return &http.Client{Timeout: timeout}
}
