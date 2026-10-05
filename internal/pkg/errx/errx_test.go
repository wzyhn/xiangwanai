package errx

import (
	"errors"
	"fmt"
	"testing"
)

func TestNew(t *testing.T) {
	err := New(CodeBadRequest, "test error")
	if err.Code != CodeBadRequest {
		t.Errorf("expected code %d, got %d", CodeBadRequest, err.Code)
	}
	if err.Message != "test error" {
		t.Errorf("expected message 'test error', got '%s'", err.Message)
	}
	expected := "[10001] test error"
	if err.Error() != expected {
		t.Errorf("unexpected error string: %s, want %s", err.Error(), expected)
	}
}

func TestHelpers(t *testing.T) {
	tests := []struct {
		fn   func(string) *Error
		code Code
	}{
		{NewBadRequest, CodeBadRequest},
		{NewUnauthorized, CodeUnauthorized},
		{NewForbidden, CodeForbidden},
		{NewNotFound, CodeNotFound},
		{NewConflict, CodeConflict},
		{NewInternal, CodeInternal},
	}
	for _, tt := range tests {
		err := tt.fn("msg")
		if err.Code != tt.code {
			t.Errorf("expected code %d, got %d", tt.code, err.Code)
		}
	}
}

// TestErrorImplementsErrorsAs verifies that callers can recover *Error from
// a wrapped chain via errors.As — the contract relied on by
// internal/pkg/response.Err to surface module-specific codes when the
// service layer adds context with fmt.Errorf("%w").
func TestErrorImplementsErrorsAs(t *testing.T) {
	leaf := New(CodeCommunityPostNotFound, "post not found")
	wrapped := fmt.Errorf("service layer: %w", leaf)
	wrapped2 := fmt.Errorf("handler layer: %w", wrapped)

	var target *Error
	if !errors.As(wrapped2, &target) {
		t.Fatal("errors.As failed to recover *Error from doubly-wrapped chain")
	}
	if target.Code != CodeCommunityPostNotFound {
		t.Errorf("recovered code = %d, want %d", target.Code, CodeCommunityPostNotFound)
	}
	if target.Message != "post not found" {
		t.Errorf("recovered message = %q, want 'post not found'", target.Message)
	}
}

// segment is the canonical segment registry shared by NoOverlap +
// boundary-aware InRange tests below. New errx const blocks MUST add a
// segment row here so the lint tests below (a) catch overlap with siblings
// and (b) verify boundary numeric values are inside the declared range.
type segment struct {
	name  string
	first Code
	last  Code
}

var allSegments = []segment{
	{"generic", 10001, 10999},
	{"auth", 20001, 20099},
	{"tenancy", 20101, 20199},
	{"storage", 20301, 20399},
	{"community", 30001, 30099},
	{"study", 30101, 30199},
	{"record", 30201, 30299},
	{"schedule", 30401, 30499},
	{"laoa", 30501, 30599},
	{"ai", 40001, 40099},
	{"billing", 40101, 40199},
	{"syncpkg", 40201, 40299},
}

func TestSegmentRanges_NoOverlap(t *testing.T) {
	for i := 0; i < len(allSegments); i++ {
		a := allSegments[i]
		if a.first > a.last {
			t.Errorf("segment %s: first=%d > last=%d", a.name, a.first, a.last)
		}
		for j := i + 1; j < len(allSegments); j++ {
			b := allSegments[j]
			if a.first <= b.last && b.first <= a.last {
				t.Errorf("segments %s (%d-%d) and %s (%d-%d) overlap", a.name, a.first, a.last, b.name, b.first, b.last)
			}
		}
	}
}

// TestSegmentRanges_BoundariesUnique guards against a new const accidentally
// landing exactly on the first/last boundary of an adjacent segment by
// asserting the set of declared boundary integers contains no duplicates
// (catches typo like declaring storage.last=30001 which would silently
// alias community.first).
func TestSegmentRanges_BoundariesUnique(t *testing.T) {
	seen := map[Code]string{}
	for _, s := range allSegments {
		for _, b := range []Code{s.first, s.last} {
			if existing, ok := seen[b]; ok && existing != s.name {
				t.Errorf("boundary %d declared by both %q and %q", b, existing, s.name)
			}
			seen[b] = s.name
		}
	}
}

func TestGenericSegmentInRange(t *testing.T) {
	codes := []Code{
		CodeBadRequest,
		CodeUnauthorized,
		CodeForbidden,
		CodeNotFound,
		CodeConflict,
		CodeInternal,
	}
	for _, code := range codes {
		if code < 10001 || code > 10999 {
			t.Errorf("generic code %d out of 10001-10999 range", code)
		}
	}
	// CodeOK is the success sentinel and intentionally outside any segment.
	if CodeOK != 0 {
		t.Errorf("CodeOK = %d, want 0 (success sentinel)", CodeOK)
	}
}

func TestAuthSegmentInRange(t *testing.T) {
	codes := []Code{
		CodeWechatAuthFail,
	}
	for _, code := range codes {
		if code < 20001 || code > 20099 {
			t.Errorf("auth code %d out of 20001-20099 range", code)
		}
	}
}

func TestStorageSegmentInRange(t *testing.T) {
	codes := []Code{
		CodeFileTooLarge,
		CodeFileTypeNotSupported,
	}
	for _, code := range codes {
		if code < 20301 || code > 20399 {
			t.Errorf("storage code %d out of 20301-20399 range", code)
		}
	}
}

func TestAISegmentInRange(t *testing.T) {
	codes := []Code{
		CodeAIQuotaExceeded,
		CodeAIBusy,
	}
	for _, code := range codes {
		if code < 40001 || code > 40099 {
			t.Errorf("ai code %d out of 40001-40099 range", code)
		}
	}
}

// TestAllConstsBelongToDeclaredSegment is a higher-order guard: every Code
// const value declared in errx.go must fall inside exactly one declared
// segment in allSegments (or be the CodeOK=0 sentinel). New const blocks
// added without a corresponding allSegments row will fail here.
func TestAllConstsBelongToDeclaredSegment(t *testing.T) {
	allCodes := []struct {
		name  string
		value Code
	}{
		{"CodeOK", CodeOK},
		{"CodeBadRequest", CodeBadRequest},
		{"CodeUnauthorized", CodeUnauthorized},
		{"CodeForbidden", CodeForbidden},
		{"CodeNotFound", CodeNotFound},
		{"CodeConflict", CodeConflict},
		{"CodeInternal", CodeInternal},
		{"CodeWechatAuthFail", CodeWechatAuthFail},
		{"CodePrincipalInactive", CodePrincipalInactive},
		{"CodePersonalScopeNotFound", CodePersonalScopeNotFound},
		{"CodePersonalScopeMismatch", CodePersonalScopeMismatch},
		{"CodeFileTooLarge", CodeFileTooLarge},
		{"CodeFileTypeNotSupported", CodeFileTypeNotSupported},
		{"CodeCommunityPostNotFound", CodeCommunityPostNotFound},
		{"CodeCommunityModerationRejected", CodeCommunityModerationRejected},
		{"CodeStudyNoteNotFound", CodeStudyNoteNotFound},
		{"CodeStudyParseUnsupportedType", CodeStudyParseUnsupportedType},
		{"CodeRecordMomentNotFound", CodeRecordMomentNotFound},
		{"CodeRecordTranscodeFailed", CodeRecordTranscodeFailed},
		{"CodeScheduleNotFound", CodeScheduleNotFound},
		{"CodeScheduleTimetableConflict", CodeScheduleTimetableConflict},
		{"CodeLaoAEnrollmentStateChanged", CodeLaoAEnrollmentStateChanged},
		{"CodeLaoAEnrollmentRetired", CodeLaoAEnrollmentRetired},
		{"CodeLaoAInvalidTransition", CodeLaoAInvalidTransition},
		{"CodeLaoAEvidenceEpochRetired", CodeLaoAEvidenceEpochRetired},
		{"CodeLaoAEvidenceNotFound", CodeLaoAEvidenceNotFound},
		{"CodeLaoAAssetConflict", CodeLaoAAssetConflict},
		{"CodeLaoAEvidenceVersionConflict", CodeLaoAEvidenceVersionConflict},
		{"CodeLaoAEvidenceLifecycleConflict", CodeLaoAEvidenceLifecycleConflict},
		{"CodeAIQuotaExceeded", CodeAIQuotaExceeded},
		{"CodeAIBusy", CodeAIBusy},
		{"CodeBillingQuotaExceeded", CodeBillingQuotaExceeded},
		{"CodeBillingLedgerConflict", CodeBillingLedgerConflict},
		{"CodeSyncPackageUnavailable", CodeSyncPackageUnavailable},
		{"CodeSyncPackageSnapshotInvalid", CodeSyncPackageSnapshotInvalid},
		{"CodeSyncPackageCodeUnavailable", CodeSyncPackageCodeUnavailable},
		{"CodeSyncPackageSaveModeUnsupported", CodeSyncPackageSaveModeUnsupported},
		{"CodeSyncPackageSaveOperationConflict", CodeSyncPackageSaveOperationConflict},
		{"CodeSyncPackageSaveOperationInactive", CodeSyncPackageSaveOperationInactive},
		{"CodeSyncPackageWritesUnavailable", CodeSyncPackageWritesUnavailable},
		{"CodeSyncPackageCoverUnavailable", CodeSyncPackageCoverUnavailable},
	}
	for _, c := range allCodes {
		if c.value == 0 {
			continue // CodeOK sentinel
		}
		matched := false
		for _, s := range allSegments {
			if c.value >= s.first && c.value <= s.last {
				matched = true
				break
			}
		}
		if !matched {
			t.Errorf("%s = %d does not belong to any declared segment in allSegments", c.name, c.value)
		}
	}
}

func TestCommunitySegmentInRange(t *testing.T) {
	codes := []Code{
		CodeCommunityPostNotFound,
		CodeCommunityPostNotEditable,
		CodeCommunityPostUnderReview,
		CodeCommunityCommentNotFound,
		CodeCommunityShareTargetExpired,
		CodeCommunityContactRateLimit,
		CodeCommunityModerationRejected,
	}
	for _, code := range codes {
		if code < 30001 || code > 30099 {
			t.Errorf("community code %d out of 30001-30099 range", code)
		}
	}
}

func TestStudySegmentInRange(t *testing.T) {
	codes := []Code{
		CodeStudyNoteNotFound,
		CodeStudyParseUnsupportedType,
	}
	for _, code := range codes {
		if code < 30101 || code > 30199 {
			t.Errorf("study code %d out of 30101-30199 range", code)
		}
	}
}

func TestRecordSegmentInRange(t *testing.T) {
	codes := []Code{
		CodeRecordMomentNotFound,
		CodeRecordAccountCascadeFailed,
		CodeRecordAuditRejected,
		CodeRecordTranscodeFailed,
	}
	for _, code := range codes {
		if code < 30201 || code > 30299 {
			t.Errorf("record code %d out of 30201-30299 range", code)
		}
	}
}

func TestScheduleSegmentInRange(t *testing.T) {
	codes := []Code{
		CodeScheduleNotFound,
		CodeScheduleImportFailed,
		CodeScheduleTimetableConflict,
	}
	for _, code := range codes {
		if code < 30401 || code > 30499 {
			t.Errorf("schedule code %d out of 30401-30499 range", code)
		}
	}
}

func TestBillingSegmentInRange(t *testing.T) {
	codes := []Code{
		CodeBillingQuotaExceeded,
		CodeBillingEntitlementMissing,
		CodeBillingCreditInsufficient,
		CodeBillingUndoExpired,
		CodeBillingLedgerConflict,
	}
	for _, code := range codes {
		if code < 40101 || code > 40199 {
			t.Errorf("billing code %d out of 40101-40199 range", code)
		}
	}
}

func TestSyncPackageSegmentInRange(t *testing.T) {
	codes := []Code{
		CodeSyncPackageUnavailable,
		CodeSyncPackageSnapshotInvalid,
		CodeSyncPackageCodeUnavailable,
		CodeSyncPackageSaveModeUnsupported,
		CodeSyncPackageSaveOperationConflict,
		CodeSyncPackageSaveOperationInactive,
		CodeSyncPackageWritesUnavailable,
		CodeSyncPackageCoverUnavailable,
	}
	for _, code := range codes {
		if code < 40201 || code > 40299 {
			t.Errorf("syncpkg code %d out of 40201-40299 range", code)
		}
	}
}

// TestAllNewSegmentCodesUnique guards against accidental duplicates inside
// the four new segments + against collision with the existing 11 generic /
// auth / storage / ai codes.
func TestAllNewSegmentCodesUnique(t *testing.T) {
	all := []Code{
		// existing
		CodeOK, CodeBadRequest, CodeUnauthorized, CodeForbidden, CodeNotFound,
		CodeConflict, CodeInternal,
		CodeWechatAuthFail,
		CodeFileTooLarge, CodeFileTypeNotSupported,
		CodeAIQuotaExceeded, CodeAIBusy,
		// new community
		CodeCommunityPostNotFound, CodeCommunityPostNotEditable,
		CodeCommunityPostUnderReview, CodeCommunityCommentNotFound,
		CodeCommunityShareTargetExpired, CodeCommunityContactRateLimit,
		CodeCommunityModerationRejected,
		// new study
		CodeStudyNoteNotFound, CodeStudyParseUnsupportedType,
		// new record
		CodeRecordMomentNotFound, CodeRecordAccountCascadeFailed,
		CodeRecordAuditRejected, CodeRecordTranscodeFailed,
		// new schedule (D-03 中期 C 独立段)
		CodeScheduleNotFound, CodeScheduleImportFailed,
		CodeScheduleTimetableConflict,
		// laoa
		CodeLaoAEnrollmentStateChanged, CodeLaoAEnrollmentDeleting,
		CodeLaoAEnrollmentRetired, CodeLaoAIdempotencyConflict,
		CodeLaoAFingerprintMismatch, CodeLaoAEnrollmentNotFound,
		CodeLaoAInvalidTransition, CodeLaoAEvidenceEpochRetired,
		CodeLaoAEvidenceNotFound, CodeLaoAAssetConflict,
		CodeLaoAEvidenceVersionConflict, CodeLaoAEvidenceLifecycleConflict,
		// new billing
		CodeBillingQuotaExceeded, CodeBillingEntitlementMissing,
		CodeBillingCreditInsufficient, CodeBillingUndoExpired,
		CodeBillingLedgerConflict,
		// new syncpkg
		CodeSyncPackageUnavailable, CodeSyncPackageSnapshotInvalid,
		CodeSyncPackageCodeUnavailable, CodeSyncPackageSaveModeUnsupported,
		CodeSyncPackageSaveOperationConflict, CodeSyncPackageSaveOperationInactive,
		CodeSyncPackageWritesUnavailable,
		CodeSyncPackageCoverUnavailable,
	}
	seen := make(map[Code]bool, len(all))
	for _, c := range all {
		if seen[c] {
			t.Errorf("duplicate code value %d", c)
		}
		seen[c] = true
	}
}
