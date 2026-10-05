// Package activity owns Xiangwan Activity Series, Instance, and Session domain
// decisions. It deliberately has no infrastructure dependencies.
package activity

import (
	"errors"
	"strings"
	"time"
)

// DisplayState is the computed public state of a Session card or detail view.
// It is a projection from trusted facts and must not be persisted as an
// operator-editable lifecycle state.
type DisplayState string

const (
	DisplayStateCancelled             DisplayState = "cancelled"
	DisplayStateRecurringGap          DisplayState = "recurring_gap"
	DisplayStateEnded                 DisplayState = "ended"
	DisplayStateInProgress            DisplayState = "in_progress"
	DisplayStateNotOpen               DisplayState = "not_open"
	DisplayStateClosed                DisplayState = "closed"
	DisplayStateFull                  DisplayState = "full"
	DisplayStateTemporarilyLockedFull DisplayState = "temporarily_locked_full"
	DisplayStateOpenLowStock          DisplayState = "open_low_stock"
	DisplayStateOpenNeedGroup         DisplayState = "open_need_group"
	DisplayStateOpen                  DisplayState = "open"
)

// HomeGroup is the Canonical homepage ordering group for a display state.
type HomeGroup uint8

const (
	HomeGroupOpen HomeGroup = iota + 1
	HomeGroupFutureUnavailable
	HomeGroupInProgress
	HomeGroupRecurringGap
	HomeGroupEnded
	HomeGroupCancelled
)

// RegistrationAllowed reports whether the state permits starting a new
// registration. Payment and questionnaire checks still run at command time.
func (state DisplayState) RegistrationAllowed() bool {
	switch state {
	case DisplayStateOpen, DisplayStateOpenLowStock, DisplayStateOpenNeedGroup:
		return true
	default:
		return false
	}
}

// HomeGroup returns the Canonical homepage group for a known display state.
func (state DisplayState) HomeGroup() (HomeGroup, bool) {
	switch state {
	case DisplayStateOpen, DisplayStateOpenLowStock, DisplayStateOpenNeedGroup:
		return HomeGroupOpen, true
	case DisplayStateTemporarilyLockedFull, DisplayStateNotOpen, DisplayStateFull, DisplayStateClosed:
		return HomeGroupFutureUnavailable, true
	case DisplayStateInProgress:
		return HomeGroupInProgress, true
	case DisplayStateRecurringGap:
		return HomeGroupRecurringGap, true
	case DisplayStateEnded:
		return HomeGroupEnded, true
	case DisplayStateCancelled:
		return HomeGroupCancelled, true
	default:
		return 0, false
	}
}

// SessionDisplayFacts are the authoritative inputs used to decide a Session's
// public display state. SeriesInRecurringGap is an aggregate fact; it is not a
// Session lifecycle flag.
type SessionDisplayFacts struct {
	InstanceCancelled    bool
	SessionCancelled     bool
	SeriesInRecurringGap bool

	Now                 time.Time
	RegistrationStartAt time.Time
	RegistrationEndAt   time.Time
	SessionStartAt      time.Time
	SessionEndAt        time.Time

	Capacity                   int
	ConfirmedRegistrationCount int
	ActiveHoldCount            int
	GroupMinimum               int
	LowStockThreshold          *int
}

// SessionDisplayDecision contains both the display state and the capacity
// values needed by response adapters. ConfirmedRegistrationCount is the N in
// N/M; active holds affect SellableCapacity but never that confirmed count.
type SessionDisplayDecision struct {
	State                      DisplayState
	Capacity                   int
	ConfirmedRegistrationCount int
	SellableCapacity           int
	NeededToReachGroupMinimum  int
}

// ViolationCode is a stable, transport-neutral invalid-fact code.
type ViolationCode string

const (
	ViolationRequired                ViolationCode = "required"
	ViolationMustBePositive          ViolationCode = "must_be_positive"
	ViolationMustBeNonNegative       ViolationCode = "must_be_non_negative"
	ViolationMustBeBefore            ViolationCode = "must_be_before"
	ViolationMustNotBeAfter          ViolationCode = "must_not_be_after"
	ViolationExceedsCapacity         ViolationCode = "exceeds_capacity"
	ViolationExceedsSellableCapacity ViolationCode = "exceeds_sellable_capacity"
	ViolationMustBeLessThanCapacity  ViolationCode = "must_be_less_than_capacity"
	ViolationMustNotBeBlank          ViolationCode = "must_not_be_blank"
	ViolationInvalidChoice           ViolationCode = "invalid_choice"
	ViolationOutOfRange              ViolationCode = "out_of_range"
	ViolationNotReady                ViolationCode = "not_ready"
	ViolationDuplicate               ViolationCode = "duplicate"
)

// FactViolation identifies one invalid authoritative input without coupling the
// domain package to an HTTP error schema.
type FactViolation struct {
	Field string
	Code  ViolationCode
}

// ErrInvalidSessionDisplayFacts supports errors.Is checks at adapter boundaries.
var ErrInvalidSessionDisplayFacts = errors.New("invalid session display facts")

// InvalidSessionDisplayFactsError preserves every validation failure in stable
// field order so admin and publication adapters can return deterministic errors.
type InvalidSessionDisplayFactsError struct {
	Violations []FactViolation
}

func (err *InvalidSessionDisplayFactsError) Error() string {
	if err == nil || len(err.Violations) == 0 {
		return ErrInvalidSessionDisplayFacts.Error()
	}

	parts := make([]string, 0, len(err.Violations))
	for _, violation := range err.Violations {
		parts = append(parts, violation.Field+":"+string(violation.Code))
	}
	return ErrInvalidSessionDisplayFacts.Error() + ": " + strings.Join(parts, ", ")
}

// Unwrap makes InvalidSessionDisplayFactsError compatible with errors.Is.
func (*InvalidSessionDisplayFactsError) Unwrap() error {
	return ErrInvalidSessionDisplayFacts
}

// ValidateSessionDisplayFacts checks publication-time ordering and the
// PostgreSQL capacity invariant without consulting a clock or infrastructure.
// Violations are returned in deterministic field order.
func ValidateSessionDisplayFacts(facts SessionDisplayFacts) []FactViolation {
	violations := make([]FactViolation, 0)

	violations = appendRequiredTime(violations, "now", facts.Now)
	violations = append(violations, validateSessionDefinition(
		facts.RegistrationStartAt,
		facts.RegistrationEndAt,
		facts.SessionStartAt,
		facts.SessionEndAt,
		facts.Capacity,
		facts.GroupMinimum,
		facts.LowStockThreshold,
	)...)

	if facts.ConfirmedRegistrationCount < 0 {
		violations = append(violations, FactViolation{Field: "confirmed_registration_count", Code: ViolationMustBeNonNegative})
	}
	if facts.ActiveHoldCount < 0 {
		violations = append(violations, FactViolation{Field: "active_hold_count", Code: ViolationMustBeNonNegative})
	}
	if facts.Capacity > 0 && facts.ConfirmedRegistrationCount > facts.Capacity {
		violations = append(violations, FactViolation{Field: "confirmed_registration_count", Code: ViolationExceedsCapacity})
	}
	if facts.Capacity > 0 && facts.ConfirmedRegistrationCount >= 0 &&
		facts.ConfirmedRegistrationCount <= facts.Capacity && facts.ActiveHoldCount >= 0 &&
		facts.ActiveHoldCount > facts.Capacity-facts.ConfirmedRegistrationCount {
		violations = append(violations, FactViolation{Field: "active_hold_count", Code: ViolationExceedsSellableCapacity})
	}

	return violations
}

func validateSessionDefinition(
	registrationStartAt time.Time,
	registrationEndAt time.Time,
	sessionStartAt time.Time,
	sessionEndAt time.Time,
	capacity int,
	groupMinimum int,
	lowStockThreshold *int,
) []FactViolation {
	violations := make([]FactViolation, 0)
	violations = appendRequiredTime(violations, "registration_start_at", registrationStartAt)
	violations = appendRequiredTime(violations, "registration_end_at", registrationEndAt)
	violations = appendRequiredTime(violations, "session_start_at", sessionStartAt)
	violations = appendRequiredTime(violations, "session_end_at", sessionEndAt)

	if !registrationStartAt.IsZero() && !registrationEndAt.IsZero() &&
		!registrationStartAt.Before(registrationEndAt) {
		violations = append(violations, FactViolation{Field: "registration_start_at", Code: ViolationMustBeBefore})
	}
	if !registrationEndAt.IsZero() && !sessionStartAt.IsZero() && registrationEndAt.After(sessionStartAt) {
		violations = append(violations, FactViolation{Field: "registration_end_at", Code: ViolationMustNotBeAfter})
	}
	if !sessionStartAt.IsZero() && !sessionEndAt.IsZero() && !sessionStartAt.Before(sessionEndAt) {
		violations = append(violations, FactViolation{Field: "session_start_at", Code: ViolationMustBeBefore})
	}

	if capacity <= 0 {
		violations = append(violations, FactViolation{Field: "capacity", Code: ViolationMustBePositive})
	}
	if groupMinimum <= 0 {
		violations = append(violations, FactViolation{Field: "group_minimum", Code: ViolationMustBePositive})
	} else if capacity > 0 && groupMinimum > capacity {
		violations = append(violations, FactViolation{Field: "group_minimum", Code: ViolationExceedsCapacity})
	}
	if lowStockThreshold != nil {
		switch {
		case *lowStockThreshold <= 0:
			violations = append(violations, FactViolation{Field: "low_stock_threshold", Code: ViolationMustBePositive})
		case capacity > 0 && *lowStockThreshold >= capacity:
			violations = append(violations, FactViolation{Field: "low_stock_threshold", Code: ViolationMustBeLessThanCapacity})
		}
	}
	return violations
}

func appendRequiredTime(violations []FactViolation, field string, value time.Time) []FactViolation {
	if value.IsZero() {
		return append(violations, FactViolation{Field: field, Code: ViolationRequired})
	}
	return violations
}

// DecideSessionDisplay applies the Canonical precedence to validated facts:
// cancelled, recurring gap, ended, in progress, time window, capacity, then
// the open sub-states.
func DecideSessionDisplay(facts SessionDisplayFacts) (SessionDisplayDecision, error) {
	if violations := ValidateSessionDisplayFacts(facts); len(violations) > 0 {
		return SessionDisplayDecision{}, &InvalidSessionDisplayFactsError{Violations: violations}
	}

	sellable := facts.Capacity - facts.ConfirmedRegistrationCount - facts.ActiveHoldCount
	neededForGroup := facts.GroupMinimum - facts.ConfirmedRegistrationCount
	if neededForGroup < 0 {
		neededForGroup = 0
	}

	decision := SessionDisplayDecision{
		Capacity:                   facts.Capacity,
		ConfirmedRegistrationCount: facts.ConfirmedRegistrationCount,
		SellableCapacity:           sellable,
		NeededToReachGroupMinimum:  neededForGroup,
	}

	switch {
	case facts.InstanceCancelled || facts.SessionCancelled:
		decision.State = DisplayStateCancelled
	case facts.SeriesInRecurringGap:
		decision.State = DisplayStateRecurringGap
	case !facts.Now.Before(facts.SessionEndAt):
		decision.State = DisplayStateEnded
	case !facts.Now.Before(facts.SessionStartAt):
		decision.State = DisplayStateInProgress
	case facts.Now.Before(facts.RegistrationStartAt):
		decision.State = DisplayStateNotOpen
	case !facts.Now.Before(facts.RegistrationEndAt):
		decision.State = DisplayStateClosed
	case facts.ConfirmedRegistrationCount >= facts.Capacity:
		decision.State = DisplayStateFull
	case sellable == 0:
		decision.State = DisplayStateTemporarilyLockedFull
	case facts.LowStockThreshold != nil && sellable <= *facts.LowStockThreshold:
		decision.State = DisplayStateOpenLowStock
	case facts.ConfirmedRegistrationCount < facts.GroupMinimum:
		decision.State = DisplayStateOpenNeedGroup
	default:
		decision.State = DisplayStateOpen
	}

	return decision, nil
}
