package activity

import (
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

type NextInstanceRouteAnchor struct {
	SeriesID    uuid.UUID
	InstanceID  uuid.UUID
	PublishedAt time.Time
}

type NextInstanceRouteCandidate struct {
	SeriesID    uuid.UUID
	InstanceID  uuid.UUID
	Status      InstanceStatus
	PublishedAt time.Time
	Sessions    []SessionRouteCandidate
}

type NextInstanceRouteResolution struct {
	SeriesID         uuid.UUID
	SourceInstanceID uuid.UUID
	TargetInstanceID *uuid.UUID
	SessionRoute     SessionRouteResolution
}

var ErrInvalidNextInstanceRouteFacts = errors.New(
	"invalid xiangwan next-Instance route facts",
)

// ResolveNextInstanceRoute selects the first later public Instance and then
// applies the global zero/one/many Session contract. It never chooses a
// Session when the target contains multiple public Sessions.
func ResolveNextInstanceRoute(
	anchor NextInstanceRouteAnchor,
	candidates []NextInstanceRouteCandidate,
) (NextInstanceRouteResolution, error) {
	if anchor.SeriesID == uuid.Nil || anchor.InstanceID == uuid.Nil ||
		anchor.PublishedAt.IsZero() {
		return NextInstanceRouteResolution{}, ErrInvalidNextInstanceRouteFacts
	}
	eligible := make([]NextInstanceRouteCandidate, 0, len(candidates))
	seenInstances := make(map[uuid.UUID]struct{}, len(candidates))
	seenSessions := make(map[uuid.UUID]struct{})
	for _, candidate := range candidates {
		if !validNextInstanceRouteCandidate(anchor, candidate) {
			return NextInstanceRouteResolution{}, ErrInvalidNextInstanceRouteFacts
		}
		if _, duplicate := seenInstances[candidate.InstanceID]; duplicate {
			return NextInstanceRouteResolution{}, ErrInvalidNextInstanceRouteFacts
		}
		seenInstances[candidate.InstanceID] = struct{}{}
		for _, session := range candidate.Sessions {
			if _, duplicate := seenSessions[session.SessionID]; duplicate {
				return NextInstanceRouteResolution{}, ErrInvalidNextInstanceRouteFacts
			}
			seenSessions[session.SessionID] = struct{}{}
		}
		if candidate.SeriesID == anchor.SeriesID &&
			candidate.InstanceID != anchor.InstanceID &&
			candidate.PublishedAt.After(anchor.PublishedAt) {
			eligible = append(eligible, candidate)
		}
	}
	base := NextInstanceRouteResolution{
		SeriesID:         anchor.SeriesID,
		SourceInstanceID: anchor.InstanceID,
		SessionRoute: SessionRouteResolution{
			Kind:                SessionRouteUnavailable,
			CandidateSessionIDs: []uuid.UUID{},
		},
	}
	if len(eligible) == 0 {
		return base, nil
	}
	sort.SliceStable(eligible, func(left, right int) bool {
		if !eligible[left].PublishedAt.Equal(eligible[right].PublishedAt) {
			return eligible[left].PublishedAt.Before(eligible[right].PublishedAt)
		}
		return eligible[left].InstanceID.String() <
			eligible[right].InstanceID.String()
	})
	selected := eligible[0]
	if len(selected.Sessions) == 0 {
		return base, nil
	}
	route, err := ResolveSessionRoute(selected.Sessions)
	if err != nil || route.Kind == SessionRouteUnavailable {
		return NextInstanceRouteResolution{}, ErrInvalidNextInstanceRouteFacts
	}
	base.TargetInstanceID = &selected.InstanceID
	base.SessionRoute = route
	return base, nil
}

func validNextInstanceRouteCandidate(
	anchor NextInstanceRouteAnchor,
	candidate NextInstanceRouteCandidate,
) bool {
	if candidate.SeriesID == uuid.Nil || candidate.InstanceID == uuid.Nil ||
		candidate.PublishedAt.IsZero() {
		return false
	}
	if candidate.Status != InstanceStatusPublished {
		return false
	}
	if candidate.SeriesID != anchor.SeriesID ||
		candidate.InstanceID == anchor.InstanceID ||
		!candidate.PublishedAt.After(anchor.PublishedAt) {
		return true
	}
	if len(candidate.Sessions) == 0 {
		return true
	}
	for _, session := range candidate.Sessions {
		if session.SessionStatus != SessionStatusPublished || session.ReviewOnly {
			return false
		}
	}
	_, err := ResolveSessionRoute(candidate.Sessions)
	return err == nil
}
