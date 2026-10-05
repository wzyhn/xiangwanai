package activity

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

type SessionRouteCandidate struct {
	SeriesID       uuid.UUID
	InstanceID     uuid.UUID
	SessionID      uuid.UUID
	SessionTitle   string
	SessionStatus  SessionStatus
	ReviewOnly     bool
	SessionStartAt time.Time
	SortOrder      int
}

type SessionRouteResolutionKind string

const (
	SessionRouteUnavailable       SessionRouteResolutionKind = "unavailable"
	SessionRouteDirect            SessionRouteResolutionKind = "session_detail"
	SessionRouteSelectionRequired SessionRouteResolutionKind = "session_selection_required"
)

type SessionRouteResolution struct {
	Kind                SessionRouteResolutionKind
	SessionID           *uuid.UUID
	CandidateSessionIDs []uuid.UUID
}

var ErrInvalidSessionRouteCandidates = errors.New("invalid xiangwan Session route candidates")

// ResolveSessionRoute enforces the global unique-Session routing contract. It
// never chooses a default when multiple public Sessions exist.
func ResolveSessionRoute(candidates []SessionRouteCandidate) (SessionRouteResolution, error) {
	if len(candidates) == 0 {
		return SessionRouteResolution{
			Kind:                SessionRouteUnavailable,
			CandidateSessionIDs: []uuid.UUID{},
		}, nil
	}

	ordered := append([]SessionRouteCandidate(nil), candidates...)
	if err := validateSessionRouteCandidates(ordered); err != nil {
		return SessionRouteResolution{}, err
	}
	sort.Slice(ordered, func(left int, right int) bool {
		if !ordered[left].SessionStartAt.Equal(ordered[right].SessionStartAt) {
			return ordered[left].SessionStartAt.Before(ordered[right].SessionStartAt)
		}
		if ordered[left].SortOrder != ordered[right].SortOrder {
			return ordered[left].SortOrder < ordered[right].SortOrder
		}
		return ordered[left].SessionID.String() < ordered[right].SessionID.String()
	})

	if len(ordered) == 1 {
		sessionID := ordered[0].SessionID
		return SessionRouteResolution{
			Kind:                SessionRouteDirect,
			SessionID:           &sessionID,
			CandidateSessionIDs: []uuid.UUID{},
		}, nil
	}

	ids := make([]uuid.UUID, 0, len(ordered))
	for _, candidate := range ordered {
		ids = append(ids, candidate.SessionID)
	}
	return SessionRouteResolution{
		Kind:                SessionRouteSelectionRequired,
		CandidateSessionIDs: ids,
	}, nil
}

func validateSessionRouteCandidates(candidates []SessionRouteCandidate) error {
	seriesID := candidates[0].SeriesID
	instanceID := candidates[0].InstanceID
	seenSessionIDs := make(map[uuid.UUID]struct{}, len(candidates))
	for index, candidate := range candidates {
		switch {
		case candidate.SeriesID == uuid.Nil:
			return fmt.Errorf("%w: candidates[%d].series_id is required", ErrInvalidSessionRouteCandidates, index)
		case candidate.InstanceID == uuid.Nil:
			return fmt.Errorf("%w: candidates[%d].instance_id is required", ErrInvalidSessionRouteCandidates, index)
		case candidate.SessionID == uuid.Nil:
			return fmt.Errorf("%w: candidates[%d].session_id is required", ErrInvalidSessionRouteCandidates, index)
		case candidate.SeriesID != seriesID:
			return fmt.Errorf("%w: candidates span Series", ErrInvalidSessionRouteCandidates)
		case candidate.InstanceID != instanceID:
			return fmt.Errorf("%w: candidates span Instances", ErrInvalidSessionRouteCandidates)
		case candidate.SessionStartAt.IsZero():
			return fmt.Errorf("%w: candidates[%d].session_start_at is required", ErrInvalidSessionRouteCandidates, index)
		case candidate.SortOrder < 0:
			return fmt.Errorf("%w: candidates[%d].sort_order is negative", ErrInvalidSessionRouteCandidates, index)
		}
		switch candidate.SessionStatus {
		case SessionStatusPublished, SessionStatusEnded, SessionStatusCancelled, SessionStatusArchived:
		default:
			return fmt.Errorf("%w: candidates[%d] is not public", ErrInvalidSessionRouteCandidates, index)
		}
		if candidate.ReviewOnly &&
			candidate.SessionStatus != SessionStatusEnded &&
			candidate.SessionStatus != SessionStatusArchived {
			return fmt.Errorf("%w: candidates[%d] review target is unavailable", ErrInvalidSessionRouteCandidates, index)
		}
		if !candidate.ReviewOnly && candidate.SessionStatus == SessionStatusArchived {
			return fmt.Errorf("%w: candidates[%d] detail target is unavailable", ErrInvalidSessionRouteCandidates, index)
		}
		if _, duplicate := seenSessionIDs[candidate.SessionID]; duplicate {
			return fmt.Errorf("%w: duplicate Session", ErrInvalidSessionRouteCandidates)
		}
		seenSessionIDs[candidate.SessionID] = struct{}{}
	}
	return nil
}
