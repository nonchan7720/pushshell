// Package server は HTTP サーバーの組み立て (ルーティング・ミドルウェア・認証) を行う。
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	middleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/nonchan7720/webapp-notification/backend/internal/api"
	"github.com/nonchan7720/webapp-notification/backend/internal/handler"
)

// Options はサーバーの依存と設定。
type Options struct {
	Handler *handler.Handler
	// APIKey は X-API-Key で保護されたエンドポイントのキー。空なら常に 401。
	APIKey string
	Logger *slog.Logger
}

// New は http.Handler を組み立てる。
//
// リクエストは OpenAPI スキーマに対して検証され (kin-openapi)、
// security scheme (apiKeyAuth / bearerAuth) の検証もここで行う。
func New(opts Options) (http.Handler, error) {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	spec, err := api.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load openapi spec: %w", err)
	}
	// servers を消しておかないと Host が一致しないリクエストがルーティングされない。
	spec.Servers = nil

	mux := http.NewServeMux()
	strict := api.NewStrictHandlerWithOptions(opts.Handler, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Error("handler error", "method", r.Method, "path", r.URL.Path, "err", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
		},
	})
	api.HandlerFromMux(strict, mux)

	validator := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: authenticate(opts.APIKey),
			MultiError:         false,
		},
		ErrorHandlerWithOpts: func(_ context.Context, err error, w http.ResponseWriter, _ *http.Request, eo middleware.ErrorHandlerOpts) {
			code := "invalid_request"
			msg := err.Error()
			switch eo.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				code, msg = "unauthorized", "unauthorized"
			case http.StatusNotFound:
				code, msg = "not_found", "no such route"
			case http.StatusMethodNotAllowed:
				code, msg = "method_not_allowed", "method not allowed"
			}
			var se *openapi3filter.SecurityRequirementsError
			if errors.As(err, &se) {
				code, msg = "unauthorized", "unauthorized"
				eo.StatusCode = http.StatusUnauthorized
			}
			var re *openapi3filter.RequestError
			if errors.As(err, &re) && re.Err != nil {
				msg = re.Error()
			}
			if errors.Is(err, routers.ErrPathNotFound) {
				code, msg = "not_found", "no such route"
				eo.StatusCode = http.StatusNotFound
			}
			writeError(w, eo.StatusCode, code, msg)
		},
	})

	h := validator(mux)
	h = withBearer(h)
	h = recoverer(logger)(h)
	h = requestLogger(logger)(h)
	return h, nil
}

// authenticate は OpenAPI の security scheme を検証する。
func authenticate(apiKey string) openapi3filter.AuthenticationFunc {
	return func(_ context.Context, in *openapi3filter.AuthenticationInput) error {
		switch in.SecuritySchemeName {
		case "apiKeyAuth":
			if apiKey == "" {
				return errors.New("API_KEY is not configured")
			}
			got := in.RequestValidationInput.Request.Header.Get("X-API-Key")
			if subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) != 1 {
				return errors.New("invalid api key")
			}
			return nil
		case "bearerAuth":
			// 存在チェックのみ。中身の検証は handler.Authorizer が行う。
			if handler.BearerFromRequest(in.RequestValidationInput.Request) == "" {
				return errors.New("missing bearer token")
			}
			return nil
		default:
			return fmt.Errorf("unknown security scheme %q", in.SecuritySchemeName)
		}
	}
}

func withBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := handler.BearerFromRequest(r); tok != "" {
			r = r.WithContext(handler.WithBearer(r.Context(), tok))
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
