package datarights

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSubmissionFingerprintAndCaseValidation(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	submission := Submission{
		TenantID:             uuid.New(),
		PrincipalID:          uuid.New(),
		OperationKey:         uuid.New(),
		RequestType:          RequestTypeExport,
		RequestScope:         RequestScopeAll,
		PrivacyPolicyVersion: "privacy-v3",
	}
	fingerprint, err := SubmissionFingerprint(submission)
	if err != nil {
		t.Fatalf("SubmissionFingerprint() error = %v", err)
	}
	again := submission
	again.OperationKey = uuid.New()
	againFingerprint, err := SubmissionFingerprint(again)
	if err != nil || fingerprint != againFingerprint {
		t.Fatalf("request identity changed fingerprint: %x / %x, %v", fingerprint, againFingerprint, err)
	}
	value := Case{
		ID:                   uuid.New(),
		TenantID:             submission.TenantID,
		PrincipalID:          submission.PrincipalID,
		OperationKey:         submission.OperationKey,
		RequestFingerprint:   fingerprint,
		RequestType:          submission.RequestType,
		RequestScope:         submission.RequestScope,
		PrivacyPolicyVersion: submission.PrivacyPolicyVersion,
		Status:               CaseStatusSubmitted,
		Version:              1,
		SubmittedAt:          now,
		UpdatedAt:            now,
	}
	if err := ValidateCase(value); err != nil {
		t.Fatalf("ValidateCase() error = %v", err)
	}
	formatted := fmt.Sprintf("%+v", value)
	if strings.Contains(formatted, value.PrincipalID.String()) ||
		strings.Contains(formatted, fmt.Sprintf("%x", value.RequestFingerprint)) {
		t.Fatalf("Case formatting leaked protected evidence: %s", formatted)
	}
	if formatted := fmt.Sprintf("%+v %+v", submission, CaseEvent{
		ActorPrincipalID: &submission.PrincipalID,
		EvidenceDigest:   fingerprint[:],
	}); strings.Contains(formatted, submission.PrincipalID.String()) ||
		strings.Contains(formatted, fmt.Sprintf("%x", fingerprint)) {
		t.Fatalf("submission/event formatting leaked protected facts: %s", formatted)
	}

	completed := now.Add(time.Hour)
	value.CompletedAt = &completed
	if !errors.Is(ValidateCase(value), ErrInvalidCase) {
		t.Fatal("non-terminal case accepted completed_at")
	}
}

func TestSubmissionAndEventValidationRejectsInvalidEnumsAndEvidence(t *testing.T) {
	t.Parallel()

	submission := Submission{
		TenantID:             uuid.New(),
		PrincipalID:          uuid.New(),
		OperationKey:         uuid.New(),
		RequestType:          RequestTypeAccess,
		RequestScope:         RequestScopeProfile,
		PrivacyPolicyVersion: "privacy-v3",
	}
	invalid := submission
	invalid.RequestType = "erase-now"
	if !errors.Is(ValidateSubmission(invalid), ErrInvalidSubmission) {
		t.Fatal("unknown data-rights request type accepted")
	}
	event := CaseEvent{
		ID:              uuid.New(),
		TenantID:        submission.TenantID,
		CaseID:          uuid.New(),
		CaseVersion:     2,
		EventType:       EventTypeDeliverySucceeded,
		ResultingStatus: CaseStatusApproved,
		DeliveryKind:    DeliveryKindExportArchive,
		DeliveryStatus:  DeliveryStatusDelivered,
		OccurredAt:      time.Now().UTC(),
	}
	if err := ValidateCaseEvent(event); err != nil {
		t.Fatalf("ValidateCaseEvent() error = %v", err)
	}
	event.DeliveryStatus = DeliveryStatusPrepared
	if !errors.Is(ValidateCaseEvent(event), ErrInvalidCaseEvent) {
		t.Fatal("mismatched delivery event status accepted")
	}
	event.DeliveryStatus = DeliveryStatusDelivered
	event.ResultingStatus = CaseStatusSubmitted
	if !errors.Is(ValidateCaseEvent(event), ErrInvalidCaseEvent) {
		t.Fatal("mismatched event resulting status accepted")
	}
}
