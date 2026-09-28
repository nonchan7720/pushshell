// Package openapivalidate is the full OpenAPI request validator
// (../../../../../openapi/openapi.yaml, via kin-openapi/openapi3filter),
// moved out of internal/httpapi so that package — and therefore
// cmd/worker, which does not use this one — does not need to link
// kin-openapi. Only cmd/server wires Middleware in (as
// httpapi.Options.Validator).
//
// spec.gen.go (generated: see oapi-codegen-spec.yaml and
// ../../../api/generate.go) embeds the spec so this package does not need
// a filesystem/go:embed path back to ../../../../openapi/openapi.yaml,
// which would reach outside its own directory.
package openapivalidate

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	middleware "github.com/oapi-codegen/nethttp-middleware"

	"github.com/nonchan7720/webapp-notification/backend/internal/api"
)

// Middleware builds the kin-openapi request validator: full body/parameter
// schema validation, "unknown route" 404s, and the apiKeyAuth/bearerAuth
// security scheme checks (same rules as httpapi.apiKeyMiddleware plus
// bearerAuth's "must be present" check — see authenticate below).
func Middleware(apiKey string) (func(http.Handler) http.Handler, error) {
	spec, err := GetSpec()
	if err != nil {
		return nil, fmt.Errorf("openapivalidate: load openapi spec: %w", err)
	}
	// servers を消しておかないと Host が一致しないリクエストがルーティングされない。
	spec.Servers = nil

	return middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: authenticate(apiKey),
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
	}), nil
}

// authenticate validates the OpenAPI security schemes
// (../../../../../openapi/openapi.yaml's securitySchemes).
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
			// Presence check only; the token's validity is checked by
			// core.Authorizer.
			if bearerFromRequest(in.RequestValidationInput.Request) == "" {
				return errors.New("missing bearer token")
			}
			return nil
		default:
			return fmt.Errorf("unknown security scheme %q", in.SecuritySchemeName)
		}
	}
}

// bearerFromRequest extracts the Authorization: Bearer token, or "" if
// there is none. Deliberately not shared with httpapi.BearerFromRequest —
// this package intentionally does not depend on internal/transport/httpapi
// (that dependency would run the other way: cmd/server wires this
// package's Middleware in as httpapi.Options.Validator).
func bearerFromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(api.Error{Code: code, Message: message})
}
