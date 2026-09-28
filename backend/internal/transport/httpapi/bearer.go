package httpapi

import (
	"context"
	"net/http"
	"strings"
)

type ctxKey int

const bearerCtxKey ctxKey = iota

// WithBearer puts the Authorization: Bearer token into ctx, for
// BearerFromContext to read back later. New's mux does this for every
// request (see bearerMiddleware in httpapi.go) before calling the strict
// handler, so Server's methods can pass it on to core.Service.
func WithBearer(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, bearerCtxKey, token)
}

// BearerFromContext returns the token WithBearer put into ctx, or "".
func BearerFromContext(ctx context.Context) string {
	v, _ := ctx.Value(bearerCtxKey).(string)
	return v
}

// BearerFromRequest extracts the token from r's Authorization: Bearer
// header, or "" if there is none.
func BearerFromRequest(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}
