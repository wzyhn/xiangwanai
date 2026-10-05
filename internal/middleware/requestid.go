package middleware

import (
	"context"

	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RequestIDKey is the canonical header name and gin-context key for the
// per-request correlation id. Kept as a string for backward compatibility
// with c.Get / c.GetString call sites; the typed ctxKey below is the
// recommended access path for non-gin code.
const RequestIDKey = requestctx.RequestIDHeader

// WithRequestID returns a new context carrying id. Pairs with
// RequestIDFromContext for non-gin call sites (workers, modules, tests).
func WithRequestID(ctx context.Context, id string) context.Context {
	return requestctx.WithRequestID(ctx, id)
}

// RequestIDFromContext returns the request id stored in ctx, or "" if
// missing. Safe to call on any ctx (including nil).
func RequestIDFromContext(ctx context.Context) string {
	return requestctx.RequestID(ctx)
}

// RequestID is the gin middleware that assigns each request a stable
// correlation id (preserving an inbound X-Request-ID header if present)
// and threads it through three places so downstream code has a uniform
// way to access it:
//
//  1. gin context: c.GetString(RequestIDKey)
//  2. response header: X-Request-ID echoed back to the client
//  3. request context.Context (via WithRequestID), so non-gin helpers
//     such as logx.FromContext or db.WithContext pick it up without
//     reaching into gin.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(RequestIDKey)
		if id == "" {
			id = uuid.New().String()
		}
		c.Set(RequestIDKey, id)
		c.Header(RequestIDKey, id)
		c.Request = c.Request.WithContext(WithRequestID(c.Request.Context(), id))
		c.Next()
	}
}
