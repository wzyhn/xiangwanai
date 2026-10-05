// Package wechat — outbound HTTP correlation helper.
//
// PR-E (audit retry P1, 2026-05-10): propagate the request_id stored in
// ctx (by middleware.RequestID) onto every WeChat-bound HTTP request via
// the X-Request-ID header, so upstream WeChat call sites are traceable
// end-to-end alongside server logs.
package wechat

import (
	"context"
	"net/http"

	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
)

// ensureCtx returns ctx unchanged if non-nil; otherwise returns
// context.Background(). Defensive helper for outbound WeChat HTTP entrypoints
// so a nil ctx (background tasks, legacy callers) cannot panic
// http.NewRequestWithContext (PR-β safety sweep, 2026-05-11).
func ensureCtx(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// applyRequestIDHeader copies the request_id (if any) from req.Context()
// onto the outbound X-Request-ID header. Safe no-op when ctx carries no
// request id (e.g. background tasks not initiated from a gin handler).
func applyRequestIDHeader(req *http.Request) {
	if req == nil {
		return
	}
	id := requestctx.RequestID(req.Context())
	if id == "" {
		return
	}
	req.Header.Set(requestctx.RequestIDHeader, id)
}
