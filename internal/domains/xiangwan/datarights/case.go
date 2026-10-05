// Package datarights owns Xiangwan consumer personal-data rights request facts.
// Requests and their lifecycle evidence are durable PostgreSQL facts; they do
// not promise immediate physical deletion or use a cache as an authority.
package datarights

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const MaxMyCases = 100

var (
	ErrInvalidSubmission = errors.New("invalid xiangwan data-rights submission")
	ErrInvalidCase       = errors.New("invalid xiangwan data-rights case")
	ErrInvalidCaseEvent  = errors.New("invalid xiangwan data-rights case event")
)

type RequestType string

const (
	RequestTypeAccess     RequestType = "access"
	RequestTypeCorrection RequestType = "correction"
	RequestTypeExport     RequestType = "export"
	RequestTypeDeletion   RequestType = "deletion"
)

type RequestScope string

const (
	RequestScopeAll                   RequestScope = "all_xiangwan_data"
	RequestScopeProfile               RequestScope = "profile"
	RequestScopeActivityParticipation RequestScope = "activity_participation"
	RequestScopePaymentsAndRefunds    RequestScope = "payments_and_refunds"
	RequestScopePublishedContent      RequestScope = "published_content"
)

type CaseStatus string

const (
	CaseStatusSubmitted            CaseStatus = "submitted"
	CaseStatusIdentityVerification CaseStatus = "identity_verification"
	CaseStatusInReview             CaseStatus = "in_review"
	CaseStatusApproved             CaseStatus = "approved"
	CaseStatusPartiallyApproved    CaseStatus = "partially_approved"
	CaseStatusRejected             CaseStatus = "rejected"
	CaseStatusFulfilled            CaseStatus = "fulfilled"
)

type EventType string

const (
	EventTypeSubmitted                     EventType = "submitted"
	EventTypeIdentityVerificationRequested EventType = "identity_verification_requested"
	EventTypeReviewStarted                 EventType = "review_started"
	EventTypeApproved                      EventType = "approved"
	EventTypePartiallyApproved             EventType = "partially_approved"
	EventTypeRejected                      EventType = "rejected"
	EventTypeDeliveryPrepared              EventType = "delivery_prepared"
	EventTypeDeliverySucceeded             EventType = "delivery_succeeded"
	EventTypeDeliveryFailed                EventType = "delivery_failed"
	EventTypeFulfilled                     EventType = "fulfilled"
)

type DeliveryKind string

const (
	DeliveryKindAccessCopy          DeliveryKind = "access_copy"
	DeliveryKindExportArchive       DeliveryKind = "export_archive"
	DeliveryKindCorrectionNotice    DeliveryKind = "correction_notice"
	DeliveryKindDeletionDisposition DeliveryKind = "deletion_disposition"
)

type DeliveryStatus string

const (
	DeliveryStatusPrepared  DeliveryStatus = "prepared"
	DeliveryStatusDelivered DeliveryStatus = "delivered"
	DeliveryStatusFailed    DeliveryStatus = "failed"
)

type Submission struct {
	TenantID             uuid.UUID
	PrincipalID          uuid.UUID
	OperationKey         uuid.UUID
	RequestType          RequestType
	RequestScope         RequestScope
	PrivacyPolicyVersion string
}

type Case struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	PrincipalID          uuid.UUID
	OperationKey         uuid.UUID
	RequestFingerprint   [sha256.Size]byte
	RequestType          RequestType
	RequestScope         RequestScope
	PrivacyPolicyVersion string
	Status               CaseStatus
	Version              int64
	SubmittedAt          time.Time
	UpdatedAt            time.Time
	CompletedAt          *time.Time
}

type CaseEvent struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	CaseID             uuid.UUID
	CaseVersion        int64
	EventType          EventType
	ResultingStatus    CaseStatus
	ActorPrincipalID   *uuid.UUID
	PolicyBasisVersion string
	DeliveryKind       DeliveryKind
	DeliveryStatus     DeliveryStatus
	EvidenceDigest     []byte
	OccurredAt         time.Time
}

type CaseHistory struct {
	Case   Case
	Events []CaseEvent
}

func (Case) String() string {
	return "xiangwan DataRightsCase{owner:[REDACTED],evidence:[REDACTED]}"
}

func (value Case) GoString() string {
	return value.String()
}

func (Submission) String() string {
	return "xiangwan DataRightsSubmission{owner:[REDACTED],operation:[REDACTED]}"
}

func (value Submission) GoString() string {
	return value.String()
}

func (CaseEvent) String() string {
	return "xiangwan DataRightsCaseEvent{actor:[REDACTED],evidence:[REDACTED]}"
}

func (value CaseEvent) GoString() string {
	return value.String()
}

func ValidRequestType(value RequestType) bool {
	switch value {
	case RequestTypeAccess, RequestTypeCorrection, RequestTypeExport,
		RequestTypeDeletion:
		return true
	default:
		return false
	}
}

func ValidRequestScope(value RequestScope) bool {
	switch value {
	case RequestScopeAll, RequestScopeProfile,
		RequestScopeActivityParticipation, RequestScopePaymentsAndRefunds,
		RequestScopePublishedContent:
		return true
	default:
		return false
	}
}

func ValidCaseStatus(value CaseStatus) bool {
	switch value {
	case CaseStatusSubmitted, CaseStatusIdentityVerification,
		CaseStatusInReview, CaseStatusApproved, CaseStatusPartiallyApproved,
		CaseStatusRejected, CaseStatusFulfilled:
		return true
	default:
		return false
	}
}

func ValidPolicyVersion(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			(index > 0 && strings.ContainsRune("._:-", character)) {
			continue
		}
		return false
	}
	return true
}

func ValidateSubmission(value Submission) error {
	if value.TenantID == uuid.Nil || value.PrincipalID == uuid.Nil ||
		value.OperationKey == uuid.Nil || value.OperationKey.Version() != 4 ||
		value.OperationKey.Variant() != uuid.RFC4122 ||
		!ValidRequestType(value.RequestType) ||
		!ValidRequestScope(value.RequestScope) ||
		!ValidPolicyVersion(value.PrivacyPolicyVersion) {
		return ErrInvalidSubmission
	}
	return nil
}

func ValidateCase(value Case) error {
	if value.ID == uuid.Nil || value.TenantID == uuid.Nil ||
		value.PrincipalID == uuid.Nil || value.OperationKey == uuid.Nil ||
		value.OperationKey.Version() != 4 ||
		value.OperationKey.Variant() != uuid.RFC4122 ||
		!ValidRequestType(value.RequestType) ||
		!ValidRequestScope(value.RequestScope) ||
		!ValidPolicyVersion(value.PrivacyPolicyVersion) ||
		!ValidCaseStatus(value.Status) || value.Version < 1 ||
		value.SubmittedAt.IsZero() || value.UpdatedAt.Before(value.SubmittedAt) ||
		(value.CompletedAt != nil && value.CompletedAt.Before(value.SubmittedAt)) ||
		((value.Status == CaseStatusRejected || value.Status == CaseStatusFulfilled) !=
			(value.CompletedAt != nil)) || allZero(value.RequestFingerprint[:]) {
		return ErrInvalidCase
	}
	return nil
}

func ValidateCaseEvent(value CaseEvent) error {
	if value.ID == uuid.Nil || value.TenantID == uuid.Nil ||
		value.CaseID == uuid.Nil || value.CaseVersion < 1 ||
		!validEventType(value.EventType) ||
		!ValidCaseStatus(value.ResultingStatus) || value.OccurredAt.IsZero() ||
		(value.ActorPrincipalID != nil && *value.ActorPrincipalID == uuid.Nil) ||
		(value.PolicyBasisVersion != "" &&
			!ValidPolicyVersion(value.PolicyBasisVersion)) ||
		(len(value.EvidenceDigest) != 0 &&
			len(value.EvidenceDigest) != sha256.Size) {
		return ErrInvalidCaseEvent
	}
	wantDeliveryStatus, deliveryEvent := deliveryStatusForEvent(value.EventType)
	if deliveryEvent != (value.DeliveryKind != "" || value.DeliveryStatus != "") {
		return ErrInvalidCaseEvent
	}
	if deliveryEvent && (!validDeliveryKind(value.DeliveryKind) ||
		value.DeliveryStatus != wantDeliveryStatus) {
		return ErrInvalidCaseEvent
	}
	if !validResultingStatus(value.EventType, value.ResultingStatus) {
		return ErrInvalidCaseEvent
	}
	return nil
}

func SubmissionFingerprint(value Submission) ([sha256.Size]byte, error) {
	if err := ValidateSubmission(value); err != nil {
		return [sha256.Size]byte{}, err
	}
	payload, err := json.Marshal(struct {
		TenantID             string       `json:"tenant_id"`
		PrincipalID          string       `json:"principal_id"`
		RequestType          RequestType  `json:"request_type"`
		RequestScope         RequestScope `json:"request_scope"`
		PrivacyPolicyVersion string       `json:"privacy_policy_version"`
	}{
		TenantID:             value.TenantID.String(),
		PrincipalID:          value.PrincipalID.String(),
		RequestType:          value.RequestType,
		RequestScope:         value.RequestScope,
		PrivacyPolicyVersion: value.PrivacyPolicyVersion,
	})
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("fingerprint data-rights submission: %w", err)
	}
	return sha256.Sum256(payload), nil
}

func validEventType(value EventType) bool {
	switch value {
	case EventTypeSubmitted, EventTypeIdentityVerificationRequested,
		EventTypeReviewStarted, EventTypeApproved, EventTypePartiallyApproved,
		EventTypeRejected, EventTypeDeliveryPrepared,
		EventTypeDeliverySucceeded, EventTypeDeliveryFailed,
		EventTypeFulfilled:
		return true
	default:
		return false
	}
}

func validDeliveryKind(value DeliveryKind) bool {
	switch value {
	case DeliveryKindAccessCopy, DeliveryKindExportArchive,
		DeliveryKindCorrectionNotice, DeliveryKindDeletionDisposition:
		return true
	default:
		return false
	}
}

func deliveryStatusForEvent(value EventType) (DeliveryStatus, bool) {
	switch value {
	case EventTypeDeliveryPrepared:
		return DeliveryStatusPrepared, true
	case EventTypeDeliverySucceeded:
		return DeliveryStatusDelivered, true
	case EventTypeDeliveryFailed:
		return DeliveryStatusFailed, true
	default:
		return "", false
	}
}

func validResultingStatus(eventType EventType, status CaseStatus) bool {
	switch eventType {
	case EventTypeSubmitted:
		return status == CaseStatusSubmitted
	case EventTypeIdentityVerificationRequested:
		return status == CaseStatusIdentityVerification
	case EventTypeReviewStarted:
		return status == CaseStatusInReview
	case EventTypeApproved:
		return status == CaseStatusApproved
	case EventTypePartiallyApproved:
		return status == CaseStatusPartiallyApproved
	case EventTypeRejected:
		return status == CaseStatusRejected
	case EventTypeDeliveryPrepared, EventTypeDeliverySucceeded,
		EventTypeDeliveryFailed:
		return status == CaseStatusApproved ||
			status == CaseStatusPartiallyApproved
	case EventTypeFulfilled:
		return status == CaseStatusFulfilled
	default:
		return false
	}
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}
