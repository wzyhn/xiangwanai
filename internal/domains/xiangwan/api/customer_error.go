package xiangwanapi

import (
	"errors"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// writeError keeps the Xiangwan API contract intact while ensuring that
// customer-facing 4xx responses contain actionable Chinese copy rather than
// internal aggregate, storage, or authentication terminology.
func writeError(c *gin.Context, err error) {
	response.Err(c, customerError(err))
}

// WriteCustomerError is the exported boundary for runtime-level middleware
// (consumer authentication) that must not leak internal terminology either.
func WriteCustomerError(c *gin.Context, err error) {
	writeError(c, err)
}

func writeErrorWithData(c *gin.Context, err error, data any) {
	response.ErrWithData(c, customerError(err), data)
}

func customerError(err error) error {
	if err == nil {
		return nil
	}
	var typed *errx.Error
	if !errors.As(err, &typed) {
		return err
	}
	message := customerErrorMessage(typed.Code, typed.Message)
	if message == "" {
		return err
	}
	return errx.New(typed.Code, message)
}

func customerErrorMessage(code errx.Code, original string) string {
	switch code {
	case errx.CodeBadRequest:
		// The avatar moderation outcomes carry reviewed customer-facing copy
		// that must reach the client verbatim; everything else keeps the
		// generic guard so internal terminology never crosses the boundary.
		switch original {
		case avatarRejectedCustomerMessage, avatarModerationUnavailableCustomerMessage:
			return original
		}
		return "提交内容有误，请检查后重试"
	case errx.CodeUnauthorized, errx.CodeWechatAuthFail:
		return "登录状态已失效，请重新登录"
	case errx.CodeForbidden, errx.CodePrincipalInactive:
		return "当前账号暂不能执行此操作"
	case errx.CodeNotFound, errx.CodePersonalScopeNotFound:
		return "相关内容不存在或已下线"
	case errx.CodeConflict, errx.CodePersonalScopeMismatch, errx.CodeInvalidStateTransition:
		return conflictCustomerMessage(original)
	case errx.CodeFileTooLarge:
		return "上传内容过大，请压缩后重试"
	case errx.CodeFileTypeNotSupported:
		return "暂不支持这种文件格式"
	case errx.CodeXiangwanAdminSessionInvalid, errx.CodeXiangwanAdminIdentityRejected:
		return "登录状态已失效，请重新登录"
	case errx.CodeXiangwanAdminScopeForbidden, errx.CodeXiangwanAdminCSRFRejected:
		return "当前账号没有此操作权限"
	case errx.CodeXiangwanAdminOperationConflict, errx.CodeXiangwanAdminVersionConflict:
		return "内容已更新，请刷新后重试"
	case errx.CodeXiangwanAdminQuickTagUnavailable:
		return "活动标签配置已变化，请刷新后重试"
	case errx.CodeXiangwanAdminPublicationInvalid:
		return "发布检查未通过，请按提示完善内容"
	case errx.CodeXiangwanAdminTargetNotFound:
		return "相关内容不存在或已下线"
	case errx.CodeXiangwanAdminLoginRateLimited:
		return "尝试次数过多，请稍后再试"
	case errx.CodeInternal:
		// The shared response boundary deliberately replaces every 5xx message
		// and records the original error for operators.
		return ""
	default:
		return ""
	}
}

const prepayRejectedCustomerMessage = "微信未能创建支付，本订单已关闭且未扣款。请联系活动方检查支付配置后重新报名。"

func conflictCustomerMessage(original string) string {
	if original == prepayRejectedCustomerMessage {
		return original
	}
	normalized := strings.ToLower(original)
	switch {
	case strings.Contains(normalized, "capacity"), strings.Contains(normalized, "full"):
		return "名额刚刚发生变化，请刷新后重试"
	case strings.Contains(normalized, "already"):
		return "当前操作已完成，请刷新查看"
	default:
		return "内容已更新，请刷新后重试"
	}
}
