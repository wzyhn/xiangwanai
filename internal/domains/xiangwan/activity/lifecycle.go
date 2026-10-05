package activity

import (
	"errors"
	"fmt"
)

// SessionTerminalFact is the only Session information needed to close an
// Instance. Ended is computed from the authoritative Session end instant;
// cancelled comes from an explicit cancellation fact.
type SessionTerminalFact string

const (
	SessionTerminalFactNonTerminal SessionTerminalFact = "non_terminal"
	SessionTerminalFactEnded       SessionTerminalFact = "ended"
	SessionTerminalFactCancelled   SessionTerminalFact = "cancelled"
)

// InstanceClosure is a terminal transition derived from every Session in one
// Instance. None means the Instance cannot close yet.
type InstanceClosure string

const (
	InstanceClosureNone      InstanceClosure = "none"
	InstanceClosureCompleted InstanceClosure = "completed"
	InstanceClosureCancelled InstanceClosure = "cancelled"
)

// InstanceClosureDecision contains the aggregate counts used to make a single,
// auditable terminal transition.
type InstanceClosureDecision struct {
	Closure        InstanceClosure
	SessionCount   int
	EndedCount     int
	CancelledCount int
}

// ShouldTransition reports whether the aggregate is ready for one terminal
// Instance transition.
func (decision InstanceClosureDecision) ShouldTransition() bool {
	return decision.Closure == InstanceClosureCompleted || decision.Closure == InstanceClosureCancelled
}

var (
	// ErrInstanceHasNoSessions rejects an impossible published Instance aggregate.
	ErrInstanceHasNoSessions = errors.New("instance has no sessions")
	// ErrUnknownSessionTerminalFact rejects unrecognized adapter input.
	ErrUnknownSessionTerminalFact = errors.New("unknown session terminal fact")
	// ErrNegativePublishedInstanceCount rejects a corrupt Series projection.
	ErrNegativePublishedInstanceCount = errors.New("published instance count must be non-negative")
)

// DecideInstanceClosure aggregates all Session terminal facts. Order has no
// effect: all cancelled closes the Instance as cancelled; at least one ended
// with every other Session ended or cancelled closes it as completed.
func DecideInstanceClosure(sessions []SessionTerminalFact) (InstanceClosureDecision, error) {
	decision := InstanceClosureDecision{
		Closure:      InstanceClosureNone,
		SessionCount: len(sessions),
	}
	if len(sessions) == 0 {
		return InstanceClosureDecision{}, ErrInstanceHasNoSessions
	}

	nonTerminalCount := 0
	for index, session := range sessions {
		switch session {
		case SessionTerminalFactNonTerminal:
			nonTerminalCount++
		case SessionTerminalFactEnded:
			decision.EndedCount++
		case SessionTerminalFactCancelled:
			decision.CancelledCount++
		default:
			return InstanceClosureDecision{}, fmt.Errorf(
				"%w at sessions[%d]: %q",
				ErrUnknownSessionTerminalFact,
				index,
				session,
			)
		}
	}

	switch {
	case decision.CancelledCount == decision.SessionCount:
		decision.Closure = InstanceClosureCancelled
	case nonTerminalCount == 0 && decision.EndedCount > 0:
		decision.Closure = InstanceClosureCompleted
	}

	return decision, nil
}

// DecideSeriesRecurring applies the irreversible Series recurrence rule. Once
// recurring, a Series remains recurring even if a damaged read model reports
// fewer publication facts; adapters should still surface that inconsistency.
func DecideSeriesRecurring(alreadyRecurring bool, successfulPublishedInstanceCount int) (bool, error) {
	if successfulPublishedInstanceCount < 0 {
		return false, ErrNegativePublishedInstanceCount
	}
	return alreadyRecurring || successfulPublishedInstanceCount >= 2, nil
}

// ShouldDisplayRecurringGap decides the aggregate gap projection. A completed
// latest Instance proves at least one Session was held; an all-cancelled
// Instance therefore never becomes a gap. Any later published Instance also
// closes the gap immediately.
func ShouldDisplayRecurringGap(
	seriesRecurring bool,
	latestPublicInstanceClosure InstanceClosure,
	hasLaterPublishedInstance bool,
) bool {
	return seriesRecurring &&
		latestPublicInstanceClosure == InstanceClosureCompleted &&
		!hasLaterPublishedInstance
}
