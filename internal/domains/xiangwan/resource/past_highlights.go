package resource

import (
	"errors"
	"sort"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

type PastHighlightAnchor struct {
	SeriesID    uuid.UUID
	InstanceID  uuid.UUID
	PublishedAt time.Time
}

type PastHighlightSessionCandidate struct {
	SessionID                  uuid.UUID
	DeliveryMode               activity.DeliveryMode
	ConfirmedRegistrationCount int
	SessionStartAt             time.Time
	SortOrder                  int
	HasPublicContent           bool
}

type PastHighlightInstanceCandidate struct {
	InstanceID               uuid.UUID
	SeriesID                 uuid.UUID
	Status                   activity.InstanceStatus
	PublishedAt              time.Time
	CompletedAt              time.Time
	HasInstancePublicContent bool
	Sessions                 []PastHighlightSessionCandidate
}

type PastHighlightContext struct {
	SeriesID                    uuid.UUID
	AnchorInstanceID            uuid.UUID
	PreviousInstanceID          uuid.UUID
	FeaturedSessionID           *uuid.UUID
	PreviousInstancePublishedAt time.Time
	PreviousInstanceCompletedAt time.Time
}

var ErrInvalidPastHighlightFacts = errors.New(
	"invalid xiangwan past-highlight facts",
)

var ErrPastHighlightAnchorUnavailable = errors.New("xiangwan past-highlight anchor unavailable")

func SelectPastHighlightContext(
	anchor PastHighlightAnchor,
	candidates []PastHighlightInstanceCandidate,
) (*PastHighlightContext, error) {
	if anchor.SeriesID == uuid.Nil || anchor.InstanceID == uuid.Nil ||
		anchor.PublishedAt.IsZero() {
		return nil, ErrInvalidPastHighlightFacts
	}
	seenInstances := make(map[uuid.UUID]struct{}, len(candidates))
	seenSessions := make(map[uuid.UUID]struct{})
	eligible := make([]PastHighlightInstanceCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !validPastHighlightInstance(candidate) {
			return nil, ErrInvalidPastHighlightFacts
		}
		if _, duplicate := seenInstances[candidate.InstanceID]; duplicate {
			return nil, ErrInvalidPastHighlightFacts
		}
		seenInstances[candidate.InstanceID] = struct{}{}
		for _, session := range candidate.Sessions {
			if _, duplicate := seenSessions[session.SessionID]; duplicate {
				return nil, ErrInvalidPastHighlightFacts
			}
			seenSessions[session.SessionID] = struct{}{}
		}
		if candidate.SeriesID != anchor.SeriesID ||
			candidate.InstanceID == anchor.InstanceID ||
			!candidate.PublishedAt.Before(anchor.PublishedAt) ||
			!instanceHasPublicHighlightContent(candidate) {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return nil, nil
	}
	sort.SliceStable(eligible, func(left, right int) bool {
		if !eligible[left].PublishedAt.Equal(eligible[right].PublishedAt) {
			return eligible[left].PublishedAt.After(eligible[right].PublishedAt)
		}
		if !eligible[left].CompletedAt.Equal(eligible[right].CompletedAt) {
			return eligible[left].CompletedAt.After(eligible[right].CompletedAt)
		}
		return eligible[left].InstanceID.String() <
			eligible[right].InstanceID.String()
	})
	selected := eligible[0]
	context := &PastHighlightContext{
		SeriesID:                    anchor.SeriesID,
		AnchorInstanceID:            anchor.InstanceID,
		PreviousInstanceID:          selected.InstanceID,
		PreviousInstancePublishedAt: selected.PublishedAt.UTC(),
		PreviousInstanceCompletedAt: selected.CompletedAt.UTC(),
	}
	if sessionID := selectPastHighlightSession(selected.Sessions); sessionID != nil {
		context.FeaturedSessionID = sessionID
	}
	return context, nil
}

func validPastHighlightInstance(
	value PastHighlightInstanceCandidate,
) bool {
	if value.InstanceID == uuid.Nil || value.SeriesID == uuid.Nil ||
		value.PublishedAt.IsZero() || value.CompletedAt.IsZero() ||
		value.PublishedAt.After(value.CompletedAt) ||
		(value.Status != activity.InstanceStatusCompleted &&
			value.Status != activity.InstanceStatusArchived) {
		return false
	}
	for _, session := range value.Sessions {
		if session.SessionID == uuid.Nil || session.SessionStartAt.IsZero() ||
			session.ConfirmedRegistrationCount < 0 || session.SortOrder < 0 ||
			(session.DeliveryMode != activity.DeliveryModeOffline &&
				session.DeliveryMode != activity.DeliveryModeOnline) {
			return false
		}
	}
	return true
}

func instanceHasPublicHighlightContent(
	value PastHighlightInstanceCandidate,
) bool {
	if value.HasInstancePublicContent {
		return true
	}
	for _, session := range value.Sessions {
		if session.HasPublicContent {
			return true
		}
	}
	return false
}

func selectPastHighlightSession(
	candidates []PastHighlightSessionCandidate,
) *uuid.UUID {
	eligible := make([]PastHighlightSessionCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.HasPublicContent {
			eligible = append(eligible, candidate)
		}
	}
	if len(eligible) == 0 {
		return nil
	}
	sort.SliceStable(eligible, func(left, right int) bool {
		leftOffline := eligible[left].DeliveryMode == activity.DeliveryModeOffline
		rightOffline := eligible[right].DeliveryMode == activity.DeliveryModeOffline
		if leftOffline != rightOffline {
			return leftOffline
		}
		if eligible[left].ConfirmedRegistrationCount !=
			eligible[right].ConfirmedRegistrationCount {
			return eligible[left].ConfirmedRegistrationCount >
				eligible[right].ConfirmedRegistrationCount
		}
		if !eligible[left].SessionStartAt.Equal(eligible[right].SessionStartAt) {
			return eligible[left].SessionStartAt.Before(
				eligible[right].SessionStartAt,
			)
		}
		if eligible[left].SortOrder != eligible[right].SortOrder {
			return eligible[left].SortOrder < eligible[right].SortOrder
		}
		return eligible[left].SessionID.String() <
			eligible[right].SessionID.String()
	})
	selected := eligible[0].SessionID
	return &selected
}
