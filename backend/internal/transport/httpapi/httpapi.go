// Package httpapi builds the http.Handler for this backend's API
// (../../../openapi/openapi.yaml): the api.StrictServerInterface
// implementation (server.go, calling core.Service), the X-API-Key check for
// apiKeyAuth operations, bearer extraction, panic recovery and request
// logging. It depends on internal/core and internal/api only — no ent, no
// kin-openapi — so it (and therefore cmd/worker) can be built for
// GOOS=js GOARCH=wasm without linking either.
//
// The one thing it does *not* do is full OpenAPI request validation (body
// schema, path/query parameter shape, "known route" 404s): that lives in
// the sibling package internal/transport/httpapi/openapivalidate, which
// does depend on kin-openapi and is wired in as an optional Validator by
// cmd/server only. cmd/worker uses New without one.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/nonchan7720/webapp-notification/backend/internal/api"
	"github.com/nonchan7720/webapp-notification/backend/internal/core"
)

// Options configures New.
type Options struct {
	// Service implements the business logic (internal/core). Required.
	Service *core.Service
	// APIKey is the key required by apiKeyAuth operations (X-API-Key
	// header). If empty, those operations always reject with 401.
	APIKey string
	Logger *slog.Logger
	// Validator, if set, wraps the whole mux with full OpenAPI request
	// validation (see internal/transport/httpapi/openapivalidate.Middleware).
	// cmd/server sets it; cmd/worker leaves it nil.
	Validator func(http.Handler) http.Handler
}

// New builds the http.Handler for the API: the strict handler (server.go)
// wrapped with an apiKeyAuth check (auth.go), bearer extraction, panic
// recovery, request logging and, if opts.Validator is set, full OpenAPI
// request validation.
func New(opts Options) (http.Handler, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	srv := &server{svc: opts.Service}
	strict := api.NewStrictHandlerWithOptions(srv, []api.StrictMiddlewareFunc{apiKeyMiddleware(opts.APIKey)}, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			mapError(logger, w, r, err)
		},
	})

	mux := http.NewServeMux()
	api.HandlerFromMux(strict, mux)

	var h http.Handler = mux
	if opts.Validator != nil {
		h = opts.Validator(h)
	}
	h = bearerMiddleware(h)
	h = recoverer(logger)(h)
	h = requestLogger(logger)(h)
	return h, nil
}

func bearerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := BearerFromRequest(r); tok != "" {
			r = r.WithContext(WithBearer(r.Context(), tok))
		}
		next.ServeHTTP(w, r)
	})
}

func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					logger.Error("panic", "err", rec, "stack", string(debug.Stack()))
					writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			logger.Info("request",
				"method", r.Method, "path", r.URL.Path, "status", sw.status,
				"duration", time.Since(start).String(), "remote", r.RemoteAddr)
		})
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.Error{Code: code, Message: message})
}
