package contentsecurity

import (
	"context"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/pkg/wechat"
)

// WechatChecker implements Checker using the WeChat msgSecCheck v2 API.
// It delegates to *wechat.MiniApp, which already manages access_token
// fetch + in-memory TTL cache (miniapp.go:184-245).
type WechatChecker struct {
	miniApp   *wechat.MiniApp
	appID     string
	appSecret string
}

// NewWechatChecker creates a WechatChecker for the given app credentials.
// miniApp is shared with other wechat callers (QR code, subscribe message, etc.)
// so token cache hits are reused across callers.
func NewWechatChecker(miniApp *wechat.MiniApp, appID, appSecret string) *WechatChecker {
	return &WechatChecker{
		miniApp:   miniApp,
		appID:     appID,
		appSecret: appSecret,
	}
}

// CheckText calls WeChat msgSecCheck v2 for the given text.
// Returns an error on transport/token failure (caller must fail-open).
// The appID parameter on the interface is ignored — this checker is
// already bound to a specific app at construction time (NewWechatChecker).
// It is kept in the interface so callers that hold multiple checkers keyed
// by appID can dispatch correctly.
func (w *WechatChecker) CheckText(ctx context.Context, _ /*appID*/, openID, text string, scene int) (Result, error) {
	if scene == 0 {
		scene = 1 // WeChat default: resource
	}
	req := wechat.MsgSecCheckRequest{
		OpenID:  openID,
		Scene:   scene,
		Version: 2,
		Content: text,
	}
	resp, err := w.miniApp.MsgSecCheck(ctx, w.appID, w.appSecret, req)
	if err != nil {
		return Result{}, fmt.Errorf("contentsecurity: wechat msgSecCheck failed: %w", err)
	}
	return Result{
		Suggest: Suggest(resp.Result.Suggest),
		Label:   resp.Result.Label,
		TraceID: resp.TraceID,
	}, nil
}
