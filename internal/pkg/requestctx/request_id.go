// Package requestctx owns request-scoped correlation primitives without
// depending on an HTTP framework, middleware stack, cache, or Redis.
package requestctx

import "context"

// RequestIDHeader is the canonical inbound and outbound correlation header.
const RequestIDHeader = "X-Request-ID"

type requestIDKey struct{}

// WithRequestID returns a context carrying id. A nil context is treated as a
// background context so worker and defensive call sites remain safe.
func WithRequestID(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the correlation id carried by ctx, or an empty string.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
