package contentsecurity

import (
	"context"
	"fmt"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/pkg/wechat"
)

// WechatImageModerator implements ImageModerator using the WeChat
// mediaCheckAsync v2 API. It delegates to *wechat.MiniApp, which already manages
// access_token fetch + in-memory TTL cache, mirroring WechatChecker for text.
//
// media_type is fixed to 2 (image); mediaCheckAsync only supports image / audio
// and this capability is image-only (WeChat image-content-security mandate).
type WechatImageModerator struct {
	miniApp   *wechat.MiniApp
	appID     string
	appSecret string
}

// NewWechatImageModerator creates a WechatImageModerator for the given app
// credentials. miniApp is shared with other wechat callers (text msgSecCheck,
// QR code, subscribe message, etc.) so token cache hits are reused.
func NewWechatImageModerator(miniApp *wechat.MiniApp, appID, appSecret string) *WechatImageModerator {
	return &WechatImageModerator{
		miniApp:   miniApp,
		appID:     appID,
		appSecret: appSecret,
	}
}

// CheckImageAsync calls WeChat mediaCheckAsync v2 for the given image URL.
// Returns an error on transport/token failure (caller must fail-open).
// The appID parameter on the interface is ignored — this moderator is already
// bound to a specific app at construction time (NewWechatImageModerator),
// mirroring WechatChecker.CheckText.
func (w *WechatImageModerator) CheckImageAsync(ctx context.Context, _ /*appID*/, openID, mediaURL string, scene int) (MediaCheckResult, error) {
	if strings.TrimSpace(mediaURL) == "" {
		return MediaCheckResult{}, fmt.Errorf("contentsecurity: empty media url")
	}
	if scene == 0 {
		scene = 1 // WeChat default: resource
	}
	req := wechat.MediaCheckAsyncRequest{
		OpenID:    openID,
		Scene:     scene,
		Version:   2,
		MediaURL:  mediaURL,
		MediaType: 2, // 2 = image
	}
	resp, err := w.miniApp.MediaCheckAsync(ctx, w.appID, w.appSecret, req)
	if err != nil {
		return MediaCheckResult{}, fmt.Errorf("contentsecurity: wechat mediaCheckAsync failed: %w", err)
	}
	return MediaCheckResult{TraceID: strings.TrimSpace(resp.TraceID)}, nil
}
