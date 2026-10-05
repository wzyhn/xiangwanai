package coupon

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var refundPolicyVersionPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`,
)

type RefundTrigger string

const (
	RefundTriggerFullCashRefund          RefundTrigger = `full_cash_refund`
	RefundTriggerSettledZeroCancellation RefundTrigger = `settled_zero_cancellation`
)

type RefundDisposition string

const (
	RefundDispositionRestore RefundDisposition = `restore`
	RefundDispositionForfeit RefundDisposition = `forfeit`
)

type RefundPolicyInput struct {
	TenantID       uuid.UUID
	CouponID       uuid.UUID
	OrderID        uuid.UUID
	RegistrationID uuid.UUID
	RefundCaseID   *uuid.UUID
	Trigger        RefundTrigger
	Reason         string
	EvaluatedAt    time.Time
}

type RefundPolicyDecision struct {
	Configured    bool
	PolicyVersion string
	Disposition   RefundDisposition
}

type RefundPolicyOrderState struct {
	Redemption Entry
	Adjustment *Entry
}

// RefundPolicyEvaluator must resolve CONFIG-COUPON-REFUND-POLICY from local
// or PostgreSQL-backed configuration. Callers hold financial locks and must
// never invoke a remote policy service through this port.
type RefundPolicyEvaluator interface {
	EvaluateCouponRefund(
		context.Context,
		RefundPolicyInput,
	) (RefundPolicyDecision, error)
}

type ApplyRefundPolicyCommand struct {
	Instrument Coupon
	History    []Entry
	Input      RefundPolicyInput
	Decision   RefundPolicyDecision
	ActorID    uuid.UUID
	RecordedAt time.Time
}

func ValidateRefundPolicyInput(value RefundPolicyInput) error {
	reason := strings.TrimSpace(value.Reason)
	if value.TenantID == uuid.Nil || value.CouponID == uuid.Nil ||
		value.OrderID == uuid.Nil || value.RegistrationID == uuid.Nil ||
		reason == `` || reason != value.Reason ||
		len([]rune(reason)) > MaxGrantNarrativeRunes ||
		value.EvaluatedAt.IsZero() {
		return ErrInvalidPolicy
	}
	switch value.Trigger {
	case RefundTriggerFullCashRefund:
		if value.RefundCaseID == nil || *value.RefundCaseID == uuid.Nil {
			return ErrInvalidPolicy
		}
	case RefundTriggerSettledZeroCancellation:
		if value.RefundCaseID != nil {
			return ErrInvalidPolicy
		}
	default:
		return ErrInvalidPolicy
	}
	return nil
}

func ValidateRefundPolicyDecision(value RefundPolicyDecision) error {
	if !value.Configured ||
		!refundPolicyVersionPattern.MatchString(value.PolicyVersion) {
		return ErrInvalidPolicy
	}
	switch value.Disposition {
	case RefundDispositionRestore, RefundDispositionForfeit:
		return nil
	default:
		return ErrInvalidPolicy
	}
}

// InspectRefundPolicyOrder returns the immutable redemption and any policy
// adjustment for one Order. It remains valid after a restored Coupon is reused,
// so command replay never depends on the Coupon's latest projection.
func InspectRefundPolicyOrder(
	instrument Coupon,
	history []Entry,
	orderID uuid.UUID,
	registrationID uuid.UUID,
) (RefundPolicyOrderState, error) {
	if ValidateCoupon(instrument) != nil || orderID == uuid.Nil ||
		registrationID == uuid.Nil || len(history) == 0 {
		return RefundPolicyOrderState{}, ErrCouponOrderMismatch
	}
	projectionAt := instrument.GrantedAt
	for _, entry := range history {
		if entry.OccurredAt.After(projectionAt) {
			projectionAt = entry.OccurredAt
		}
	}
	if _, err := Project(instrument, history, projectionAt); err != nil {
		return RefundPolicyOrderState{}, err
	}

	var redemption *Entry
	var adjustment *Entry
	released := false
	for _, entry := range history {
		if entry.OrderID == nil || *entry.OrderID != orderID ||
			entry.RegistrationID == nil ||
			*entry.RegistrationID != registrationID {
			continue
		}
		switch entry.EntryType {
		case EntryTypeReleased:
			released = true
		case EntryTypeRedeemed:
			if redemption != nil {
				return RefundPolicyOrderState{}, ErrInvalidLedger
			}
			redemption = cloneEntryPointer(entry)
		case EntryTypeRestored, EntryTypeForfeited:
			if adjustment != nil {
				return RefundPolicyOrderState{}, ErrInvalidLedger
			}
			adjustment = cloneEntryPointer(entry)
		}
	}
	if released || redemption == nil {
		return RefundPolicyOrderState{}, ErrCouponOrderMismatch
	}
	if adjustment != nil && (adjustment.RelatedEntryID == nil ||
		*adjustment.RelatedEntryID != redemption.ID) {
		return RefundPolicyOrderState{}, ErrInvalidLedger
	}
	return RefundPolicyOrderState{
		Redemption: *redemption,
		Adjustment: adjustment,
	}, nil
}

func ApplyRefundPolicy(command ApplyRefundPolicyCommand) (Entry, error) {
	if ValidateCoupon(command.Instrument) != nil ||
		ValidateRefundPolicyInput(command.Input) != nil ||
		ValidateRefundPolicyDecision(command.Decision) != nil ||
		command.ActorID == uuid.Nil || command.RecordedAt.IsZero() ||
		command.RecordedAt.Before(command.Input.EvaluatedAt) ||
		command.Input.TenantID != command.Instrument.TenantID ||
		command.Input.CouponID != command.Instrument.ID {
		return Entry{}, ErrInvalidPolicy
	}
	projection, err := Project(
		command.Instrument,
		command.History,
		command.Input.EvaluatedAt,
	)
	if err != nil {
		return Entry{}, err
	}
	redeemed := projection.TerminalEntry
	if projection.Status != StatusRedeemed || redeemed == nil ||
		redeemed.EntryType != EntryTypeRedeemed ||
		redeemed.OrderID == nil ||
		*redeemed.OrderID != command.Input.OrderID ||
		redeemed.RegistrationID == nil ||
		*redeemed.RegistrationID != command.Input.RegistrationID {
		return Entry{}, ErrCouponOrderMismatch
	}
	if command.Decision.Disposition == RefundDispositionRestore &&
		projection.CorrectionRequired {
		return Entry{}, ErrCouponUnavailable
	}

	entryType := EntryTypeRestored
	if command.Decision.Disposition == RefundDispositionForfeit {
		entryType = EntryTypeForfeited
	}
	orderID := command.Input.OrderID
	registrationID := command.Input.RegistrationID
	relatedEntryID := redeemed.ID
	actorID := command.ActorID
	reason := command.Input.Reason
	policyVersion := command.Decision.PolicyVersion
	value := Entry{
		ID:                  uuid.New(),
		TenantID:            command.Instrument.TenantID,
		CouponID:            command.Instrument.ID,
		PrincipalID:         command.Instrument.PrincipalID,
		EntrySequence:       int64(len(command.History) + 1),
		EntryType:           entryType,
		BusinessKey:         `refund-policy:` + redeemed.ID.String(),
		OrderID:             &orderID,
		RegistrationID:      &registrationID,
		RelatedEntryID:      &relatedEntryID,
		RefundCaseID:        cloneUUID(command.Input.RefundCaseID),
		ActorID:             &actorID,
		Reason:              &reason,
		RefundPolicyVersion: &policyVersion,
		OccurredAt:          command.Input.EvaluatedAt.UTC(),
		RecordedAt:          command.RecordedAt.UTC(),
	}
	if ValidateEntry(value) != nil {
		return Entry{}, ErrInvalidLedger
	}
	return value, nil
}
