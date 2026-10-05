package coupon

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

type Status string

const (
	StatusAvailable   Status = `available`
	StatusHeld        Status = `held`
	StatusRedeemed    Status = `redeemed`
	StatusInvalidated Status = `invalidated`
	StatusExpired     Status = `expired`
)

type Projection struct {
	Instrument         Coupon
	Status             Status
	ActiveHold         *Entry
	TerminalEntry      *Entry
	RefundAdjustment   *Entry
	CorrectionRequired bool
}

type HoldCommand struct {
	Instrument         Coupon
	History            []Entry
	OrderID            uuid.UUID
	RegistrationID     uuid.UUID
	SeriesID           uuid.UUID
	ActivityType       activity.ActivityType
	OriginalPriceCents int64
	HoldExpiresAt      time.Time
	At                 time.Time
	RecordedAt         time.Time
}

type ReleaseCommand struct {
	Instrument Coupon
	History    []Entry
	OrderID    uuid.UUID
	Reason     string
	At         time.Time
	RecordedAt time.Time
}

type RedeemCommand struct {
	Instrument Coupon
	History    []Entry
	OrderID    uuid.UUID
	At         time.Time
	RecordedAt time.Time
}

type CheckinCorrectionCommand struct {
	Instrument            Coupon
	History               []Entry
	RevokedCheckinEventID uuid.UUID
	ActorID               uuid.UUID
	Reason                string
	At                    time.Time
	RecordedAt            time.Time
}

var (
	ErrInvalidLedger       = errors.New(`invalid xiangwan Coupon ledger`)
	ErrCouponUnavailable   = errors.New(`xiangwan Coupon is unavailable`)
	ErrCouponScopeMismatch = errors.New(`xiangwan Coupon scope mismatch`)
	ErrCouponOrderMismatch = errors.New(`xiangwan Coupon Order mismatch`)
	ErrCouponTerminal      = errors.New(`xiangwan Coupon is terminal`)
)

func Project(
	instrument Coupon,
	history []Entry,
	at time.Time,
) (Projection, error) {
	if ValidateCoupon(instrument) != nil || at.IsZero() ||
		len(history) == 0 {
		return Projection{}, ErrInvalidLedger
	}
	result := Projection{
		Instrument: instrument,
		Status:     StatusAvailable,
	}
	var previous Entry
	for index := range history {
		entry := history[index]
		if err := validateLedgerEntry(
			instrument,
			entry,
			int64(index+1),
			previous,
			at,
		); err != nil {
			return Projection{}, err
		}
		if index == 0 {
			if !entryMatchesGrant(instrument, entry) {
				return Projection{}, ErrInvalidLedger
			}
			previous = entry
			continue
		}
		switch entry.EntryType {
		case EntryTypeHeld:
			if result.Status != StatusAvailable {
				return Projection{}, ErrInvalidLedger
			}
			result.Status = StatusHeld
			result.ActiveHold = cloneEntryPointer(entry)
		case EntryTypeReleased:
			if !entryClosesActiveHold(entry, result.ActiveHold) {
				return Projection{}, ErrInvalidLedger
			}
			result.Status = StatusAvailable
			result.ActiveHold = nil
		case EntryTypeRedeemed:
			if !entryClosesActiveHold(entry, result.ActiveHold) {
				return Projection{}, ErrInvalidLedger
			}
			result.Status = StatusRedeemed
			result.TerminalEntry = cloneEntryPointer(entry)
			result.ActiveHold = nil
			result.RefundAdjustment = nil
		case EntryTypeRestored:
			if !entryClosesRedemption(entry, result.TerminalEntry) ||
				result.CorrectionRequired {
				return Projection{}, ErrInvalidLedger
			}
			result.Status = StatusAvailable
			result.TerminalEntry = nil
			result.RefundAdjustment = cloneEntryPointer(entry)
		case EntryTypeForfeited:
			if !entryClosesRedemption(entry, result.TerminalEntry) {
				return Projection{}, ErrInvalidLedger
			}
			result.Status = StatusRedeemed
			result.TerminalEntry = cloneEntryPointer(entry)
			result.RefundAdjustment = cloneEntryPointer(entry)
		case EntryTypeInvalidated:
			if result.Status != StatusAvailable {
				return Projection{}, ErrInvalidLedger
			}
			result.Status = StatusInvalidated
			result.TerminalEntry = cloneEntryPointer(entry)
		case EntryTypeCorrectionRequired:
			if result.CorrectionRequired {
				return Projection{}, ErrInvalidLedger
			}
			result.CorrectionRequired = true
		default:
			return Projection{}, ErrInvalidLedger
		}
		previous = entry
	}
	if result.Status == StatusAvailable && !at.Before(instrument.ExpiresAt) {
		result.Status = StatusExpired
	}
	return result, nil
}

func Hold(command HoldCommand) (Entry, error) {
	if command.OrderID == uuid.Nil || command.RegistrationID == uuid.Nil ||
		command.SeriesID == uuid.Nil ||
		command.OriginalPriceCents < command.Instrument.MinimumOrderCents ||
		command.At.IsZero() || command.RecordedAt.Before(command.At) ||
		!command.HoldExpiresAt.After(command.At) ||
		command.HoldExpiresAt.After(command.Instrument.ExpiresAt) {
		return Entry{}, ErrCouponOrderMismatch
	}
	projection, err := Project(command.Instrument, command.History, command.At)
	if err != nil {
		return Entry{}, err
	}
	if projection.Status != StatusAvailable || projection.CorrectionRequired {
		return Entry{}, ErrCouponUnavailable
	}
	if !scopeMatches(
		command.Instrument,
		command.SeriesID,
		command.ActivityType,
	) {
		return Entry{}, ErrCouponScopeMismatch
	}
	orderID := command.OrderID
	registrationID := command.RegistrationID
	return newLifecycleEntry(
		command.Instrument,
		command.History,
		EntryTypeHeld,
		`hold:`+orderID.String(),
		&orderID,
		&registrationID,
		nil,
		nil,
		nil,
		nil,
		command.At,
		command.RecordedAt,
	)
}

func Release(command ReleaseCommand) (Entry, error) {
	reason := strings.TrimSpace(command.Reason)
	if command.OrderID == uuid.Nil || !validNarrative(&reason) ||
		command.At.IsZero() || command.RecordedAt.Before(command.At) {
		return Entry{}, ErrCouponOrderMismatch
	}
	projection, err := Project(command.Instrument, command.History, command.At)
	if err != nil {
		return Entry{}, err
	}
	if projection.Status != StatusHeld || projection.ActiveHold == nil ||
		projection.ActiveHold.OrderID == nil ||
		*projection.ActiveHold.OrderID != command.OrderID ||
		projection.ActiveHold.RegistrationID == nil {
		return Entry{}, ErrCouponOrderMismatch
	}
	orderID := command.OrderID
	registrationID := *projection.ActiveHold.RegistrationID
	relatedID := projection.ActiveHold.ID
	return newLifecycleEntry(
		command.Instrument,
		command.History,
		EntryTypeReleased,
		`release:`+orderID.String(),
		&orderID,
		&registrationID,
		&relatedID,
		nil,
		nil,
		&reason,
		command.At,
		command.RecordedAt,
	)
}

func Redeem(command RedeemCommand) (Entry, error) {
	if command.OrderID == uuid.Nil || command.At.IsZero() ||
		command.RecordedAt.Before(command.At) {
		return Entry{}, ErrCouponOrderMismatch
	}
	projection, err := Project(command.Instrument, command.History, command.At)
	if err != nil {
		return Entry{}, err
	}
	if projection.Status != StatusHeld || projection.ActiveHold == nil ||
		projection.ActiveHold.OrderID == nil ||
		*projection.ActiveHold.OrderID != command.OrderID ||
		projection.ActiveHold.RegistrationID == nil {
		return Entry{}, ErrCouponOrderMismatch
	}
	orderID := command.OrderID
	registrationID := *projection.ActiveHold.RegistrationID
	relatedID := projection.ActiveHold.ID
	return newLifecycleEntry(
		command.Instrument,
		command.History,
		EntryTypeRedeemed,
		`redeem:`+orderID.String(),
		&orderID,
		&registrationID,
		&relatedID,
		nil,
		nil,
		nil,
		command.At,
		command.RecordedAt,
	)
}

func CorrectForRevokedCheckin(
	command CheckinCorrectionCommand,
) (Entry, error) {
	reason := strings.TrimSpace(command.Reason)
	if command.Instrument.GrantKind != GrantKindInitialGuest ||
		command.Instrument.SourceCheckinEvent == nil ||
		command.RevokedCheckinEventID == uuid.Nil ||
		command.ActorID == uuid.Nil || !validNarrative(&reason) ||
		command.At.IsZero() || command.RecordedAt.Before(command.At) {
		return Entry{}, ErrInvalidLedger
	}
	// Reconciliation can run after an Order releases a hold. Project all facts
	// known at this write while retaining the original revocation event time on
	// the new correction entry. The source time cannot exclude a later release.
	projection, err := Project(command.Instrument, command.History, command.RecordedAt)
	if err != nil {
		return Entry{}, err
	}
	if projection.CorrectionRequired &&
		(projection.Status == StatusHeld ||
			projection.Status == StatusRedeemed) {
		return Entry{}, ErrCouponTerminal
	}
	var entryType EntryType
	hasHistoricalRedemption := ledgerHasEntryType(
		command.History,
		EntryTypeRedeemed,
	)
	switch projection.Status {
	case StatusAvailable, StatusExpired:
		if hasHistoricalRedemption {
			entryType = EntryTypeCorrectionRequired
		} else {
			entryType = EntryTypeInvalidated
		}
	case StatusHeld, StatusRedeemed:
		entryType = EntryTypeCorrectionRequired
	case StatusInvalidated:
		return Entry{}, ErrCouponTerminal
	default:
		return Entry{}, ErrInvalidLedger
	}
	relatedID := command.History[0].ID
	sourceEventID := command.RevokedCheckinEventID
	actorID := command.ActorID
	return newLifecycleEntry(
		command.Instrument,
		command.History,
		entryType,
		`checkin-correction:`+sourceEventID.String(),
		nil,
		nil,
		&relatedID,
		&sourceEventID,
		&actorID,
		&reason,
		command.At,
		command.RecordedAt,
	)
}

func newLifecycleEntry(
	instrument Coupon,
	history []Entry,
	entryType EntryType,
	businessKey string,
	orderID *uuid.UUID,
	registrationID *uuid.UUID,
	relatedEntryID *uuid.UUID,
	sourceCheckinEventID *uuid.UUID,
	actorID *uuid.UUID,
	reason *string,
	at time.Time,
	recordedAt time.Time,
) (Entry, error) {
	value := Entry{
		ID:                   uuid.New(),
		TenantID:             instrument.TenantID,
		CouponID:             instrument.ID,
		PrincipalID:          instrument.PrincipalID,
		EntrySequence:        int64(len(history) + 1),
		EntryType:            entryType,
		BusinessKey:          businessKey,
		OrderID:              cloneUUID(orderID),
		RegistrationID:       cloneUUID(registrationID),
		RelatedEntryID:       cloneUUID(relatedEntryID),
		SourceCheckinEventID: cloneUUID(sourceCheckinEventID),
		ActorID:              cloneUUID(actorID),
		Reason:               cloneString(reason),
		OccurredAt:           at.UTC(),
		RecordedAt:           recordedAt.UTC(),
	}
	if ValidateEntry(value) != nil {
		return Entry{}, ErrInvalidLedger
	}
	return value, nil
}

func validateLedgerEntry(
	instrument Coupon,
	entry Entry,
	expectedSequence int64,
	previous Entry,
	at time.Time,
) error {
	if ValidateEntry(entry) != nil ||
		entry.TenantID != instrument.TenantID ||
		entry.CouponID != instrument.ID ||
		entry.PrincipalID != instrument.PrincipalID ||
		entry.EntrySequence != expectedSequence ||
		entry.OccurredAt.After(at) {
		return ErrInvalidLedger
	}
	if expectedSequence > 1 &&
		entry.RecordedAt.Before(previous.RecordedAt) {
		return ErrInvalidLedger
	}
	return nil
}

func entryMatchesGrant(instrument Coupon, entry Entry) bool {
	return entry.EntryType == EntryTypeGranted &&
		entry.BusinessKey == instrument.GrantBusinessKey &&
		entry.ActorID == nil == (instrument.GrantedBy == nil) &&
		(entry.ActorID == nil || *entry.ActorID == *instrument.GrantedBy) &&
		entry.OccurredAt.Equal(instrument.GrantedAt) &&
		entry.RecordedAt.Equal(instrument.CreatedAt)
}

func entryClosesActiveHold(entry Entry, activeHold *Entry) bool {
	return activeHold != nil && entry.RelatedEntryID != nil &&
		*entry.RelatedEntryID == activeHold.ID &&
		entry.OrderID != nil && activeHold.OrderID != nil &&
		*entry.OrderID == *activeHold.OrderID &&
		entry.RegistrationID != nil && activeHold.RegistrationID != nil &&
		*entry.RegistrationID == *activeHold.RegistrationID
}

func entryClosesRedemption(entry Entry, terminal *Entry) bool {
	return terminal != nil && terminal.EntryType == EntryTypeRedeemed &&
		entry.RelatedEntryID != nil &&
		*entry.RelatedEntryID == terminal.ID &&
		entry.OrderID != nil && terminal.OrderID != nil &&
		*entry.OrderID == *terminal.OrderID &&
		entry.RegistrationID != nil && terminal.RegistrationID != nil &&
		*entry.RegistrationID == *terminal.RegistrationID
}

func ledgerHasEntryType(entries []Entry, entryType EntryType) bool {
	for _, entry := range entries {
		if entry.EntryType == entryType {
			return true
		}
	}
	return false
}

func scopeMatches(
	instrument Coupon,
	seriesID uuid.UUID,
	activityType activity.ActivityType,
) bool {
	switch instrument.ScopeType {
	case ScopeTypeActivityType:
		return instrument.ScopeActivityType != nil &&
			*instrument.ScopeActivityType == activityType
	case ScopeTypeSeries:
		return instrument.ScopeSeriesID != nil &&
			*instrument.ScopeSeriesID == seriesID
	default:
		return false
	}
}

func cloneEntryPointer(value Entry) *Entry {
	cloned := value
	cloned.OrderID = cloneUUID(value.OrderID)
	cloned.RegistrationID = cloneUUID(value.RegistrationID)
	cloned.RelatedEntryID = cloneUUID(value.RelatedEntryID)
	cloned.RefundCaseID = cloneUUID(value.RefundCaseID)
	cloned.SourceCheckinEventID = cloneUUID(value.SourceCheckinEventID)
	cloned.ActorID = cloneUUID(value.ActorID)
	cloned.Reason = cloneString(value.Reason)
	cloned.RefundPolicyVersion = cloneString(value.RefundPolicyVersion)
	return &cloned
}

func (status Status) Available() bool {
	return status == StatusAvailable
}

func (status Status) String() string {
	return string(status)
}

func (projection Projection) String() string {
	return fmt.Sprintf(
		`CouponProjection{status:%s,correction_required:%t}`,
		projection.Status,
		projection.CorrectionRequired,
	)
}
