//go:build js && wasm

package push

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/syumai/workers-go/cloudflare/fetch"
)

// newHTTPClient returns the HTTPDoer used when none is injected, for the
// js/wasm build (cmd/worker): github.com/syumai/workers-go/cloudflare/fetch's
// RoundTripper, called directly, instead of an *http.Client.
//
// Two separate reasons, both confirmed empirically on Cloudflare Workers:
//
//  1. Correctness: Go's own js/wasm RoundTripper (net/http/roundtrip_js.go)
//     calls the global fetch as fetch(url, options), which workerd rejects
//     with "JavaScript error: Illegal invocation" (seen via
//     `mise run worker:dev`, PUSH_PROVIDER=expo, POST /v1/notifications).
//     workers-go's fetch package builds a proper Request object and calls
//     fetch(request, init), which workerd accepts.
//
//  2. Size: (*http.Client).Do falls back to http.DefaultTransport when
//     Client.Transport is nil, so merely calling Do — even on a client
//     whose Transport is always set — makes the linker keep the whole
//     *http.Transport, and with it crypto/tls, crypto/x509, the root-CA
//     handling and every cipher they reach: ~4.8MB raw / ~1.1MB gzip of
//     wasm this worker never executes (Workers' own fetch does TLS).
//     Calling the RoundTripper directly keeps none of that, which is the
//     single biggest lever for staying under the free plan's 3MB gzip
//     limit (see worker/build.sh and the size gate in .github/workflows/ci.yml).
//     For the same reason no code reachable from cmd/worker may call
//     (*http.Client).Do / Get / Post; HTTPDoer exists so the senders don't.
func newHTTPClient(timeout time.Duration) HTTPDoer {
	return &fetchDoer{
		rt:      fetch.NewClient().HTTPClient(fetch.RedirectModeFollow).Transport,
		timeout: timeout,
	}
}

// fetchDoer is the js/wasm HTTPDoer: one fetch-backed RoundTrip per Do,
// with the timeout applied through the request's context (best effort:
// whether the fetch itself observes cancellation is up to the runtime, and
// Workers imposes its own per-request limits regardless).
type fetchDoer struct {
	rt      http.RoundTripper
	timeout time.Duration
}

func (d *fetchDoer) Do(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(req.Context(), d.timeout)
	resp, err := d.rt.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose releases the request's timeout context when the body is
// closed (the same contract *http.Client keeps for its Timeout).
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}
