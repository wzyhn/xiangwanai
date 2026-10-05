package activity

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type SessionCancellationImpact struct {
	CancelledRegistrationCount int
	ReleasedConfirmedCount     int
	ReleasedHoldCount          int
	ClosedPendingOrderCount    int
	RefundCaseCount            int
	RequestedRefundCents       int64
	CouponAdjustmentCount      int
}

type SessionCancellationReceipt struct {
	ID                         uuid.UUID
	TenantID                   uuid.UUID
	SeriesID                   uuid.UUID
	InstanceID                 uuid.UUID
	SessionID                  uuid.UUID
	PreviewID                  uuid.UUID
	IdempotencyKey             string
	CancelledBy                uuid.UUID
	CancellationReason         string
	NotificationStrategy       CancellationNotificationStrategy
	CancelledRegistrationCount int
	ReleasedConfirmedCount     int
	ReleasedHoldCount          int
	ClosedPendingOrderCount    int
	RefundCaseCount            int
	RequestedRefundCents       int64
	CouponAdjustmentCount      int
	CancelledAt                time.Time
	ResultingSessionVersion    int64
	CreatedAt                  time.Time
}

type CancelSessionCommand struct {
	SeriesID             uuid.UUID
	PreviewID            uuid.UUID
	CancelledBy          uuid.UUID
	IdempotencyKey       string
	Reason               string
	NotificationStrategy CancellationNotificationStrategy
	At                   time.Time
	RecordedAt           time.Time
}

var (
	ErrInvalidSessionCancellation = errors.New("invalid xiangwan Session cancellation")
	ErrSessionNotCancellable      = errors.New("xiangwan Session is not cancellable")
)

var sessionCancellationKeyPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`,
)

// CancelSession creates the terminal Session projection and its immutable
// aggregate receipt. The caller must derive impact while holding every affected
// Registration, Order, hold, and Refund row in the same transaction.
func CancelSession(
	current Session,
	command CancelSessionCommand,
	impact SessionCancellationImpact,
) (Session, SessionCancellationReceipt, error) {
	if err := validateSessionCancellation(current, command, impact); err != nil {
		return Session{}, SessionCancellationReceipt{}, err
	}
	if current.Status != SessionStatusPublished {
		return Session{}, SessionCancellationReceipt{}, ErrSessionNotCancellable
	}

	cancelledAt := command.At.UTC()
	updated := current
	updated.Status = SessionStatusCancelled
	updated.ConfirmedRegistrationCount = 0
	updated.ActiveHoldCount = 0
	updated.Version++
	updated.UpdatedAt = cancelledAt

	receipt := SessionCancellationReceipt{
		ID:                         uuid.New(),
		TenantID:                   current.TenantID,
		SeriesID:                   command.SeriesID,
		InstanceID:                 current.InstanceID,
		SessionID:                  current.ID,
		PreviewID:                  command.PreviewID,
		IdempotencyKey:             command.IdempotencyKey,
		CancelledBy:                command.CancelledBy,
		CancellationReason:         command.Reason,
		NotificationStrategy:       command.NotificationStrategy,
		CancelledRegistrationCount: impact.CancelledRegistrationCount,
		ReleasedConfirmedCount:     impact.ReleasedConfirmedCount,
		ReleasedHoldCount:          impact.ReleasedHoldCount,
		ClosedPendingOrderCount:    impact.ClosedPendingOrderCount,
		RefundCaseCount:            impact.RefundCaseCount,
		RequestedRefundCents:       impact.RequestedRefundCents,
		CouponAdjustmentCount:      impact.CouponAdjustmentCount,
		CancelledAt:                cancelledAt,
		ResultingSessionVersion:    updated.Version,
		CreatedAt:                  command.RecordedAt.UTC(),
	}
	return updated, receipt, nil
}

func validateSessionCancellation(
	current Session,
	command CancelSessionCommand,
	impact SessionCancellationImpact,
) error {
	reason := strings.TrimSpace(command.Reason)
	switch {
	case current.ID == uuid.Nil:
	case current.TenantID == uuid.Nil:
	case current.InstanceID == uuid.Nil:
	case command.SeriesID == uuid.Nil:
	case command.PreviewID == uuid.Nil:
	case command.CancelledBy == uuid.Nil:
	case !sessionCancellationKeyPattern.MatchString(command.IdempotencyKey):
	case reason == "" || reason != command.Reason || len([]rune(reason)) > 500:
	case command.NotificationStrategy != CancellationNotificationManualRequired:
	case current.Version < 1:
	case current.CreatedAt.IsZero() || current.UpdatedAt.IsZero():
	case command.At.IsZero() || command.At.Before(current.UpdatedAt):
	case command.At.Before(current.CreatedAt):
	case command.RecordedAt.IsZero() || command.RecordedAt.Before(command.At):
	case current.ConfirmedRegistrationCount < 0 || current.ActiveHoldCount < 0:
	case impact.CancelledRegistrationCount < 0:
	case impact.ReleasedConfirmedCount < 0:
	case impact.ReleasedHoldCount < 0:
	case impact.ClosedPendingOrderCount < 0:
	case impact.RefundCaseCount < 0:
	case impact.RequestedRefundCents < 0:
	case impact.CouponAdjustmentCount < 0:
	case impact.ReleasedConfirmedCount != current.ConfirmedRegistrationCount:
	case impact.ReleasedHoldCount != current.ActiveHoldCount:
	case impact.CancelledRegistrationCount !=
		impact.ReleasedConfirmedCount+impact.ReleasedHoldCount:
	case impact.ClosedPendingOrderCount > impact.ReleasedHoldCount:
	case impact.RefundCaseCount+impact.CouponAdjustmentCount >
		impact.ReleasedConfirmedCount:
	case (impact.RefundCaseCount == 0) != (impact.RequestedRefundCents == 0):
	default:
		return nil
	}
	return fmt.Errorf("%w: facts do not converge", ErrInvalidSessionCancellation)
}
