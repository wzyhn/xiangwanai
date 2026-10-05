package activity

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestResolveSessionRouteImplementsZeroOneManyContract(t *testing.T) {
	t.Parallel()

	seriesID := uuid.New()
	instanceID := uuid.New()
	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	firstID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	secondID := uuid.MustParse("00000000-0000-0000-0000-000000000002")
	thirdID := uuid.MustParse("00000000-0000-0000-0000-000000000003")
	candidate := func(id uuid.UUID, start time.Time, sortOrder int) SessionRouteCandidate {
		return SessionRouteCandidate{
			SeriesID:       seriesID,
			InstanceID:     instanceID,
			SessionID:      id,
			SessionStatus:  SessionStatusPublished,
			SessionStartAt: start,
			SortOrder:      sortOrder,
		}
	}

	empty, err := ResolveSessionRoute(nil)
	if err != nil {
		t.Fatalf("ResolveSessionRoute(empty) error = %v", err)
	}
	if empty.Kind != SessionRouteUnavailable || empty.SessionID != nil || len(empty.CandidateSessionIDs) != 0 {
		t.Fatalf("ResolveSessionRoute(empty) = %+v", empty)
	}

	archived := candidate(firstID, now, 0)
	archived.SessionStatus = SessionStatusArchived
	archived.ReviewOnly = true
	direct, err := ResolveSessionRoute([]SessionRouteCandidate{archived})
	if err != nil {
		t.Fatalf("ResolveSessionRoute(one) error = %v", err)
	}
	if direct.Kind != SessionRouteDirect || direct.SessionID == nil || *direct.SessionID != firstID || len(direct.CandidateSessionIDs) != 0 {
		t.Fatalf("ResolveSessionRoute(one) = %+v", direct)
	}

	input := []SessionRouteCandidate{
		candidate(thirdID, now.Add(time.Hour), 0),
		candidate(secondID, now, 2),
		candidate(firstID, now, 1),
	}
	original := append([]SessionRouteCandidate(nil), input...)
	selection, err := ResolveSessionRoute(input)
	if err != nil {
		t.Fatalf("ResolveSessionRoute(many) error = %v", err)
	}
	if selection.Kind != SessionRouteSelectionRequired ||
		selection.SessionID != nil ||
		!reflect.DeepEqual(selection.CandidateSessionIDs, []uuid.UUID{firstID, secondID, thirdID}) {
		t.Fatalf("ResolveSessionRoute(many) = %+v", selection)
	}
	if !reflect.DeepEqual(input, original) {
		t.Fatalf("ResolveSessionRoute mutated input: %+v", input)
	}
}

func TestResolveSessionRouteRejectsMixedOrMalformedCandidates(t *testing.T) {
	t.Parallel()

	now := time.Now()
	valid := SessionRouteCandidate{
		SeriesID:       uuid.New(),
		InstanceID:     uuid.New(),
		SessionID:      uuid.New(),
		SessionStatus:  SessionStatusPublished,
		SessionStartAt: now,
	}
	tests := []struct {
		name   string
		mutate func(*SessionRouteCandidate)
	}{
		{name: "missing Session", mutate: func(candidate *SessionRouteCandidate) { candidate.SessionID = uuid.Nil }},
		{name: "draft", mutate: func(candidate *SessionRouteCandidate) { candidate.SessionStatus = SessionStatusDraft }},
		{name: "archived detail target", mutate: func(candidate *SessionRouteCandidate) { candidate.SessionStatus = SessionStatusArchived }},
		{name: "published review target", mutate: func(candidate *SessionRouteCandidate) { candidate.ReviewOnly = true }},
		{name: "missing start", mutate: func(candidate *SessionRouteCandidate) { candidate.SessionStartAt = time.Time{} }},
		{name: "negative sort", mutate: func(candidate *SessionRouteCandidate) { candidate.SortOrder = -1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			test.mutate(&candidate)
			if _, err := ResolveSessionRoute([]SessionRouteCandidate{candidate}); !errors.Is(err, ErrInvalidSessionRouteCandidates) {
				t.Fatalf("ResolveSessionRoute() error = %v", err)
			}
		})
	}

	mixedInstance := valid
	mixedInstance.SessionID = uuid.New()
	mixedInstance.InstanceID = uuid.New()
	if _, err := ResolveSessionRoute([]SessionRouteCandidate{valid, mixedInstance}); !errors.Is(err, ErrInvalidSessionRouteCandidates) {
		t.Fatalf("mixed Instance error = %v", err)
	}
	mixedSeries := valid
	mixedSeries.SessionID = uuid.New()
	mixedSeries.SeriesID = uuid.New()
	if _, err := ResolveSessionRoute([]SessionRouteCandidate{valid, mixedSeries}); !errors.Is(err, ErrInvalidSessionRouteCandidates) {
		t.Fatalf("mixed Series error = %v", err)
	}
	if _, err := ResolveSessionRoute([]SessionRouteCandidate{valid, valid}); !errors.Is(err, ErrInvalidSessionRouteCandidates) {
		t.Fatalf("duplicate Session error = %v", err)
	}
}
