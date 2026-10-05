// Package refund owns Xiangwan manual Refund processing. Participation and
// payment remain immutable facts on their own state axes.
package refund

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusPendingManual Status = "pending_manual"
	StatusProcessing    Status = "processing"
	StatusRefunded      Status = "refunded"
	StatusFailed        Status = "failed"
	StatusRejected      Status = "rejected"
)

type ReasonCode string

const (
	ReasonHoldExpiredAfterPayment        ReasonCode = "hold_expired_after_payment"
	ReasonOrderClosedAfterPayment        ReasonCode = "order_closed_after_payment"
	ReasonSessionUnavailableAfterPayment ReasonCode = "session_unavailable_after_payment"
	ReasonUserCancelled                  ReasonCode = "user_cancelled"
	ReasonSessionCancelled               ReasonCode = "session_cancelled"
	ReasonInstanceCancelled              ReasonCode = "instance_cancelled"
	ReasonOperatorAdjustment             ReasonCode = "operator_adjustment"
)

type Case struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	OrderID               uuid.UUID
	RegistrationID        uuid.UUID
	SeriesID              uuid.UUID
	InstanceID            uuid.UUID
	SessionID             uuid.UUID
	PrincipalID           uuid.UUID
	RefundStatus          Status
	ReasonCode            ReasonCode
	IdempotencyKey        string
	RequestedRefundCents  int64
	SuccessfulRefundCents int64
	ProcessingStartedAt   *time.Time
	ResolvedAt            *time.Time
	HandledBy             *uuid.UUID
	ExternalRefundID      *string
	EvidenceReference     *string
	OperatorNote          *string
	FailureReason         *string
	Version               int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type NewCaseCommand struct {
	TenantID             uuid.UUID
	OrderID              uuid.UUID
	RegistrationID       uuid.UUID
	SeriesID             uuid.UUID
	InstanceID           uuid.UUID
	SessionID            uuid.UUID
	PrincipalID          uuid.UUID
	ReasonCode           ReasonCode
	IdempotencyKey       string
	ActualPaidCents      int64
	RequestedRefundCents int64
	OperatorNote         string
	Now                  time.Time
}

type StartProcessingCommand struct {
	HandledBy    uuid.UUID
	OperatorNote string
	At           time.Time
}

type CompleteCommand struct {
	HandledBy         uuid.UUID
	ExternalRefundID  string
	EvidenceReference string
	OperatorNote      string
	At                time.Time
}

type ResolveFailureCommand struct {
	HandledBy     uuid.UUID
	FailureReason string
	OperatorNote  string
	At            time.Time
}

var (
	ErrInvalidRefund        = errors.New("invalid xiangwan Refund")
	ErrRefundTerminal       = errors.New("xiangwan Refund is terminal")
	ErrRefundReplayConflict = errors.New("xiangwan Refund replay conflicts with recorded fact")
)

var refundIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func NewCase(command NewCaseCommand) (Case, error) {
	if err := validateNewCaseCommand(command); err != nil {
		return Case{}, err
	}
	now := command.Now.UTC()
	return Case{
		ID:                    uuid.New(),
		TenantID:              command.TenantID,
		OrderID:               command.OrderID,
		RegistrationID:        command.RegistrationID,
		SeriesID:              command.SeriesID,
		InstanceID:            command.InstanceID,
		SessionID:             command.SessionID,
		PrincipalID:           command.PrincipalID,
		RefundStatus:          StatusPendingManual,
		ReasonCode:            command.ReasonCode,
		IdempotencyKey:        command.IdempotencyKey,
		RequestedRefundCents:  command.RequestedRefundCents,
		SuccessfulRefundCents: 0,
		OperatorNote:          optionalString(command.OperatorNote),
		Version:               1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}, nil
}

func StartProcessing(
	current Case,
	command StartProcessingCommand,
) (Case, bool, error) {
	if err := validateOperatorTransition(
		current,
		command.HandledBy,
		command.OperatorNote,
		command.At,
	); err != nil {
		return Case{}, false, err
	}
	switch current.RefundStatus {
	case StatusProcessing:
		if current.ProcessingStartedAt == nil ||
			current.HandledBy == nil ||
			*current.HandledBy != command.HandledBy ||
			!current.ProcessingStartedAt.Equal(command.At) ||
			!optionalStringEqual(current.OperatorNote, command.OperatorNote) {
			return Case{}, false, ErrRefundReplayConflict
		}
		return cloneCase(current), false, nil
	case StatusPendingManual, StatusFailed:
	case StatusRefunded, StatusRejected:
		return Case{}, false, ErrRefundTerminal
	default:
		return Case{}, false, fmt.Errorf("%w: unknown status", ErrInvalidRefund)
	}

	updated := cloneCase(current)
	startedAt := command.At.UTC()
	handledBy := command.HandledBy
	updated.RefundStatus = StatusProcessing
	updated.ProcessingStartedAt = &startedAt
	updated.ResolvedAt = nil
	updated.HandledBy = &handledBy
	updated.ExternalRefundID = nil
	updated.EvidenceReference = nil
	updated.OperatorNote = optionalString(command.OperatorNote)
	updated.FailureReason = nil
	updated.SuccessfulRefundCents = 0
	updated.Version++
	updated.UpdatedAt = startedAt
	return updated, true, nil
}

func Complete(
	current Case,
	command CompleteCommand,
) (Case, bool, error) {
	if err := validateOperatorTransition(
		current,
		command.HandledBy,
		command.OperatorNote,
		command.At,
	); err != nil {
		return Case{}, false, err
	}
	externalRefundID := normalizeOptional(command.ExternalRefundID)
	evidenceReference := normalizeOptional(command.EvidenceReference)
	if (externalRefundID == "" && evidenceReference == "") ||
		len([]rune(externalRefundID)) > 128 ||
		len([]rune(evidenceReference)) > 500 {
		return Case{}, false, fmt.Errorf("%w: completion evidence is invalid", ErrInvalidRefund)
	}
	if current.RequestedRefundCents <= 0 {
		return Case{}, false, fmt.Errorf("%w: requested amount is invalid", ErrInvalidRefund)
	}

	if current.RefundStatus == StatusRefunded {
		if current.ResolvedAt == nil ||
			current.HandledBy == nil ||
			*current.HandledBy != command.HandledBy ||
			!current.ResolvedAt.Equal(command.At) ||
			!optionalStringEqual(current.ExternalRefundID, externalRefundID) ||
			!optionalStringEqual(current.EvidenceReference, evidenceReference) ||
			!optionalStringEqual(current.OperatorNote, command.OperatorNote) ||
			current.SuccessfulRefundCents != current.RequestedRefundCents {
			return Case{}, false, ErrRefundReplayConflict
		}
		return cloneCase(current), false, nil
	}
	switch current.RefundStatus {
	case StatusPendingManual, StatusProcessing, StatusFailed:
	case StatusRejected:
		return Case{}, false, ErrRefundTerminal
	default:
		return Case{}, false, fmt.Errorf("%w: unknown status", ErrInvalidRefund)
	}

	updated := cloneCase(current)
	resolvedAt := command.At.UTC()
	handledBy := command.HandledBy
	updated.RefundStatus = StatusRefunded
	updated.SuccessfulRefundCents = current.RequestedRefundCents
	updated.ResolvedAt = &resolvedAt
	updated.HandledBy = &handledBy
	updated.ExternalRefundID = optionalString(externalRefundID)
	updated.EvidenceReference = optionalString(evidenceReference)
	updated.OperatorNote = optionalString(command.OperatorNote)
	updated.FailureReason = nil
	updated.Version++
	updated.UpdatedAt = resolvedAt
	return updated, true, nil
}

func Fail(
	current Case,
	command ResolveFailureCommand,
) (Case, bool, error) {
	return resolveUnsuccessful(current, StatusFailed, command)
}

func Reject(
	current Case,
	command ResolveFailureCommand,
) (Case, bool, error) {
	return resolveUnsuccessful(current, StatusRejected, command)
}

func resolveUnsuccessful(
	current Case,
	status Status,
	command ResolveFailureCommand,
) (Case, bool, error) {
	if err := validateOperatorTransition(
		current,
		command.HandledBy,
		command.OperatorNote,
		command.At,
	); err != nil {
		return Case{}, false, err
	}
	failureReason := strings.TrimSpace(command.FailureReason)
	if failureReason == "" || failureReason != command.FailureReason ||
		len([]rune(failureReason)) > 500 {
		return Case{}, false, fmt.Errorf("%w: failure reason is invalid", ErrInvalidRefund)
	}

	if current.RefundStatus == status {
		if current.ResolvedAt == nil ||
			current.HandledBy == nil ||
			current.FailureReason == nil ||
			*current.HandledBy != command.HandledBy ||
			*current.FailureReason != failureReason ||
			!current.ResolvedAt.Equal(command.At) ||
			!optionalStringEqual(current.OperatorNote, command.OperatorNote) {
			return Case{}, false, ErrRefundReplayConflict
		}
		return cloneCase(current), false, nil
	}
	switch current.RefundStatus {
	case StatusPendingManual, StatusProcessing:
	case StatusFailed:
		if status == StatusRejected {
			break
		}
		return Case{}, false, ErrRefundReplayConflict
	case StatusRefunded, StatusRejected:
		return Case{}, false, ErrRefundTerminal
	default:
		return Case{}, false, fmt.Errorf("%w: unknown status", ErrInvalidRefund)
	}

	updated := cloneCase(current)
	resolvedAt := command.At.UTC()
	handledBy := command.HandledBy
	updated.RefundStatus = status
	updated.SuccessfulRefundCents = 0
	updated.ResolvedAt = &resolvedAt
	updated.HandledBy = &handledBy
	updated.ExternalRefundID = nil
	updated.EvidenceReference = nil
	updated.OperatorNote = optionalString(command.OperatorNote)
	updated.FailureReason = &failureReason
	updated.Version++
	updated.UpdatedAt = resolvedAt
	return updated, true, nil
}

func validateNewCaseCommand(command NewCaseCommand) error {
	switch {
	case command.TenantID == uuid.Nil:
	case command.OrderID == uuid.Nil:
	case command.RegistrationID == uuid.Nil:
	case command.SeriesID == uuid.Nil:
	case command.InstanceID == uuid.Nil:
	case command.SessionID == uuid.Nil:
	case command.PrincipalID == uuid.Nil:
	case !knownReasonCode(command.ReasonCode):
	case !refundIdempotencyKeyPattern.MatchString(command.IdempotencyKey):
	case command.ActualPaidCents <= 0:
	case command.RequestedRefundCents <= 0:
	case command.RequestedRefundCents > command.ActualPaidCents:
	case invalidOptionalText(command.OperatorNote, 1000):
	case command.Now.IsZero():
	default:
		return nil
	}
	return ErrInvalidRefund
}

func validateOperatorTransition(
	current Case,
	handledBy uuid.UUID,
	operatorNote string,
	at time.Time,
) error {
	switch {
	case current.CreatedAt.IsZero():
	case handledBy == uuid.Nil:
	case invalidOptionalText(operatorNote, 1000):
	case at.IsZero() || at.Before(current.CreatedAt):
	case !current.UpdatedAt.IsZero() && at.Before(current.UpdatedAt):
	case current.ProcessingStartedAt != nil && at.Before(*current.ProcessingStartedAt):
	default:
		return nil
	}
	return ErrInvalidRefund
}

func knownReasonCode(value ReasonCode) bool {
	switch value {
	case ReasonHoldExpiredAfterPayment,
		ReasonOrderClosedAfterPayment,
		ReasonSessionUnavailableAfterPayment,
		ReasonUserCancelled,
		ReasonSessionCancelled,
		ReasonInstanceCancelled,
		ReasonOperatorAdjustment:
		return true
	default:
		return false
	}
}

func invalidOptionalText(value string, maxRunes int) bool {
	return value != strings.TrimSpace(value) || len([]rune(value)) > maxRunes
}

func normalizeOptional(value string) string {
	return strings.TrimSpace(value)
}

func optionalString(value string) *string {
	normalized := normalizeOptional(value)
	if normalized == "" {
		return nil
	}
	return &normalized
}

func optionalStringEqual(recorded *string, candidate string) bool {
	normalized := normalizeOptional(candidate)
	if recorded == nil {
		return normalized == ""
	}
	return *recorded == normalized
}

func cloneCase(value Case) Case {
	cloned := value
	cloned.ProcessingStartedAt = cloneTime(value.ProcessingStartedAt)
	cloned.ResolvedAt = cloneTime(value.ResolvedAt)
	cloned.HandledBy = cloneUUID(value.HandledBy)
	cloned.ExternalRefundID = cloneString(value.ExternalRefundID)
	cloned.EvidenceReference = cloneString(value.EvidenceReference)
	cloned.OperatorNote = cloneString(value.OperatorNote)
	cloned.FailureReason = cloneString(value.FailureReason)
	return cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
