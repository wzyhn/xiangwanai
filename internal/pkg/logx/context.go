package logx

import (
	"context"

	"go.uber.org/zap"

	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
)

// loggerCtxKey is the unexported context key used to stash a *zap.Logger
// (typically the root logger created by MustInit) so non-gin call sites can
// retrieve it without depending on gin.Context.
type loggerCtxKey struct{}

// WithContext attaches logger to ctx. Returns ctx unchanged if logger is nil
// (callers should not need to nil-guard before calling).
func WithContext(ctx context.Context, logger *zap.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if logger == nil {
		return ctx
	}
	return context.WithValue(ctx, loggerCtxKey{}, logger)
}

// FromContext returns the *zap.Logger stored in ctx, or zap.NewNop() if none
// was attached. The returned logger is automatically bound to the
// `request_id` field if middleware.RequestIDFromContext(ctx) is non-empty.
//
// This is the recommended way for modules / handlers / workers to obtain a
// logger: they do not need to reach into gin.Context, and they do not need
// to remember to add request_id manually.
func FromContext(ctx context.Context) *zap.Logger {
	if ctx == nil {
		return zap.NewNop()
	}
	logger, ok := ctx.Value(loggerCtxKey{}).(*zap.Logger)
	if !ok || logger == nil {
		logger = zap.NewNop()
	}
	if rid := requestctx.RequestID(ctx); rid != "" {
		logger = logger.With(zap.String("request_id", rid))
	}
	return logger
}
