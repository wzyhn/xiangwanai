package activity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"time"

	"github.com/google/uuid"
)

const SessionCancellationPreviewLifetime = 10 * time.Minute

type CancellationNotificationStrategy string

const CancellationNotificationManualRequired CancellationNotificationStrategy = "manual_required"

type SessionCancellationPreview struct {
	ID                          uuid.UUID
	TenantID                    uuid.UUID
	SeriesID                    uuid.UUID
	InstanceID                  uuid.UUID
	SessionID                   uuid.UUID
	ParentInstancePreviewID     *uuid.UUID
	RequestedBy                 uuid.UUID
	IdempotencyKey              string
	SnapshotDigest              string
	ExpectedSessionVersion      int64
	CancelledRegistrationCount  int
	ConfirmedRegistrationCount  int
	ActiveHoldCount             int
	FreeRegistrationCount       int
	PaidRefundRegistrationCount int
	PendingOrderCount           int
	UnknownPaymentCount         int
	RefundCaseCount             int
	RequestedRefundCents        int64
	CouponAdjustmentCount       int
	NotificationStrategy        CancellationNotificationStrategy
	ExpiresAt                   time.Time
	ConsumedAt                  *time.Time
	CreatedAt                   time.Time
}

type SessionCancellationPreviewCommand struct {
	RequestedBy    uuid.UUID
	IdempotencyKey string
	At             time.Time
}

type SessionCancellationSnapshot struct {
	TenantID                   uuid.UUID
	SeriesID                   uuid.UUID
	InstanceID                 uuid.UUID
	SessionID                  uuid.UUID
	SessionStatus              SessionStatus
	SessionVersion             int64
	SessionUpdatedAt           time.Time
	RefundReasonCode           string
	ConfirmedRegistrationCount int
	ActiveHoldCount            int
	Registrations              []SessionCancellationRegistrationSnapshot
}

type SessionCancellationRegistrationSnapshot struct {
	RegistrationID      uuid.UUID
	ParticipationStatus string
	Version             int64
	UpdatedAt           time.Time
	Order               *SessionCancellationOrderSnapshot
}

type SessionCancellationOrderSnapshot struct {
	OrderID            uuid.UUID
	PaymentStatus      string
	OriginalPriceCents int64
	DiscountCents      int64
	PayableCents       int64
	ActualPaidCents    *int64
	Version            int64
	UpdatedAt          time.Time
	Hold               SessionCancellationHoldSnapshot
	Refund             *SessionCancellationRefundSnapshot
	Coupon             *SessionCancellationCouponSnapshot
}

type SessionCancellationHoldSnapshot struct {
	HoldID     uuid.UUID
	HoldStatus string
	ExpiresAt  time.Time
	Version    int64
	UpdatedAt  time.Time
}

type SessionCancellationRefundSnapshot struct {
	RefundCaseID          uuid.UUID
	RefundStatus          string
	ReasonCode            string
	RequestedRefundCents  int64
	SuccessfulRefundCents int64
	Version               int64
	UpdatedAt             time.Time
}

type SessionCancellationCouponSnapshot struct {
	CouponID            uuid.UUID
	RedemptionEntryID   uuid.UUID
	LedgerEntrySequence int64
}

type SessionCancellationAssessment struct {
	CancelledRegistrationCount  int
	ConfirmedRegistrationCount  int
	ActiveHoldCount             int
	FreeRegistrationCount       int
	PaidRefundRegistrationCount int
	PendingOrderCount           int
	UnknownPaymentCount         int
	RefundCaseCount             int
	RequestedRefundCents        int64
	CouponAdjustmentCount       int
}

var (
	ErrInvalidSessionCancellationPreview = errors.New(
		"invalid xiangwan Session cancellation preview",
	)
	sessionCancellationPreviewDigestPattern = regexp.MustCompile(
		`^[0-9a-f]{64}$`,
	)
)

// NewSessionCancellationPreview creates a short-lived confirmation snapshot.
// The digest covers every mutable participation and financial fact used by the
// eventual cancellation transaction.
func NewSessionCancellationPreview(
	command SessionCancellationPreviewCommand,
	snapshot SessionCancellationSnapshot,
) (SessionCancellationPreview, error) {
	if command.RequestedBy == uuid.Nil ||
		!sessionCancellationKeyPattern.MatchString(command.IdempotencyKey) ||
		command.At.IsZero() {
		return SessionCancellationPreview{}, ErrInvalidSessionCancellationPreview
	}
	assessment, digest, err := AssessSessionCancellation(snapshot)
	if err != nil {
		return SessionCancellationPreview{}, err
	}

	createdAt := command.At.UTC()
	return SessionCancellationPreview{
		ID:                          uuid.New(),
		TenantID:                    snapshot.TenantID,
		SeriesID:                    snapshot.SeriesID,
		InstanceID:                  snapshot.InstanceID,
		SessionID:                   snapshot.SessionID,
		RequestedBy:                 command.RequestedBy,
		IdempotencyKey:              command.IdempotencyKey,
		SnapshotDigest:              digest,
		ExpectedSessionVersion:      snapshot.SessionVersion,
		CancelledRegistrationCount:  assessment.CancelledRegistrationCount,
		ConfirmedRegistrationCount:  assessment.ConfirmedRegistrationCount,
		ActiveHoldCount:             assessment.ActiveHoldCount,
		FreeRegistrationCount:       assessment.FreeRegistrationCount,
		PaidRefundRegistrationCount: assessment.PaidRefundRegistrationCount,
		PendingOrderCount:           assessment.PendingOrderCount,
		UnknownPaymentCount:         assessment.UnknownPaymentCount,
		RefundCaseCount:             assessment.RefundCaseCount,
		RequestedRefundCents:        assessment.RequestedRefundCents,
		CouponAdjustmentCount:       assessment.CouponAdjustmentCount,
		NotificationStrategy:        CancellationNotificationManualRequired,
		ExpiresAt:                   createdAt.Add(SessionCancellationPreviewLifetime),
		CreatedAt:                   createdAt,
	}, nil
}

// AssessSessionCancellation validates and digests a cancellation snapshot while
// deriving the impact shown to the operator.
func AssessSessionCancellation(
	snapshot SessionCancellationSnapshot,
) (SessionCancellationAssessment, string, error) {
	if err := validateSessionCancellationSnapshot(snapshot); err != nil {
		return SessionCancellationAssessment{}, "", err
	}

	registrations := append(
		[]SessionCancellationRegistrationSnapshot(nil),
		snapshot.Registrations...,
	)
	sort.Slice(registrations, func(left, right int) bool {
		return registrations[left].RegistrationID.String() <
			registrations[right].RegistrationID.String()
	})

	assessment := SessionCancellationAssessment{
		CancelledRegistrationCount: len(registrations),
		CouponAdjustmentCount:      0,
	}
	seen := make(map[uuid.UUID]struct{}, len(registrations))
	for _, current := range registrations {
		if _, exists := seen[current.RegistrationID]; exists {
			return SessionCancellationAssessment{}, "", invalidCancellationPreview(
				"duplicate Registration",
			)
		}
		seen[current.RegistrationID] = struct{}{}
		if err := validateSessionCancellationRegistrationSnapshot(
			current,
			snapshot.RefundReasonCode,
		); err != nil {
			return SessionCancellationAssessment{}, "", err
		}

		switch current.ParticipationStatus {
		case "confirmed":
			assessment.ConfirmedRegistrationCount++
			if current.Order == nil {
				assessment.FreeRegistrationCount++
				continue
			}
			switch current.Order.PaymentStatus {
			case "paid_confirmed":
				if current.Order.ActualPaidCents == nil ||
					*current.Order.ActualPaidCents <= 0 ||
					current.Order.Hold.HoldStatus != "converted" {
					return SessionCancellationAssessment{}, "", invalidCancellationPreview(
						"paid Registration facts do not converge",
					)
				}
				assessment.PaidRefundRegistrationCount++
				assessment.RefundCaseCount++
				if *current.Order.ActualPaidCents >
					math.MaxInt64-assessment.RequestedRefundCents {
					return SessionCancellationAssessment{}, "", invalidCancellationPreview(
						"refund amount overflows",
					)
				}
				assessment.RequestedRefundCents += *current.Order.ActualPaidCents
			case "settled_zero":
				if current.Order.ActualPaidCents != nil ||
					current.Order.OriginalPriceCents <= 0 ||
					current.Order.DiscountCents !=
						current.Order.OriginalPriceCents ||
					current.Order.PayableCents != 0 ||
					current.Order.Hold.HoldStatus != "converted" ||
					current.Order.Coupon == nil {
					return SessionCancellationAssessment{}, "", invalidCancellationPreview(
						"zero-settled Registration facts do not converge",
					)
				}
				assessment.CouponAdjustmentCount++
			default:
				return SessionCancellationAssessment{}, "", invalidCancellationPreview(
					"confirmed Registration has incompatible Order",
				)
			}
		case "pending_payment":
			if current.Order == nil ||
				current.Order.Hold.HoldStatus != "active" {
				return SessionCancellationAssessment{}, "", invalidCancellationPreview(
					"pending Registration lacks active payment hold",
				)
			}
			assessment.ActiveHoldCount++
			switch current.Order.PaymentStatus {
			case "pending":
				assessment.PendingOrderCount++
			case "unknown":
				assessment.UnknownPaymentCount++
			default:
				return SessionCancellationAssessment{}, "", invalidCancellationPreview(
					"pending Registration has incompatible Order",
				)
			}
		default:
			return SessionCancellationAssessment{}, "", invalidCancellationPreview(
				"Registration is not open",
			)
		}
	}

	if assessment.ConfirmedRegistrationCount !=
		snapshot.ConfirmedRegistrationCount ||
		assessment.ActiveHoldCount != snapshot.ActiveHoldCount {
		return SessionCancellationAssessment{}, "", invalidCancellationPreview(
			"Session counters do not match child facts",
		)
	}

	canonical := canonicalSessionCancellationSnapshot{
		TenantID:                   snapshot.TenantID.String(),
		SeriesID:                   snapshot.SeriesID.String(),
		InstanceID:                 snapshot.InstanceID.String(),
		SessionID:                  snapshot.SessionID.String(),
		SessionStatus:              snapshot.SessionStatus,
		SessionVersion:             snapshot.SessionVersion,
		SessionUpdatedAt:           canonicalPublicationTime(snapshot.SessionUpdatedAt),
		RefundReasonCode:           snapshot.RefundReasonCode,
		ConfirmedRegistrationCount: snapshot.ConfirmedRegistrationCount,
		ActiveHoldCount:            snapshot.ActiveHoldCount,
		Registrations: canonicalizeSessionCancellationRegistrations(
			registrations,
		),
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return SessionCancellationAssessment{}, "", fmt.Errorf(
			"encode canonical Session cancellation snapshot: %w",
			err,
		)
	}
	sum := sha256.Sum256(encoded)
	return assessment, hex.EncodeToString(sum[:]), nil
}

// SessionCancellationPreviewMatchesSnapshot rechecks the complete digest and
// operator-visible impact immediately before a cancellation commit.
func SessionCancellationPreviewMatchesSnapshot(
	preview SessionCancellationPreview,
	snapshot SessionCancellationSnapshot,
) (bool, error) {
	assessment, digest, err := AssessSessionCancellation(snapshot)
	if err != nil {
		return false, err
	}
	return preview.TenantID == snapshot.TenantID &&
			preview.SeriesID == snapshot.SeriesID &&
			preview.InstanceID == snapshot.InstanceID &&
			preview.SessionID == snapshot.SessionID &&
			preview.ExpectedSessionVersion == snapshot.SessionVersion &&
			preview.SnapshotDigest == digest &&
			preview.CancelledRegistrationCount ==
				assessment.CancelledRegistrationCount &&
			preview.ConfirmedRegistrationCount ==
				assessment.ConfirmedRegistrationCount &&
			preview.ActiveHoldCount == assessment.ActiveHoldCount &&
			preview.FreeRegistrationCount == assessment.FreeRegistrationCount &&
			preview.PaidRefundRegistrationCount ==
				assessment.PaidRefundRegistrationCount &&
			preview.PendingOrderCount == assessment.PendingOrderCount &&
			preview.UnknownPaymentCount == assessment.UnknownPaymentCount &&
			preview.RefundCaseCount == assessment.RefundCaseCount &&
			preview.RequestedRefundCents == assessment.RequestedRefundCents &&
			preview.CouponAdjustmentCount == assessment.CouponAdjustmentCount &&
			preview.NotificationStrategy == CancellationNotificationManualRequired,
		nil
}

func validateSessionCancellationSnapshot(snapshot SessionCancellationSnapshot) error {
	switch {
	case snapshot.TenantID == uuid.Nil:
	case snapshot.SeriesID == uuid.Nil:
	case snapshot.InstanceID == uuid.Nil:
	case snapshot.SessionID == uuid.Nil:
	case snapshot.SessionStatus != SessionStatusPublished:
	case snapshot.SessionVersion < 1 || snapshot.SessionVersion == math.MaxInt64:
	case snapshot.SessionUpdatedAt.IsZero():
	case snapshot.RefundReasonCode != "session_cancelled" &&
		snapshot.RefundReasonCode != "instance_cancelled":
	case snapshot.ConfirmedRegistrationCount < 0:
	case snapshot.ActiveHoldCount < 0:
	default:
		return nil
	}
	return invalidCancellationPreview("invalid Session identity or state")
}

func validateSessionCancellationRegistrationSnapshot(
	current SessionCancellationRegistrationSnapshot,
	refundReasonCode string,
) error {
	if current.RegistrationID == uuid.Nil ||
		current.Version < 1 ||
		current.UpdatedAt.IsZero() {
		return invalidCancellationPreview("invalid Registration fact")
	}
	if current.Order == nil {
		if current.ParticipationStatus != "confirmed" {
			return invalidCancellationPreview("open Registration lacks Order")
		}
		return nil
	}
	order := current.Order
	if order.OrderID == uuid.Nil ||
		order.OriginalPriceCents < 0 || order.DiscountCents < 0 ||
		order.DiscountCents > order.OriginalPriceCents ||
		order.PayableCents != order.OriginalPriceCents-order.DiscountCents ||
		order.Version < 1 ||
		order.UpdatedAt.IsZero() ||
		order.Hold.HoldID == uuid.Nil ||
		order.Hold.Version < 1 ||
		order.Hold.ExpiresAt.IsZero() ||
		order.Hold.UpdatedAt.IsZero() {
		return invalidCancellationPreview("invalid payment fact")
	}
	if order.Coupon != nil && (order.PaymentStatus != "settled_zero" ||
		order.Coupon.CouponID == uuid.Nil ||
		order.Coupon.RedemptionEntryID == uuid.Nil ||
		order.Coupon.LedgerEntrySequence < 3) {
		return invalidCancellationPreview("invalid Coupon fact")
	}
	if order.Refund == nil {
		return nil
	}
	if current.ParticipationStatus != "confirmed" ||
		order.Refund.RefundCaseID == uuid.Nil ||
		order.Refund.ReasonCode != refundReasonCode ||
		order.Refund.RequestedRefundCents <= 0 ||
		order.ActualPaidCents == nil ||
		order.Refund.RequestedRefundCents != *order.ActualPaidCents ||
		order.Refund.SuccessfulRefundCents < 0 ||
		order.Refund.SuccessfulRefundCents >
			order.Refund.RequestedRefundCents ||
		order.Refund.Version < 1 ||
		order.Refund.UpdatedAt.IsZero() {
		return invalidCancellationPreview("invalid Refund fact")
	}
	switch order.Refund.RefundStatus {
	case "pending_manual", "processing", "refunded", "failed", "rejected":
	default:
		return invalidCancellationPreview("invalid Refund status")
	}
	return nil
}

func invalidCancellationPreview(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSessionCancellationPreview, reason)
}

type canonicalSessionCancellationSnapshot struct {
	TenantID                   string                                     `json:"tenant_id"`
	SeriesID                   string                                     `json:"series_id"`
	InstanceID                 string                                     `json:"instance_id"`
	SessionID                  string                                     `json:"session_id"`
	SessionStatus              SessionStatus                              `json:"session_status"`
	SessionVersion             int64                                      `json:"session_version"`
	SessionUpdatedAt           string                                     `json:"session_updated_at"`
	RefundReasonCode           string                                     `json:"refund_reason_code"`
	ConfirmedRegistrationCount int                                        `json:"confirmed_registration_count"`
	ActiveHoldCount            int                                        `json:"active_hold_count"`
	Registrations              []canonicalSessionCancellationRegistration `json:"registrations"`
}

func validSessionCancellationPreviewDigest(value string) bool {
	return sessionCancellationPreviewDigestPattern.MatchString(value)
}

type canonicalSessionCancellationRegistration struct {
	RegistrationID      string                             `json:"registration_id"`
	ParticipationStatus string                             `json:"participation_status"`
	Version             int64                              `json:"version"`
	UpdatedAt           string                             `json:"updated_at"`
	Order               *canonicalSessionCancellationOrder `json:"order"`
}

type canonicalSessionCancellationOrder struct {
	OrderID            string                              `json:"order_id"`
	PaymentStatus      string                              `json:"payment_status"`
	OriginalPriceCents int64                               `json:"original_price_cents"`
	DiscountCents      int64                               `json:"discount_cents"`
	PayableCents       int64                               `json:"payable_cents"`
	ActualPaidCents    *int64                              `json:"actual_paid_cents"`
	Version            int64                               `json:"version"`
	UpdatedAt          string                              `json:"updated_at"`
	Hold               canonicalSessionCancellationHold    `json:"hold"`
	Refund             *canonicalSessionCancellationRefund `json:"refund"`
	Coupon             *canonicalSessionCancellationCoupon `json:"coupon"`
}

type canonicalSessionCancellationHold struct {
	HoldID     string `json:"hold_id"`
	HoldStatus string `json:"hold_status"`
	ExpiresAt  string `json:"expires_at"`
	Version    int64  `json:"version"`
	UpdatedAt  string `json:"updated_at"`
}

type canonicalSessionCancellationRefund struct {
	RefundCaseID          string `json:"refund_case_id"`
	RefundStatus          string `json:"refund_status"`
	ReasonCode            string `json:"reason_code"`
	RequestedRefundCents  int64  `json:"requested_refund_cents"`
	SuccessfulRefundCents int64  `json:"successful_refund_cents"`
	Version               int64  `json:"version"`
	UpdatedAt             string `json:"updated_at"`
}

type canonicalSessionCancellationCoupon struct {
	CouponID            string `json:"coupon_id"`
	RedemptionEntryID   string `json:"redemption_entry_id"`
	LedgerEntrySequence int64  `json:"ledger_entry_sequence"`
}

func canonicalizeSessionCancellationRegistrations(
	values []SessionCancellationRegistrationSnapshot,
) []canonicalSessionCancellationRegistration {
	canonical := make(
		[]canonicalSessionCancellationRegistration,
		0,
		len(values),
	)
	for _, value := range values {
		current := canonicalSessionCancellationRegistration{
			RegistrationID:      value.RegistrationID.String(),
			ParticipationStatus: value.ParticipationStatus,
			Version:             value.Version,
			UpdatedAt:           canonicalPublicationTime(value.UpdatedAt),
		}
		if value.Order != nil {
			order := value.Order
			orderValue := &canonicalSessionCancellationOrder{
				OrderID:            order.OrderID.String(),
				PaymentStatus:      order.PaymentStatus,
				OriginalPriceCents: order.OriginalPriceCents,
				DiscountCents:      order.DiscountCents,
				PayableCents:       order.PayableCents,
				ActualPaidCents:    cloneCancellationPreviewInt64(order.ActualPaidCents),
				Version:            order.Version,
				UpdatedAt:          canonicalPublicationTime(order.UpdatedAt),
				Hold: canonicalSessionCancellationHold{
					HoldID:     order.Hold.HoldID.String(),
					HoldStatus: order.Hold.HoldStatus,
					ExpiresAt:  canonicalPublicationTime(order.Hold.ExpiresAt),
					Version:    order.Hold.Version,
					UpdatedAt:  canonicalPublicationTime(order.Hold.UpdatedAt),
				},
			}
			if order.Refund != nil {
				refund := order.Refund
				orderValue.Refund = &canonicalSessionCancellationRefund{
					RefundCaseID:          refund.RefundCaseID.String(),
					RefundStatus:          refund.RefundStatus,
					ReasonCode:            refund.ReasonCode,
					RequestedRefundCents:  refund.RequestedRefundCents,
					SuccessfulRefundCents: refund.SuccessfulRefundCents,
					Version:               refund.Version,
					UpdatedAt:             canonicalPublicationTime(refund.UpdatedAt),
				}
			}
			if order.Coupon != nil {
				coupon := order.Coupon
				orderValue.Coupon = &canonicalSessionCancellationCoupon{
					CouponID:            coupon.CouponID.String(),
					RedemptionEntryID:   coupon.RedemptionEntryID.String(),
					LedgerEntrySequence: coupon.LedgerEntrySequence,
				}
			}
			current.Order = orderValue
		}
		canonical = append(canonical, current)
	}
	return canonical
}

func cloneCancellationPreviewInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
