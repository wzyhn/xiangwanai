package logx

import (
	"context"

	"go.uber.org/zap"

	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
)

// Detach returns a new context that inherits logger + request_id from src,
// but is not bound to src's cancellation/deadline. Use when the work must
// continue past the request lifecycle (worker dispatch, deferred publish,
// async fan-out) yet should still emit logs traceable to the originating
// request via the request_id field.
//
// Detach is the canonical replacement for `context.Background()` at hand-off
// points; using bare Background drops the logger + request_id and breaks the
// "5 分钟根因" promise (target §1 #6).
//
// The returned context has no Deadline / Done channel — callers that need a
// bound on the deferred work should layer context.WithTimeout on top.
func Detach(src context.Context) context.Context {
	bg := context.Background()
	if src == nil {
		return bg
	}
	if logger, ok := src.Value(loggerCtxKey{}).(*zap.Logger); ok && logger != nil {
		bg = WithContext(bg, logger)
	}
	if rid := requestctx.RequestID(src); rid != "" {
		bg = requestctx.WithRequestID(bg, rid)
	}
	return bg
}
