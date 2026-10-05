package activity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type InstanceCancellationSessionImpact struct {
	SessionID                   uuid.UUID     `json:"session_id"`
	SessionStatus               SessionStatus `json:"session_status"`
	ExpectedSessionVersion      int64         `json:"expected_session_version"`
	SnapshotDigest              string        `json:"snapshot_digest"`
	CancelledRegistrationCount  int           `json:"cancelled_registration_count"`
	ConfirmedRegistrationCount  int           `json:"confirmed_registration_count"`
	ActiveHoldCount             int           `json:"active_hold_count"`
	FreeRegistrationCount       int           `json:"free_registration_count"`
	PaidRefundRegistrationCount int           `json:"paid_refund_registration_count"`
	PendingOrderCount           int           `json:"pending_order_count"`
	UnknownPaymentCount         int           `json:"unknown_payment_count"`
	RefundCaseCount             int           `json:"refund_case_count"`
	RequestedRefundCents        int64         `json:"requested_refund_cents"`
	CouponAdjustmentCount       int           `json:"coupon_adjustment_count"`
}

type InstanceCancellationPreview struct {
	ID                           uuid.UUID
	TenantID                     uuid.UUID
	SeriesID                     uuid.UUID
	InstanceID                   uuid.UUID
	RequestedBy                  uuid.UUID
	IdempotencyKey               string
	SnapshotDigest               string
	ExpectedInstanceVersion      int64
	SessionCount                 int
	TargetSessionCount           int
	AlreadyCancelledSessionCount int
	CancelledRegistrationCount   int
	ConfirmedRegistrationCount   int
	ActiveHoldCount              int
	FreeRegistrationCount        int
	PaidRefundRegistrationCount  int
	PendingOrderCount            int
	UnknownPaymentCount          int
	RefundCaseCount              int
	RequestedRefundCents         int64
	CouponAdjustmentCount        int
	CancellationReason           string
	NotificationStrategy         CancellationNotificationStrategy
	SessionImpacts               []InstanceCancellationSessionImpact
	ExpiresAt                    time.Time
	ConsumedAt                   *time.Time
	CreatedAt                    time.Time
}

type InstanceCancellationPreviewCommand struct {
	RequestedBy    uuid.UUID
	IdempotencyKey string
	Reason         string
	At             time.Time
}

type InstanceCancellationSnapshot struct {
	TenantID          uuid.UUID
	SeriesID          uuid.UUID
	InstanceID        uuid.UUID
	InstanceStatus    InstanceStatus
	InstanceVersion   int64
	InstanceUpdatedAt time.Time
	Sessions          []InstanceCancellationSessionSnapshot
}

type InstanceCancellationSessionSnapshot struct {
	SessionID        uuid.UUID
	SessionStatus    SessionStatus
	SessionVersion   int64
	SessionUpdatedAt time.Time
	Cancellation     *SessionCancellationSnapshot
}

type InstanceCancellationAssessment struct {
	SessionCount                 int
	TargetSessionCount           int
	AlreadyCancelledSessionCount int
	CancelledRegistrationCount   int
	ConfirmedRegistrationCount   int
	ActiveHoldCount              int
	FreeRegistrationCount        int
	PaidRefundRegistrationCount  int
	PendingOrderCount            int
	UnknownPaymentCount          int
	RefundCaseCount              int
	RequestedRefundCents         int64
	CouponAdjustmentCount        int
	SessionImpacts               []InstanceCancellationSessionImpact
}

type InstanceCancellationReceipt struct {
	ID                           uuid.UUID
	TenantID                     uuid.UUID
	SeriesID                     uuid.UUID
	InstanceID                   uuid.UUID
	PreviewID                    uuid.UUID
	IdempotencyKey               string
	CancelledBy                  uuid.UUID
	CancellationReason           string
	NotificationStrategy         CancellationNotificationStrategy
	SessionCount                 int
	NewlyCancelledSessionCount   int
	AlreadyCancelledSessionCount int
	CancelledRegistrationCount   int
	ReleasedConfirmedCount       int
	ReleasedHoldCount            int
	ClosedPendingOrderCount      int
	RefundCaseCount              int
	RequestedRefundCents         int64
	CouponAdjustmentCount        int
	CancelledAt                  time.Time
	ResultingInstanceVersion     int64
	CreatedAt                    time.Time
}

type CancelInstanceCommand struct {
	PreviewID            uuid.UUID
	CancelledBy          uuid.UUID
	IdempotencyKey       string
	Reason               string
	NotificationStrategy CancellationNotificationStrategy
	At                   time.Time
	RecordedAt           time.Time
}

var (
	ErrInvalidInstanceCancellation = errors.New(
		"invalid xiangwan Instance cancellation",
	)
	ErrInstanceNotCancellable = errors.New("xiangwan Instance is not cancellable")
)

func NewInstanceCancellationPreview(
	command InstanceCancellationPreviewCommand,
	snapshot InstanceCancellationSnapshot,
) (InstanceCancellationPreview, error) {
	reason := strings.TrimSpace(command.Reason)
	if command.RequestedBy == uuid.Nil ||
		!sessionCancellationKeyPattern.MatchString(command.IdempotencyKey) ||
		reason == "" ||
		reason != command.Reason ||
		len([]rune(reason)) > 500 ||
		command.At.IsZero() {
		return InstanceCancellationPreview{}, ErrInvalidInstanceCancellation
	}
	assessment, digest, err := AssessInstanceCancellation(snapshot)
	if err != nil {
		return InstanceCancellationPreview{}, err
	}
	createdAt := command.At.UTC()
	return InstanceCancellationPreview{
		ID:                           uuid.New(),
		TenantID:                     snapshot.TenantID,
		SeriesID:                     snapshot.SeriesID,
		InstanceID:                   snapshot.InstanceID,
		RequestedBy:                  command.RequestedBy,
		IdempotencyKey:               command.IdempotencyKey,
		SnapshotDigest:               digest,
		ExpectedInstanceVersion:      snapshot.InstanceVersion,
		SessionCount:                 assessment.SessionCount,
		TargetSessionCount:           assessment.TargetSessionCount,
		AlreadyCancelledSessionCount: assessment.AlreadyCancelledSessionCount,
		CancelledRegistrationCount:   assessment.CancelledRegistrationCount,
		ConfirmedRegistrationCount:   assessment.ConfirmedRegistrationCount,
		ActiveHoldCount:              assessment.ActiveHoldCount,
		FreeRegistrationCount:        assessment.FreeRegistrationCount,
		PaidRefundRegistrationCount:  assessment.PaidRefundRegistrationCount,
		PendingOrderCount:            assessment.PendingOrderCount,
		UnknownPaymentCount:          assessment.UnknownPaymentCount,
		RefundCaseCount:              assessment.RefundCaseCount,
		RequestedRefundCents:         assessment.RequestedRefundCents,
		CouponAdjustmentCount:        assessment.CouponAdjustmentCount,
		CancellationReason:           command.Reason,
		NotificationStrategy:         CancellationNotificationManualRequired,
		SessionImpacts:               assessment.SessionImpacts,
		ExpiresAt:                    createdAt.Add(SessionCancellationPreviewLifetime),
		CreatedAt:                    createdAt,
	}, nil
}

func AssessInstanceCancellation(
	snapshot InstanceCancellationSnapshot,
) (InstanceCancellationAssessment, string, error) {
	if snapshot.TenantID == uuid.Nil ||
		snapshot.SeriesID == uuid.Nil ||
		snapshot.InstanceID == uuid.Nil ||
		snapshot.InstanceStatus != InstanceStatusPublished ||
		snapshot.InstanceVersion < 1 ||
		snapshot.InstanceVersion == math.MaxInt64 ||
		snapshot.InstanceUpdatedAt.IsZero() ||
		len(snapshot.Sessions) == 0 {
		return InstanceCancellationAssessment{}, "", ErrInvalidInstanceCancellation
	}
	sessions := append(
		[]InstanceCancellationSessionSnapshot(nil),
		snapshot.Sessions...,
	)
	sort.Slice(sessions, func(left, right int) bool {
		return sessions[left].SessionID.String() <
			sessions[right].SessionID.String()
	})
	assessment := InstanceCancellationAssessment{
		SessionCount:   len(sessions),
		SessionImpacts: make([]InstanceCancellationSessionImpact, 0, len(sessions)),
	}
	canonicalSessions := make([]canonicalInstanceCancellationSession, 0, len(sessions))
	seen := make(map[uuid.UUID]struct{}, len(sessions))
	for _, current := range sessions {
		if current.SessionID == uuid.Nil ||
			current.SessionVersion < 1 ||
			current.SessionUpdatedAt.IsZero() {
			return InstanceCancellationAssessment{}, "", ErrInvalidInstanceCancellation
		}
		if _, exists := seen[current.SessionID]; exists {
			return InstanceCancellationAssessment{}, "", ErrInvalidInstanceCancellation
		}
		seen[current.SessionID] = struct{}{}
		impact := InstanceCancellationSessionImpact{
			SessionID:              current.SessionID,
			SessionStatus:          current.SessionStatus,
			ExpectedSessionVersion: current.SessionVersion,
		}
		canonical := canonicalInstanceCancellationSession{
			SessionID:        current.SessionID.String(),
			SessionStatus:    current.SessionStatus,
			SessionVersion:   current.SessionVersion,
			SessionUpdatedAt: canonicalPublicationTime(current.SessionUpdatedAt),
		}
		switch current.SessionStatus {
		case SessionStatusCancelled:
			if current.Cancellation != nil {
				return InstanceCancellationAssessment{}, "",
					ErrInvalidInstanceCancellation
			}
			assessment.AlreadyCancelledSessionCount++
			cancelledDigest, err := digestCanonicalInstanceCancellationSession(
				canonical,
			)
			if err != nil {
				return InstanceCancellationAssessment{}, "", err
			}
			impact.SnapshotDigest = cancelledDigest
		case SessionStatusPublished:
			if current.Cancellation == nil ||
				current.Cancellation.TenantID != snapshot.TenantID ||
				current.Cancellation.SeriesID != snapshot.SeriesID ||
				current.Cancellation.InstanceID != snapshot.InstanceID ||
				current.Cancellation.SessionID != current.SessionID ||
				current.Cancellation.SessionStatus != current.SessionStatus ||
				current.Cancellation.SessionVersion != current.SessionVersion ||
				!current.Cancellation.SessionUpdatedAt.Equal(
					current.SessionUpdatedAt,
				) ||
				current.Cancellation.RefundReasonCode != "instance_cancelled" {
				return InstanceCancellationAssessment{}, "",
					ErrInvalidInstanceCancellation
			}
			sessionAssessment, sessionDigest, err := AssessSessionCancellation(
				*current.Cancellation,
			)
			if err != nil {
				return InstanceCancellationAssessment{}, "", err
			}
			assessment.TargetSessionCount++
			impact.SnapshotDigest = sessionDigest
			copySessionCancellationAssessment(&impact, sessionAssessment)
			if err := addInstanceCancellationImpact(&assessment, impact); err != nil {
				return InstanceCancellationAssessment{}, "", err
			}
			canonical.CancellationDigest = sessionDigest
		default:
			return InstanceCancellationAssessment{}, "",
				ErrInstanceNotCancellable
		}
		assessment.SessionImpacts = append(assessment.SessionImpacts, impact)
		canonicalSessions = append(canonicalSessions, canonical)
	}
	if assessment.TargetSessionCount == 0 {
		return InstanceCancellationAssessment{}, "", ErrInstanceNotCancellable
	}

	canonical := canonicalInstanceCancellationSnapshot{
		TenantID:          snapshot.TenantID.String(),
		SeriesID:          snapshot.SeriesID.String(),
		InstanceID:        snapshot.InstanceID.String(),
		InstanceStatus:    snapshot.InstanceStatus,
		InstanceVersion:   snapshot.InstanceVersion,
		InstanceUpdatedAt: canonicalPublicationTime(snapshot.InstanceUpdatedAt),
		Sessions:          canonicalSessions,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return InstanceCancellationAssessment{}, "", fmt.Errorf(
			"encode canonical Instance cancellation snapshot: %w",
			err,
		)
	}
	sum := sha256.Sum256(encoded)
	return assessment, hex.EncodeToString(sum[:]), nil
}

func InstanceCancellationPreviewMatchesSnapshot(
	preview InstanceCancellationPreview,
	snapshot InstanceCancellationSnapshot,
) (bool, error) {
	assessment, digest, err := AssessInstanceCancellation(snapshot)
	if err != nil {
		return false, err
	}
	return preview.TenantID == snapshot.TenantID &&
			preview.SeriesID == snapshot.SeriesID &&
			preview.InstanceID == snapshot.InstanceID &&
			preview.ExpectedInstanceVersion == snapshot.InstanceVersion &&
			preview.SnapshotDigest == digest &&
			instanceCancellationPreviewMatchesAssessment(preview, assessment),
		nil
}

func CancelInstance(
	current Instance,
	command CancelInstanceCommand,
	assessment InstanceCancellationAssessment,
) (Instance, InstanceCancellationReceipt, error) {
	if err := validateInstanceCancellation(current, command, assessment); err != nil {
		return Instance{}, InstanceCancellationReceipt{}, err
	}
	if current.Status != InstanceStatusPublished {
		return Instance{}, InstanceCancellationReceipt{}, ErrInstanceNotCancellable
	}
	cancelledAt := command.At.UTC()
	updated := current
	updated.Status = InstanceStatusCancelled
	updated.Version++
	updated.UpdatedAt = cancelledAt
	receipt := InstanceCancellationReceipt{
		ID:                           uuid.New(),
		TenantID:                     current.TenantID,
		SeriesID:                     current.SeriesID,
		InstanceID:                   current.ID,
		PreviewID:                    command.PreviewID,
		IdempotencyKey:               command.IdempotencyKey,
		CancelledBy:                  command.CancelledBy,
		CancellationReason:           command.Reason,
		NotificationStrategy:         command.NotificationStrategy,
		SessionCount:                 assessment.SessionCount,
		NewlyCancelledSessionCount:   assessment.TargetSessionCount,
		AlreadyCancelledSessionCount: assessment.AlreadyCancelledSessionCount,
		CancelledRegistrationCount:   assessment.CancelledRegistrationCount,
		ReleasedConfirmedCount:       assessment.ConfirmedRegistrationCount,
		ReleasedHoldCount:            assessment.ActiveHoldCount,
		ClosedPendingOrderCount:      assessment.PendingOrderCount,
		RefundCaseCount:              assessment.RefundCaseCount,
		RequestedRefundCents:         assessment.RequestedRefundCents,
		CouponAdjustmentCount:        assessment.CouponAdjustmentCount,
		CancelledAt:                  cancelledAt,
		ResultingInstanceVersion:     updated.Version,
		CreatedAt:                    command.RecordedAt.UTC(),
	}
	return updated, receipt, nil
}

func validateInstanceCancellation(
	current Instance,
	command CancelInstanceCommand,
	assessment InstanceCancellationAssessment,
) error {
	reason := strings.TrimSpace(command.Reason)
	switch {
	case current.ID == uuid.Nil:
	case current.TenantID == uuid.Nil:
	case current.SeriesID == uuid.Nil:
	case current.Version < 1 || current.Version == math.MaxInt64:
	case current.CreatedAt.IsZero() || current.UpdatedAt.IsZero():
	case command.PreviewID == uuid.Nil:
	case command.CancelledBy == uuid.Nil:
	case !sessionCancellationKeyPattern.MatchString(command.IdempotencyKey):
	case reason == "" || reason != command.Reason || len([]rune(reason)) > 500:
	case command.NotificationStrategy != CancellationNotificationManualRequired:
	case command.At.IsZero() || command.At.Before(current.UpdatedAt):
	case command.RecordedAt.IsZero() || command.RecordedAt.Before(command.At):
	case assessment.SessionCount < 1:
	case assessment.TargetSessionCount < 1:
	case assessment.AlreadyCancelledSessionCount < 0:
	case assessment.SessionCount !=
		assessment.TargetSessionCount+assessment.AlreadyCancelledSessionCount:
	case len(assessment.SessionImpacts) != assessment.SessionCount:
	case assessment.CancelledRegistrationCount < 0:
	case assessment.ConfirmedRegistrationCount < 0:
	case assessment.ActiveHoldCount < 0:
	case assessment.CancelledRegistrationCount !=
		assessment.ConfirmedRegistrationCount+assessment.ActiveHoldCount:
	case assessment.PendingOrderCount < 0 ||
		assessment.PendingOrderCount > assessment.ActiveHoldCount:
	case assessment.UnknownPaymentCount < 0 ||
		assessment.PendingOrderCount+assessment.UnknownPaymentCount !=
			assessment.ActiveHoldCount:
	case assessment.FreeRegistrationCount < 0:
	case assessment.PaidRefundRegistrationCount < 0:
	case assessment.FreeRegistrationCount+
		assessment.PaidRefundRegistrationCount+
		assessment.CouponAdjustmentCount !=
		assessment.ConfirmedRegistrationCount:
	case assessment.RefundCaseCount !=
		assessment.PaidRefundRegistrationCount:
	case assessment.RequestedRefundCents < 0:
	case assessment.CouponAdjustmentCount < 0:
	case (assessment.RefundCaseCount == 0) !=
		(assessment.RequestedRefundCents == 0):
	default:
		return nil
	}
	return ErrInvalidInstanceCancellation
}

func copySessionCancellationAssessment(
	target *InstanceCancellationSessionImpact,
	source SessionCancellationAssessment,
) {
	target.CancelledRegistrationCount = source.CancelledRegistrationCount
	target.ConfirmedRegistrationCount = source.ConfirmedRegistrationCount
	target.ActiveHoldCount = source.ActiveHoldCount
	target.FreeRegistrationCount = source.FreeRegistrationCount
	target.PaidRefundRegistrationCount = source.PaidRefundRegistrationCount
	target.PendingOrderCount = source.PendingOrderCount
	target.UnknownPaymentCount = source.UnknownPaymentCount
	target.RefundCaseCount = source.RefundCaseCount
	target.RequestedRefundCents = source.RequestedRefundCents
	target.CouponAdjustmentCount = source.CouponAdjustmentCount
}

func addInstanceCancellationImpact(
	target *InstanceCancellationAssessment,
	source InstanceCancellationSessionImpact,
) error {
	if source.RequestedRefundCents >
		math.MaxInt64-target.RequestedRefundCents {
		return ErrInvalidInstanceCancellation
	}
	target.CancelledRegistrationCount += source.CancelledRegistrationCount
	target.ConfirmedRegistrationCount += source.ConfirmedRegistrationCount
	target.ActiveHoldCount += source.ActiveHoldCount
	target.FreeRegistrationCount += source.FreeRegistrationCount
	target.PaidRefundRegistrationCount += source.PaidRefundRegistrationCount
	target.PendingOrderCount += source.PendingOrderCount
	target.UnknownPaymentCount += source.UnknownPaymentCount
	target.RefundCaseCount += source.RefundCaseCount
	target.RequestedRefundCents += source.RequestedRefundCents
	target.CouponAdjustmentCount += source.CouponAdjustmentCount
	return nil
}

func instanceCancellationPreviewMatchesAssessment(
	preview InstanceCancellationPreview,
	assessment InstanceCancellationAssessment,
) bool {
	if preview.SessionCount != assessment.SessionCount ||
		preview.TargetSessionCount != assessment.TargetSessionCount ||
		preview.AlreadyCancelledSessionCount !=
			assessment.AlreadyCancelledSessionCount ||
		preview.CancelledRegistrationCount !=
			assessment.CancelledRegistrationCount ||
		preview.ConfirmedRegistrationCount !=
			assessment.ConfirmedRegistrationCount ||
		preview.ActiveHoldCount != assessment.ActiveHoldCount ||
		preview.FreeRegistrationCount != assessment.FreeRegistrationCount ||
		preview.PaidRefundRegistrationCount !=
			assessment.PaidRefundRegistrationCount ||
		preview.PendingOrderCount != assessment.PendingOrderCount ||
		preview.UnknownPaymentCount != assessment.UnknownPaymentCount ||
		preview.RefundCaseCount != assessment.RefundCaseCount ||
		preview.RequestedRefundCents != assessment.RequestedRefundCents ||
		preview.CouponAdjustmentCount != assessment.CouponAdjustmentCount ||
		preview.NotificationStrategy != CancellationNotificationManualRequired ||
		len(preview.SessionImpacts) != len(assessment.SessionImpacts) {
		return false
	}
	for index := range preview.SessionImpacts {
		if preview.SessionImpacts[index] != assessment.SessionImpacts[index] {
			return false
		}
	}
	return true
}

type canonicalInstanceCancellationSnapshot struct {
	TenantID          string                                 `json:"tenant_id"`
	SeriesID          string                                 `json:"series_id"`
	InstanceID        string                                 `json:"instance_id"`
	InstanceStatus    InstanceStatus                         `json:"instance_status"`
	InstanceVersion   int64                                  `json:"instance_version"`
	InstanceUpdatedAt string                                 `json:"instance_updated_at"`
	Sessions          []canonicalInstanceCancellationSession `json:"sessions"`
}

type canonicalInstanceCancellationSession struct {
	SessionID          string        `json:"session_id"`
	SessionStatus      SessionStatus `json:"session_status"`
	SessionVersion     int64         `json:"session_version"`
	SessionUpdatedAt   string        `json:"session_updated_at"`
	CancellationDigest string        `json:"cancellation_digest"`
}

func digestCanonicalInstanceCancellationSession(
	value canonicalInstanceCancellationSession,
) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf(
			"encode canonical cancelled Session snapshot: %w",
			err,
		)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
