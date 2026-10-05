// Package registration owns Xiangwan participation eligibility. Payment,
// refund, and check-in are separate state axes and must not be inferred from a
// Registration status.
package registration

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ParticipationStatus string

const (
	ParticipationStatusPendingPayment ParticipationStatus = "pending_payment"
	ParticipationStatusConfirmed      ParticipationStatus = "confirmed"
	ParticipationStatusCancelled      ParticipationStatus = "cancelled"
)

type Registration struct {
	ID                  uuid.UUID
	TenantID            uuid.UUID
	SeriesID            uuid.UUID
	InstanceID          uuid.UUID
	SessionID           uuid.UUID
	PrincipalID         uuid.UUID
	ParticipationStatus ParticipationStatus
	IdempotencyKey      string
	ConfirmedAt         *time.Time
	CancelledAt         *time.Time
	CancellationReason  *string
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type NewRegistrationCommand struct {
	TenantID        uuid.UUID
	SeriesID        uuid.UUID
	InstanceID      uuid.UUID
	SessionID       uuid.UUID
	PrincipalID     uuid.UUID
	IdempotencyKey  string
	RequiresPayment bool
	Now             time.Time
}

var (
	ErrInvalidRegistration              = errors.New("invalid xiangwan Registration")
	ErrRegistrationTerminal             = errors.New("xiangwan Registration is terminal")
	ErrRegistrationCancellationConflict = errors.New("xiangwan Registration cancellation conflicts with recorded fact")
)

var registrationIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// ValidIdempotencyKey reports whether a caller-provided operation identity can
// be used to create or replay a Registration.
func ValidIdempotencyKey(value string) bool {
	return registrationIdempotencyKeyPattern.MatchString(value)
}

// NewRegistration creates either a paid pending context or a free confirmed
// participation fact. Callers must still execute this result inside the
// PostgreSQL Session-lock/capacity transaction.
func NewRegistration(command NewRegistrationCommand) (Registration, error) {
	if err := validateNewRegistrationCommand(command); err != nil {
		return Registration{}, err
	}

	now := command.Now.UTC()
	created := Registration{
		ID:                  uuid.New(),
		TenantID:            command.TenantID,
		SeriesID:            command.SeriesID,
		InstanceID:          command.InstanceID,
		SessionID:           command.SessionID,
		PrincipalID:         command.PrincipalID,
		ParticipationStatus: ParticipationStatusPendingPayment,
		IdempotencyKey:      command.IdempotencyKey,
		Version:             1,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if !command.RequiresPayment {
		created.ParticipationStatus = ParticipationStatusConfirmed
		created.ConfirmedAt = timePointer(now)
	}
	return created, nil
}

// ConfirmRegistration converts a paid pending context into participation. The
// changed result lets the transaction increment capacity/history exactly once.
func ConfirmRegistration(current Registration, at time.Time) (Registration, bool, error) {
	if at.IsZero() || current.CreatedAt.IsZero() || at.Before(current.CreatedAt) {
		return Registration{}, false, fmt.Errorf("%w: invalid confirmation time", ErrInvalidRegistration)
	}
	switch current.ParticipationStatus {
	case ParticipationStatusConfirmed:
		if current.ConfirmedAt == nil {
			return Registration{}, false, fmt.Errorf("%w: confirmed_at is required", ErrInvalidRegistration)
		}
		return cloneRegistration(current), false, nil
	case ParticipationStatusCancelled:
		return Registration{}, false, ErrRegistrationTerminal
	case ParticipationStatusPendingPayment:
	default:
		return Registration{}, false, fmt.Errorf("%w: unknown participation status", ErrInvalidRegistration)
	}

	confirmed := cloneRegistration(current)
	confirmedAt := at.UTC()
	confirmed.ParticipationStatus = ParticipationStatusConfirmed
	confirmed.ConfirmedAt = &confirmedAt
	confirmed.Version++
	confirmed.UpdatedAt = confirmedAt
	return confirmed, true, nil
}

// CancelRegistration invalidates participation immediately. A retry carrying
// the recorded reason is idempotent; it can never rewrite the cancellation fact.
func CancelRegistration(
	current Registration,
	reason string,
	at time.Time,
) (Registration, bool, error) {
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == "" || len([]rune(normalizedReason)) > 500 {
		return Registration{}, false, fmt.Errorf("%w: invalid cancellation reason", ErrInvalidRegistration)
	}
	if at.IsZero() || current.CreatedAt.IsZero() || at.Before(current.CreatedAt) ||
		(current.ConfirmedAt != nil && at.Before(*current.ConfirmedAt)) {
		return Registration{}, false, fmt.Errorf("%w: invalid cancellation time", ErrInvalidRegistration)
	}

	switch current.ParticipationStatus {
	case ParticipationStatusCancelled:
		if current.CancelledAt == nil || current.CancellationReason == nil {
			return Registration{}, false, fmt.Errorf("%w: cancellation fact is incomplete", ErrInvalidRegistration)
		}
		if *current.CancellationReason != normalizedReason {
			return Registration{}, false, ErrRegistrationCancellationConflict
		}
		return cloneRegistration(current), false, nil
	case ParticipationStatusPendingPayment, ParticipationStatusConfirmed:
	default:
		return Registration{}, false, fmt.Errorf("%w: unknown participation status", ErrInvalidRegistration)
	}

	cancelled := cloneRegistration(current)
	cancelledAt := at.UTC()
	cancelled.ParticipationStatus = ParticipationStatusCancelled
	cancelled.CancelledAt = &cancelledAt
	cancelled.CancellationReason = stringPointer(normalizedReason)
	cancelled.Version++
	cancelled.UpdatedAt = cancelledAt
	return cancelled, true, nil
}

func validateNewRegistrationCommand(command NewRegistrationCommand) error {
	switch {
	case command.TenantID == uuid.Nil:
		return fmt.Errorf("%w: tenant_id is required", ErrInvalidRegistration)
	case command.SeriesID == uuid.Nil:
		return fmt.Errorf("%w: series_id is required", ErrInvalidRegistration)
	case command.InstanceID == uuid.Nil:
		return fmt.Errorf("%w: instance_id is required", ErrInvalidRegistration)
	case command.SessionID == uuid.Nil:
		return fmt.Errorf("%w: session_id is required", ErrInvalidRegistration)
	case command.PrincipalID == uuid.Nil:
		return fmt.Errorf("%w: principal_id is required", ErrInvalidRegistration)
	case !ValidIdempotencyKey(command.IdempotencyKey):
		return fmt.Errorf("%w: idempotency_key is invalid", ErrInvalidRegistration)
	case command.Now.IsZero():
		return fmt.Errorf("%w: now is required", ErrInvalidRegistration)
	default:
		return nil
	}
}

func cloneRegistration(value Registration) Registration {
	cloned := value
	cloned.ConfirmedAt = cloneTime(value.ConfirmedAt)
	cloned.CancelledAt = cloneTime(value.CancelledAt)
	if value.CancellationReason != nil {
		reason := *value.CancellationReason
		cloned.CancellationReason = &reason
	}
	return cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func stringPointer(value string) *string {
	return &value
}
