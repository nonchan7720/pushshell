package httpapi

import (
	"context"
	"crypto/subtle"
	"net/http"

	"github.com/nonchan7720/webapp-notification/backend/internal/api"
	"github.com/nonchan7720/webapp-notification/backend/internal/core"
)

// apiKeyOperations is which operations (api.StrictServerInterface method
// names, matching the operationID api.StrictMiddlewareFunc is called with)
// require the apiKeyAuth security scheme, per ../../../openapi/openapi.yaml.
// The remaining operations either need no auth (Healthz) or only ever list
// bearerAuth/{} (RegisterDevice, UnregisterDevice, UnregisterDeviceLogin) —
// {} being one of the alternatives means they never reject on missing/wrong
// headers here; a deployment that wants to actually verify the bearer token
// implements core.Authorizer.
var apiKeyOperations = map[string]bool{
	"GetDevice":        true,
	"UnregisterLogin":  true,
	"SendNotification": true,
}

// apiKeyMiddleware is an api.StrictMiddlewareFunc that rejects requests to
// apiKeyOperations unless X-API-Key matches apiKey (constant-time compare,
// same as the previous internal/server's kin-openapi AuthenticationFunc).
// It does nothing for every other operation. Replaces kin-openapi's
// per-operation "security:" enforcement without depending on kin-openapi —
// see openapivalidate for the (optional, cmd/server-only) full OpenAPI
// request validator, which layers additional checks on top of this.
func apiKeyMiddleware(apiKey string) api.StrictMiddlewareFunc {
	return func(next api.StrictHandlerFunc, operationID string) api.StrictHandlerFunc {
		if !apiKeyOperations[operationID] {
			return next
		}
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
			got := r.Header.Get("X-API-Key")
			if apiKey == "" || subtle.ConstantTimeCompare([]byte(got), []byte(apiKey)) != 1 {
				return nil, core.ErrUnauthorized
			}
			return next(ctx, w, r, request)
		}
	}
}
