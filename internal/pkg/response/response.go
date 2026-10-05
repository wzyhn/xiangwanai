package response

import (
	"errors"
	"net/http"

	"github.com/wzyhn/xiangwanai/internal/pkg/errx"

	"github.com/gin-gonic/gin"
)

type Body struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func OK(c *gin.Context, data any) {
	c.PureJSON(http.StatusOK, Body{
		Code:    0,
		Message: "ok",
		Data:    data,
	})
}

func Created(c *gin.Context, data any) {
	c.PureJSON(http.StatusCreated, Body{
		Code:    0,
		Message: "created",
		Data:    data,
	})
}

// ErrWithData writes a JSON error response identical to Err but additionally
// populates the Body.Data field with data. Use this when the error carries
// structured detail that the client must read (e.g., a 409 Conflict that
// lists which resources are blocking the operation). The HTTP status and code
// are resolved from the error chain exactly as in Err.
func ErrWithData(c *gin.Context, err error, data any) {
	if err == nil {
		c.PureJSON(http.StatusInternalServerError, Body{
			Code:    int(errx.CodeInternal),
			Message: "internal server error",
			Data:    data,
		})
		return
	}
	var e *errx.Error
	if errors.As(err, &e) {
		writeTypedError(c, err, e, data)
		return
	}
	internalErr(c, err, data)
}

// internalErr is the fail-closed fallback for non-errx errors: 500 + generic
// message. The real error is attached to the gin context so the access-log
// line carries it (logx.GinAccessLogger "errors" field) — it must never reach
// the client body (leaks internals, e.g. DB DSN hosts) nor masquerade as 400
// (5xx-alert blind spot; B5 drill 2026-07-11: a DB outage was counted entirely
// as status=400). Client-caused failures must arrive here already wrapped as
// *errx.Error (e.g. errx.NewBadRequest for binding/validation).
func internalErr(c *gin.Context, err error, data any) {
	_ = c.Error(err)
	c.PureJSON(http.StatusInternalServerError, Body{
		Code:    int(errx.CodeInternal),
		Message: "internal server error",
		Data:    data,
	})
}

// Err writes a JSON error response. The error chain is unwrapped via
// errors.As so a wrapped *errx.Error (e.g. fmt.Errorf("layer X: %w",
// errx.NewBadRequest(...))) is recovered and surfaces its real code instead
// of the legacy 400 fallback. This is the round-2 obs P1-4 fix:
// type-asserted err.(*errx.Error) silently dropped wrapped errors and
// emitted 400 + raw err.Error() text — broken contract for any module that
// added context with %w.
func Err(c *gin.Context, err error) {
	// Defensive: a nil error should never reach Err, but legacy call sites
	// occasionally pass through a (possibly-nil) error from a chain. Treat
	// nil as a 500 so we surface the bug loudly rather than panicking on
	// err.Error() (PR-β safety sweep, 2026-05-11).
	if err == nil {
		c.PureJSON(http.StatusInternalServerError, Body{
			Code:    int(errx.CodeInternal),
			Message: "internal server error",
		})
		return
	}
	var e *errx.Error
	if errors.As(err, &e) {
		writeTypedError(c, err, e, nil)
		return
	}
	internalErr(c, err, nil)
}

// writeTypedError preserves public 4xx business messages but treats every 5xx
// message as internal detail. Repository and provider adapters sometimes add
// operational context to an errx error; status classification must not turn
// that context into a client-visible database or provider error. The full
// chain remains attached to Gin for the access logger.
func writeTypedError(c *gin.Context, err error, e *errx.Error, data any) {
	status := codeToHTTP(e.Code)
	message := e.Message
	if status >= http.StatusInternalServerError {
		_ = c.Error(err)
		message = "internal server error"
	}
	c.PureJSON(status, Body{
		Code:    int(e.Code),
		Message: message,
		Data:    data,
	})
}

// codeToHTTP maps an errx.Code to the appropriate HTTP status. Unknown codes
// fall back to 500 InternalServerError so unmapped segments fail loudly
// rather than silently returning 200/400.
func codeToHTTP(code errx.Code) int {
	switch code {
	// generic 10xxx
	case errx.CodeBadRequest:
		return http.StatusBadRequest
	case errx.CodeUnauthorized:
		return http.StatusUnauthorized
	case errx.CodeForbidden:
		return http.StatusForbidden
	case errx.CodeNotFound:
		return http.StatusNotFound
	case errx.CodeConflict:
		return http.StatusConflict
	case errx.CodeInternal:
		return http.StatusInternalServerError

	// auth 20001-20099
	case errx.CodeWechatAuthFail:
		return http.StatusUnauthorized
	// codex bug-sweep B7 (2026-05-12): missing mapping → was returning 500.
	// Invalid state transitions are conflict-class (peer attempted action
	// that contradicts current resource state).
	case errx.CodeInvalidStateTransition:
		return http.StatusConflict
	// ADR-034 live ActivePrincipalGate: JWT 有效但主体 inactive → 统一 403。
	case errx.CodePrincipalInactive:
		return http.StatusForbidden

	// tenancy 20101-20199 (ADR-034 personal scope resolver)
	case errx.CodePersonalScopeNotFound:
		return http.StatusNotFound
	case errx.CodePersonalScopeMismatch:
		return http.StatusConflict

	// storage 20301-20399
	case errx.CodeFileTooLarge:
		return http.StatusRequestEntityTooLarge
	case errx.CodeFileTypeNotSupported:
		return http.StatusBadRequest

	// community 30001-30099
	case errx.CodeCommunityPostNotFound, errx.CodeCommunityCommentNotFound:
		return http.StatusNotFound
	case errx.CodeCommunityPostNotEditable:
		return http.StatusConflict
	case errx.CodeCommunityPostUnderReview:
		return http.StatusForbidden
	case errx.CodeCommunityShareTargetExpired:
		return http.StatusGone
	case errx.CodeCommunityContactRateLimit:
		return http.StatusTooManyRequests
	case errx.CodeCommunityModerationRejected:
		return http.StatusUnprocessableEntity

	// study 30101-30199 (note / parse — schedule/timetable 移到 30401-30499)
	case errx.CodeStudyNoteNotFound:
		return http.StatusNotFound
	case errx.CodeStudyParseUnsupportedType:
		return http.StatusUnsupportedMediaType

	// record 30201-30299 (audit_status=pending 不进此段;走 success payload)
	case errx.CodeRecordMomentNotFound:
		return http.StatusNotFound
	case errx.CodeRecordAccountCascadeFailed, errx.CodeRecordTranscodeFailed:
		return http.StatusInternalServerError
	case errx.CodeRecordAuditRejected:
		return http.StatusUnprocessableEntity

	// schedule / timetable 30401-30499 (独立段;D-03 中期 C 拆独立模块时
	// 保持 wire-format 稳定)
	case errx.CodeScheduleNotFound:
		return http.StatusNotFound
	case errx.CodeScheduleImportFailed:
		return http.StatusUnprocessableEntity
	case errx.CodeScheduleTimetableConflict:
		return http.StatusConflict

	// laoa 30501-30599 (老A enrollment / operation ledger; ADR-034)
	case errx.CodeLaoAEnrollmentStateChanged,
		errx.CodeLaoAEnrollmentDeleting,
		errx.CodeLaoAIdempotencyConflict,
		errx.CodeLaoAFingerprintMismatch,
		errx.CodeLaoAInvalidTransition,
		errx.CodeLaoAAssetConflict,
		errx.CodeLaoAEvidenceVersionConflict,
		errx.CodeLaoAEvidenceLifecycleConflict:
		return http.StatusConflict
	case errx.CodeLaoAEnrollmentRetired,
		errx.CodeLaoAEvidenceEpochRetired:
		return http.StatusGone
	case errx.CodeLaoAEnrollmentNotFound,
		errx.CodeLaoAEvidenceNotFound:
		return http.StatusNotFound

	// xiangwan administrator 30601-30699
	case errx.CodeXiangwanAdminUnavailable:
		return http.StatusServiceUnavailable
	case errx.CodeXiangwanAdminSessionInvalid,
		errx.CodeXiangwanAdminIdentityRejected:
		return http.StatusUnauthorized
	case errx.CodeXiangwanAdminScopeForbidden,
		errx.CodeXiangwanAdminCSRFRejected:
		return http.StatusForbidden
	case errx.CodeXiangwanAdminOperationConflict,
		errx.CodeXiangwanAdminVersionConflict,
		errx.CodeXiangwanAdminQuickTagUnavailable:
		return http.StatusConflict
	case errx.CodeXiangwanAdminPublicationInvalid:
		return http.StatusUnprocessableEntity
	case errx.CodeXiangwanAdminTargetNotFound:
		return http.StatusNotFound
	case errx.CodeXiangwanAdminLoginRateLimited:
		return http.StatusTooManyRequests

	// ai 40001-40099
	case errx.CodeAIQuotaExceeded:
		return http.StatusPaymentRequired
	case errx.CodeAIBusy:
		return http.StatusServiceUnavailable

	// billing 40101-40199
	case errx.CodeBillingQuotaExceeded, errx.CodeBillingCreditInsufficient:
		return http.StatusPaymentRequired
	case errx.CodeBillingEntitlementMissing:
		return http.StatusForbidden
	case errx.CodeBillingUndoExpired:
		return http.StatusGone
	case errx.CodeBillingLedgerConflict:
		return http.StatusConflict

	// syncpkg 40201-40299
	case errx.CodeSyncPackageUnavailable:
		return http.StatusNotFound
	case errx.CodeSyncPackageSnapshotInvalid:
		return http.StatusUnprocessableEntity
	case errx.CodeSyncPackageCodeUnavailable,
		errx.CodeSyncPackageCoverUnavailable:
		return http.StatusServiceUnavailable
	case errx.CodeSyncPackageWritesUnavailable:
		return http.StatusServiceUnavailable
	case errx.CodeSyncPackageSaveModeUnsupported,
		errx.CodeSyncPackageSaveOperationConflict,
		errx.CodeSyncPackageSaveOperationInactive:
		return http.StatusConflict

	default:
		return http.StatusInternalServerError
	}
}
