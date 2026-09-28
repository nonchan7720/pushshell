package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/nonchan7720/webapp-notification/backend/internal/core"
)

// mapError is the fallback for errors that reach api.StrictHTTPServerOptions.ResponseErrorHandlerFunc:
// either an unexpected error from a Server method, or apiKeyMiddleware's
// core.ErrUnauthorized. Most core.ErrInvalidInput/core.ErrUnauthorized cases
// are already mapped to a typed 400/401 JSON response inline in server.go
// (where the OpenAPI spec documents one for that operation); this is the
// catch-all for the operations that do not document one (UnregisterDevice,
// UnregisterDeviceLogin, UnregisterLogin, GetDevice — see
// ../../../openapi/openapi.yaml) plus apiKeyMiddleware's rejections, so a
// request still gets the right status code and body shape instead of a 500.
func mapError(logger *slog.Logger, w http.ResponseWriter, r *http.Request, err error) {
	var inv core.ErrInvalidInput
	switch {
	case errors.As(err, &inv):
		writeError(w, http.StatusBadRequest, "invalid_request", inv.Message)
	case errors.Is(err, core.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "unauthorized", "unauthorized")
	case errors.Is(err, core.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	default:
		logger.Error("handler error", "method", r.Method, "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "internal server error")
	}
}
