package refund

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type EventType string

const (
	EventTypeProcessingStarted EventType = "processing_started"
	EventTypeRefundCompleted   EventType = "refund_completed"
	EventTypeRefundFailed      EventType = "refund_failed"
	EventTypeRefundRejected    EventType = "refund_rejected"
)

type Event struct {
	ID                     uuid.UUID
	TenantID               uuid.UUID
	RefundCaseID           uuid.UUID
	OrderID                uuid.UUID
	EventSequence          int64
	EventType              EventType
	IdempotencyKey         string
	FromStatus             Status
	ToStatus               Status
	ActorID                uuid.UUID
	SuccessfulRefundCents  int64
	ExternalRefundID       *string
	EvidenceReference      *string
	OperatorNote           *string
	FailureReason          *string
	OccurredAt             time.Time
	ResultingRefundVersion int64
	CreatedAt              time.Time
}

var (
	ErrInvalidRefundEvent  = errors.New("invalid xiangwan Refund event")
	ErrRefundEventConflict = errors.New("xiangwan Refund event conflicts with transition")
)

// NewEvent creates the immutable receipt for one state-changing Refund
// transition. Exact command replays are resolved from the stored event before
// this constructor is called and therefore never append another event.
func NewEvent(
	before Case,
	after Case,
	idempotencyKey string,
	recordedAt time.Time,
) (Event, error) {
	if !refundIdempotencyKeyPattern.MatchString(idempotencyKey) ||
		recordedAt.IsZero() ||
		after.UpdatedAt.IsZero() ||
		recordedAt.Before(after.UpdatedAt) ||
		after.HandledBy == nil ||
		before.ID == uuid.Nil ||
		!sameEventCaseIdentity(before, after) ||
		after.Version != before.Version+1 ||
		after.Version < 2 {
		return Event{}, ErrInvalidRefundEvent
	}
	eventType, err := eventTypeForTransition(before.RefundStatus, after.RefundStatus)
	if err != nil {
		return Event{}, err
	}
	return Event{
		ID:                     uuid.New(),
		TenantID:               after.TenantID,
		RefundCaseID:           after.ID,
		OrderID:                after.OrderID,
		EventSequence:          after.Version - 1,
		EventType:              eventType,
		IdempotencyKey:         idempotencyKey,
		FromStatus:             before.RefundStatus,
		ToStatus:               after.RefundStatus,
		ActorID:                *after.HandledBy,
		SuccessfulRefundCents:  after.SuccessfulRefundCents,
		ExternalRefundID:       cloneEventString(after.ExternalRefundID),
		EvidenceReference:      cloneEventString(after.EvidenceReference),
		OperatorNote:           cloneEventString(after.OperatorNote),
		FailureReason:          cloneEventString(after.FailureReason),
		OccurredAt:             after.UpdatedAt.UTC(),
		ResultingRefundVersion: after.Version,
		CreatedAt:              recordedAt.UTC(),
	}, nil
}

func eventTypeForTransition(from Status, to Status) (EventType, error) {
	switch to {
	case StatusProcessing:
		if from == StatusPendingManual || from == StatusFailed {
			return EventTypeProcessingStarted, nil
		}
	case StatusRefunded:
		if from == StatusPendingManual || from == StatusProcessing || from == StatusFailed {
			return EventTypeRefundCompleted, nil
		}
	case StatusFailed:
		if from == StatusPendingManual || from == StatusProcessing {
			return EventTypeRefundFailed, nil
		}
	case StatusRejected:
		if from == StatusPendingManual || from == StatusProcessing || from == StatusFailed {
			return EventTypeRefundRejected, nil
		}
	}
	return "", fmt.Errorf(
		"%w: %s to %s",
		ErrRefundEventConflict,
		from,
		to,
	)
}

func sameEventCaseIdentity(before Case, after Case) bool {
	return before.ID == after.ID &&
		before.TenantID == after.TenantID &&
		before.OrderID == after.OrderID &&
		before.RegistrationID == after.RegistrationID &&
		before.SeriesID == after.SeriesID &&
		before.InstanceID == after.InstanceID &&
		before.SessionID == after.SessionID &&
		before.PrincipalID == after.PrincipalID &&
		before.ReasonCode == after.ReasonCode &&
		before.IdempotencyKey == after.IdempotencyKey &&
		before.RequestedRefundCents == after.RequestedRefundCents &&
		before.CreatedAt.Equal(after.CreatedAt)
}

func cloneEventString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
