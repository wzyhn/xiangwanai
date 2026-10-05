package xiangwanapi

import (
	"context"
	"errors"
	"fmt"

	wechatpkg "github.com/wzyhn/xiangwanai/internal/pkg/wechat"
)

// ErrAvatarImageRejected means WeChat content-security flagged the uploaded
// avatar image as risky. The upload is rejected and nothing is stored.
var ErrAvatarImageRejected = errors.New("avatar image rejected by content security")

// ErrAvatarModerationUnavailable means the avatar moderation service could
// not produce a verdict (transport failure, token failure, provider error).
// The upload fails closed: nothing is stored until a verdict exists.
var ErrAvatarModerationUnavailable = errors.New("avatar moderation unavailable")

// AvatarModerator screens one uploaded avatar image through server-side
// content-security review before it may be published. A direct HTTP client
// can bypass the Mini Program chooseAvatar component, so the gate must live
// on the server. A nil moderator disables the gate (explicit local-dev
// switch); every non-nil outcome is fail-closed.
type AvatarModerator interface {
	CheckAvatarImage(ctx context.Context, data []byte, filename string) error
}

// WechatAvatarModerator implements AvatarModerator with the synchronous
// WeChat wxa/img_sec_check API, reusing the MiniApp access-token cache the
// WeChat login path already uses.
type WechatAvatarModerator struct {
	miniApp   *wechatpkg.MiniApp
	appID     string
	appSecret string
}

func NewWechatAvatarModerator(
	miniApp *wechatpkg.MiniApp,
	appID string,
	appSecret string,
) *WechatAvatarModerator {
	return &WechatAvatarModerator{miniApp: miniApp, appID: appID, appSecret: appSecret}
}

func (moderator *WechatAvatarModerator) CheckAvatarImage(
	ctx context.Context,
	data []byte,
	filename string,
) error {
	if moderator == nil || moderator.miniApp == nil {
		return ErrAvatarModerationUnavailable
	}
	response, err := moderator.miniApp.ImgSecCheck(
		ctx, moderator.appID, moderator.appSecret, data, filename,
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAvatarModerationUnavailable, err)
	}
	switch response.ErrCode {
	case 0:
		return nil
	case 87014:
		return ErrAvatarImageRejected
	default:
		return fmt.Errorf(
			"%w: img_sec_check errcode %d %s",
			ErrAvatarModerationUnavailable, response.ErrCode, response.ErrMsg,
		)
	}
}
