package xiangwanapi

import (
	"context"
	"errors"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/pkg/contentsecurity"
	"github.com/google/uuid"
)

type fakeNicknameChecker struct {
	result contentsecurity.Result
	err    error
}

func (checker fakeNicknameChecker) CheckText(
	context.Context, string, string, string, int,
) (contentsecurity.Result, error) {
	return checker.result, checker.err
}

func TestWechatNicknameModeratorFailsClosedAndRejectsRiskyText(t *testing.T) {
	t.Parallel()
	principalID := uuid.New()
	resolver := func(context.Context, uuid.UUID, string) (string, error) {
		return "openid", nil
	}
	tests := []struct {
		name       string
		checker    fakeNicknameChecker
		resolveErr error
		want       error
	}{
		{
			name:    "risky",
			checker: fakeNicknameChecker{result: contentsecurity.Result{Suggest: contentsecurity.SuggestRisky}},
			want:    ErrNicknameRejected,
		},
		{
			name:    "provider error",
			checker: fakeNicknameChecker{err: errors.New("provider unavailable")},
			want:    ErrNicknameModerationUnavailable,
		},
		{
			name:       "identity error",
			checker:    fakeNicknameChecker{result: contentsecurity.Result{Suggest: contentsecurity.SuggestPass}},
			resolveErr: errors.New("identity unavailable"),
			want:       ErrNicknameModerationUnavailable,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resolve := resolver
			if tc.resolveErr != nil {
				resolve = func(context.Context, uuid.UUID, string) (string, error) {
					return "", tc.resolveErr
				}
			}
			moderator := NewWechatNicknameModerator(tc.checker, resolve, "wx1234567890123456")
			if err := moderator.CheckNickname(context.Background(), principalID, "昵称"); !errors.Is(err, tc.want) {
				t.Fatalf("CheckNickname() error=%v, want %v", err, tc.want)
			}
		})
	}
}
