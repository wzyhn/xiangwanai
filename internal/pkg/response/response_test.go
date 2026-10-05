package response

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/pkg/errx"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestOKDoesNotEscapeSignedURLQuery(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	OK(ctx, map[string]string{
		"url": "https://api.example.com/api/v1/files/file-1/content?exp=1&sig=test",
	})

	body := recorder.Body.String()
	if !strings.Contains(body, "&sig=test") {
		t.Fatalf("expected raw ampersand in response body, got %q", body)
	}
	if strings.Contains(body, "\\u0026") {
		t.Fatalf("expected response body to avoid unicode-escaped ampersand, got %q", body)
	}
}

// TestErr_DirectErrxError exercises the legacy happy path: an unwrapped
// *errx.Error gets its own code emitted (preserving pre-PR-#98 contract).
func TestErr_DirectErrxError(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	Err(ctx, errx.New(errx.CodeNotFound, "missing"))

	if recorder.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", recorder.Code)
	}
	var got Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if got.Code != int(errx.CodeNotFound) {
		t.Errorf("code: got %d, want %d", got.Code, errx.CodeNotFound)
	}
	if got.Message != "missing" {
		t.Errorf("message: got %q, want 'missing'", got.Message)
	}
}

// TestErr_WrappedErrxError is the round-2 obs P1-4 fix verification:
// fmt.Errorf("layer: %w", errx.NewBadRequest(...)) used to land in the
// type-assert fallback (HTTP 400 with the wrapped error string). After
// switching to errors.As, the inner code/message must surface unchanged.
func TestErr_WrappedErrxError(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	leaf := errx.New(errx.CodeCommunityPostUnderReview, "post is under moderation")
	wrapped := fmt.Errorf("service layer: %w", leaf)
	wrapped2 := fmt.Errorf("handler layer: %w", wrapped)

	Err(ctx, wrapped2)

	if recorder.Code != http.StatusForbidden {
		t.Errorf("wrapped chain: status got %d, want 403 (CodeCommunityPostUnderReview → 403)", recorder.Code)
	}
	var got Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Code != int(errx.CodeCommunityPostUnderReview) {
		t.Errorf("wrapped chain: recovered code %d, want %d", got.Code, errx.CodeCommunityPostUnderReview)
	}
	if got.Message != "post is under moderation" {
		t.Errorf("wrapped chain: recovered message %q, want 'post is under moderation'", got.Message)
	}
}

// 2026-07-11(B5 演练 B-1)反转:非 errx 错误曾回退 400+原始 err.Error()——
// ①infra 故障(DB 宕机)被计入 400,5xx 告警全盲;②错误串(含内网地址)直出客户端。
// 现行契约:fail-closed 500 + 泛化文案;真错误仅经 c.Error() 落 access log。
// 想要 400 的调用方必须自己包 errx.NewBadRequest。
func TestErr_NonErrxFallback(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	Err(ctx, errors.New("dial tcp 10.0.0.7:5432: connection refused"))

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500 (fail-closed fallback)", recorder.Code)
	}
	var got Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Code != int(errx.CodeInternal) {
		t.Errorf("code: got %d, want %d (Internal fallback)", got.Code, errx.CodeInternal)
	}
	if got.Message != "internal server error" {
		t.Errorf("message: got %q, want generic text", got.Message)
	}
	if strings.Contains(recorder.Body.String(), "10.0.0.7") {
		t.Error("原始错误串泄入客户端响应体(信息泄露回归)")
	}
	if len(ctx.Errors) != 1 || !strings.Contains(ctx.Errors[0].Error(), "10.0.0.7") {
		t.Errorf("真错误应经 c.Error() 挂上下文供 access log 记载: %v", ctx.Errors)
	}
}

// ErrWithData 同一回退语义:非 errx → 500 泛化,但 Data 保留(调用方显式要求
// 返回的结构化细节),真错误同样只挂 context。
func TestErrWithData_NonErrxFallback(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)

	ErrWithData(ctx, errors.New("secret-internal-detail"), map[string]any{"k": "v"})

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want 500", recorder.Code)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "secret-internal-detail") {
		t.Error("原始错误串泄入响应体")
	}
	if !strings.Contains(body, `"k":"v"`) {
		t.Errorf("Data 应保留: %s", body)
	}
	if len(ctx.Errors) != 1 {
		t.Errorf("真错误应挂 context: %v", ctx.Errors)
	}
}

// TestCodeToHTTP_AllSegments table-tests the HTTP status mapping for every
// code so a future code addition without a mapping fails loudly here rather
// than silently returning 500 in production.
func TestCodeToHTTP_AllSegments(t *testing.T) {
	tests := []struct {
		code   errx.Code
		status int
	}{
		// generic
		{errx.CodeBadRequest, http.StatusBadRequest},
		{errx.CodeUnauthorized, http.StatusUnauthorized},
		{errx.CodeForbidden, http.StatusForbidden},
		{errx.CodeNotFound, http.StatusNotFound},
		{errx.CodeConflict, http.StatusConflict},
		{errx.CodeInternal, http.StatusInternalServerError},
		// auth
		{errx.CodeWechatAuthFail, http.StatusUnauthorized},
		// storage
		{errx.CodeFileTooLarge, http.StatusRequestEntityTooLarge},
		{errx.CodeFileTypeNotSupported, http.StatusBadRequest},
		// community
		{errx.CodeCommunityPostNotFound, http.StatusNotFound},
		{errx.CodeCommunityCommentNotFound, http.StatusNotFound},
		{errx.CodeCommunityPostNotEditable, http.StatusConflict},
		{errx.CodeCommunityPostUnderReview, http.StatusForbidden},
		{errx.CodeCommunityShareTargetExpired, http.StatusGone},
		{errx.CodeCommunityContactRateLimit, http.StatusTooManyRequests},
		{errx.CodeCommunityModerationRejected, http.StatusUnprocessableEntity},
		// study (note / parse — schedule/timetable 移到 30401-30499)
		{errx.CodeStudyNoteNotFound, http.StatusNotFound},
		{errx.CodeStudyParseUnsupportedType, http.StatusUnsupportedMediaType},
		// record (audit_status=pending 不进 errx — 走 success payload state field)
		{errx.CodeRecordMomentNotFound, http.StatusNotFound},
		{errx.CodeRecordAccountCascadeFailed, http.StatusInternalServerError},
		{errx.CodeRecordTranscodeFailed, http.StatusInternalServerError},
		{errx.CodeRecordAuditRejected, http.StatusUnprocessableEntity},
		// schedule (D-03 中期 C 独立段;30401-30499)
		{errx.CodeScheduleNotFound, http.StatusNotFound},
		{errx.CodeScheduleImportFailed, http.StatusUnprocessableEntity},
		{errx.CodeScheduleTimetableConflict, http.StatusConflict},
		// laoa V1a evidence
		{errx.CodeLaoAEvidenceEpochRetired, http.StatusGone},
		{errx.CodeLaoAEvidenceNotFound, http.StatusNotFound},
		{errx.CodeLaoAAssetConflict, http.StatusConflict},
		{errx.CodeLaoAEvidenceVersionConflict, http.StatusConflict},
		{errx.CodeLaoAEvidenceLifecycleConflict, http.StatusConflict},
		// ai
		{errx.CodeAIQuotaExceeded, http.StatusPaymentRequired},
		{errx.CodeAIBusy, http.StatusServiceUnavailable},
		// billing
		{errx.CodeBillingQuotaExceeded, http.StatusPaymentRequired},
		{errx.CodeBillingCreditInsufficient, http.StatusPaymentRequired},
		{errx.CodeBillingEntitlementMissing, http.StatusForbidden},
		{errx.CodeBillingUndoExpired, http.StatusGone},
		{errx.CodeBillingLedgerConflict, http.StatusConflict},
		// syncpkg
		{errx.CodeSyncPackageUnavailable, http.StatusNotFound},
		{errx.CodeSyncPackageSnapshotInvalid, http.StatusUnprocessableEntity},
		{errx.CodeSyncPackageCodeUnavailable, http.StatusServiceUnavailable},
		{errx.CodeSyncPackageSaveModeUnsupported, http.StatusConflict},
		{errx.CodeSyncPackageSaveOperationConflict, http.StatusConflict},
		{errx.CodeSyncPackageSaveOperationInactive, http.StatusConflict},
		{errx.CodeSyncPackageWritesUnavailable, http.StatusServiceUnavailable},
		{errx.CodeSyncPackageCoverUnavailable, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		got := codeToHTTP(tt.code)
		if got != tt.status {
			t.Errorf("codeToHTTP(%d) = %d, want %d", tt.code, got, tt.status)
		}
	}
}

// TestCodeToHTTP_UnknownFallsBackTo500 ensures unmapped codes fail loud.
func TestCodeToHTTP_UnknownFallsBackTo500(t *testing.T) {
	if got := codeToHTTP(errx.Code(99999)); got != http.StatusInternalServerError {
		t.Errorf("unknown code: got %d, want 500", got)
	}
}
