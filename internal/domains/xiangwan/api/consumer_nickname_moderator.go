package xiangwanapi

import (
	"context"
	"errors"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/pkg/contentsecurity"
	"github.com/google/uuid"
)

var (
	ErrNicknameRejected              = errors.New("nickname rejected by content security")
	ErrNicknameModerationUnavailable = errors.New("nickname moderation unavailable")
)

// ConsumerNicknameModerator screens a requested nickname before the identity
// writer replaces the currently approved value.
type ConsumerNicknameModerator interface {
	CheckNickname(context.Context, uuid.UUID, string) error
}

// WechatNicknameModerator adapts the shared WeChat text checker to the
// Xiangwan profile route. Any missing identity, provider error, or malformed
// verdict fails closed so the previously approved nickname remains intact.
type WechatNicknameModerator struct {
	checker        contentsecurity.Checker
	openIDResolver func(context.Context, uuid.UUID, string) (string, error)
	appID          string
}

func NewWechatNicknameModerator(
	checker contentsecurity.Checker,
	openIDResolver func(context.Context, uuid.UUID, string) (string, error),
	appID string,
) *WechatNicknameModerator {
	return &WechatNicknameModerator{
		checker: checker, openIDResolver: openIDResolver, appID: strings.TrimSpace(appID),
	}
}

func (moderator *WechatNicknameModerator) CheckNickname(
	ctx context.Context,
	principalID uuid.UUID,
	nickname string,
) error {
	if moderator == nil || moderator.checker == nil || moderator.openIDResolver == nil ||
		ctx == nil || principalID == uuid.Nil || moderator.appID == "" {
		return ErrNicknameModerationUnavailable
	}
	openID, err := moderator.openIDResolver(ctx, principalID, moderator.appID)
	if err != nil || strings.TrimSpace(openID) == "" {
		return ErrNicknameModerationUnavailable
	}
	result, err := moderator.checker.CheckText(ctx, moderator.appID, openID, nickname, 1)
	if err != nil {
		return ErrNicknameModerationUnavailable
	}
	switch result.Suggest {
	case contentsecurity.SuggestPass, contentsecurity.SuggestReview:
		return nil
	case contentsecurity.SuggestRisky:
		return ErrNicknameRejected
	default:
		return ErrNicknameModerationUnavailable
	}
}
