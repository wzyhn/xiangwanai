package activity

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestResolveNextInstanceRouteUsesEarliestLaterPublicInstance(t *testing.T) {
	t.Parallel()

	seriesID := nextRouteUUID(1)
	anchor := NextInstanceRouteAnchor{
		SeriesID:    seriesID,
		InstanceID:  nextRouteUUID(2),
		PublishedAt: nextRouteTime(10),
	}
	first := nextRouteCandidate(seriesID, 3, 12, nextRouteUUID(4))
	later := nextRouteCandidate(seriesID, 5, 14, nextRouteUUID(6))
	previous := nextRouteCandidate(seriesID, 7, 8, nextRouteUUID(8))
	otherSeries := nextRouteCandidate(nextRouteUUID(9), 10, 11, nextRouteUUID(11))

	got, err := ResolveNextInstanceRoute(anchor, []NextInstanceRouteCandidate{
		later,
		otherSeries,
		previous,
		first,
	})
	if err != nil || got.SeriesID != seriesID ||
		got.SourceInstanceID != anchor.InstanceID ||
		got.TargetInstanceID == nil || *got.TargetInstanceID != first.InstanceID ||
		got.SessionRoute.Kind != SessionRouteDirect ||
		got.SessionRoute.SessionID == nil ||
		*got.SessionRoute.SessionID != first.Sessions[0].SessionID {
		t.Fatalf("ResolveNextInstanceRoute() = %+v, %v", got, err)
	}
}

func TestResolveNextInstanceRouteNeverDefaultsMultipleSessions(t *testing.T) {
	t.Parallel()

	seriesID := nextRouteUUID(20)
	instanceID := nextRouteUUID(21)
	firstSessionID := nextRouteUUID(22)
	secondSessionID := nextRouteUUID(23)
	candidate := nextRouteCandidate(seriesID, 21, 12, secondSessionID)
	candidate.Sessions = append(candidate.Sessions, SessionRouteCandidate{
		SeriesID:       seriesID,
		InstanceID:     instanceID,
		SessionID:      firstSessionID,
		SessionStatus:  SessionStatusPublished,
		SessionStartAt: nextRouteTime(12),
		SortOrder:      1,
	})

	got, err := ResolveNextInstanceRoute(NextInstanceRouteAnchor{
		SeriesID:    seriesID,
		InstanceID:  nextRouteUUID(24),
		PublishedAt: nextRouteTime(10),
	}, []NextInstanceRouteCandidate{candidate})
	if err != nil || got.TargetInstanceID == nil ||
		*got.TargetInstanceID != instanceID ||
		got.SessionRoute.Kind != SessionRouteSelectionRequired ||
		got.SessionRoute.SessionID != nil ||
		!reflect.DeepEqual(
			got.SessionRoute.CandidateSessionIDs,
			[]uuid.UUID{firstSessionID, secondSessionID},
		) {
		t.Fatalf("ResolveNextInstanceRoute(many) = %+v, %v", got, err)
	}
}

func TestResolveNextInstanceRouteReturnsUnavailableWithoutTarget(t *testing.T) {
	t.Parallel()

	anchor := NextInstanceRouteAnchor{
		SeriesID:    nextRouteUUID(30),
		InstanceID:  nextRouteUUID(31),
		PublishedAt: nextRouteTime(10),
	}
	got, err := ResolveNextInstanceRoute(anchor, nil)
	if err != nil || got.TargetInstanceID != nil ||
		got.SessionRoute.Kind != SessionRouteUnavailable ||
		got.SessionRoute.SessionID != nil ||
		len(got.SessionRoute.CandidateSessionIDs) != 0 {
		t.Fatalf("ResolveNextInstanceRoute(empty) = %+v, %v", got, err)
	}
}

func TestResolveNextInstanceRouteDoesNotSkipAnUnroutableNextInstance(
	t *testing.T,
) {
	t.Parallel()

	seriesID := nextRouteUUID(35)
	anchor := NextInstanceRouteAnchor{
		SeriesID:    seriesID,
		InstanceID:  nextRouteUUID(36),
		PublishedAt: nextRouteTime(10),
	}
	immediate := NextInstanceRouteCandidate{
		SeriesID:    seriesID,
		InstanceID:  nextRouteUUID(37),
		Status:      InstanceStatusPublished,
		PublishedAt: nextRouteTime(11),
	}
	later := nextRouteCandidate(seriesID, 38, 12, nextRouteUUID(39))

	got, err := ResolveNextInstanceRoute(
		anchor,
		[]NextInstanceRouteCandidate{later, immediate},
	)
	if err != nil || got.TargetInstanceID != nil ||
		got.SessionRoute.Kind != SessionRouteUnavailable {
		t.Fatalf("ResolveNextInstanceRoute(unroutable next) = %+v, %v", got, err)
	}
}

func TestResolveNextInstanceRouteRejectsUnsafeFacts(t *testing.T) {
	t.Parallel()

	seriesID := nextRouteUUID(40)
	anchor := NextInstanceRouteAnchor{
		SeriesID:    seriesID,
		InstanceID:  nextRouteUUID(41),
		PublishedAt: nextRouteTime(10),
	}
	valid := nextRouteCandidate(seriesID, 42, 12, nextRouteUUID(43))
	tests := []struct {
		name       string
		anchor     NextInstanceRouteAnchor
		candidates []NextInstanceRouteCandidate
	}{
		{name: "invalid anchor", anchor: NextInstanceRouteAnchor{}},
		{name: "draft target", anchor: anchor, candidates: []NextInstanceRouteCandidate{func() NextInstanceRouteCandidate {
			candidate := valid
			candidate.Status = InstanceStatusDraft
			return candidate
		}()}},
		{name: "completed target", anchor: anchor, candidates: []NextInstanceRouteCandidate{func() NextInstanceRouteCandidate {
			candidate := valid
			candidate.Status = InstanceStatusCompleted
			return candidate
		}()}},
		{name: "cancelled target", anchor: anchor, candidates: []NextInstanceRouteCandidate{func() NextInstanceRouteCandidate {
			candidate := valid
			candidate.Status = InstanceStatusCancelled
			return candidate
		}()}},
		{name: "invalid Session", anchor: anchor, candidates: []NextInstanceRouteCandidate{func() NextInstanceRouteCandidate {
			candidate := valid
			candidate.Sessions[0].SessionStatus = SessionStatusDraft
			return candidate
		}()}},
		{name: "ended Session", anchor: anchor, candidates: []NextInstanceRouteCandidate{func() NextInstanceRouteCandidate {
			candidate := valid
			candidate.Sessions[0].SessionStatus = SessionStatusEnded
			return candidate
		}()}},
		{name: "cancelled Session", anchor: anchor, candidates: []NextInstanceRouteCandidate{func() NextInstanceRouteCandidate {
			candidate := valid
			candidate.Sessions[0].SessionStatus = SessionStatusCancelled
			return candidate
		}()}},
		{name: "duplicate Instance", anchor: anchor, candidates: []NextInstanceRouteCandidate{valid, valid}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ResolveNextInstanceRoute(
				test.anchor,
				test.candidates,
			); !errors.Is(err, ErrInvalidNextInstanceRouteFacts) {
				t.Fatalf("ResolveNextInstanceRoute() error = %v", err)
			}
		})
	}
}

func TestResolveNextInstanceRouteDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	seriesID := nextRouteUUID(50)
	candidates := []NextInstanceRouteCandidate{
		nextRouteCandidate(seriesID, 51, 14, nextRouteUUID(52)),
		nextRouteCandidate(seriesID, 53, 12, nextRouteUUID(54)),
	}
	want := append([]NextInstanceRouteCandidate(nil), candidates...)
	for index := range candidates {
		want[index].Sessions = append(
			[]SessionRouteCandidate(nil),
			candidates[index].Sessions...,
		)
	}
	_, err := ResolveNextInstanceRoute(NextInstanceRouteAnchor{
		SeriesID:    seriesID,
		InstanceID:  nextRouteUUID(55),
		PublishedAt: nextRouteTime(10),
	}, candidates)
	if err != nil || !reflect.DeepEqual(candidates, want) {
		t.Fatalf("ResolveNextInstanceRoute() mutated input: %+v, %v", candidates, err)
	}
}

func nextRouteCandidate(
	seriesID uuid.UUID,
	instanceByte byte,
	publishedHour int,
	sessionID uuid.UUID,
) NextInstanceRouteCandidate {
	instanceID := nextRouteUUID(instanceByte)
	return NextInstanceRouteCandidate{
		SeriesID:    seriesID,
		InstanceID:  instanceID,
		Status:      InstanceStatusPublished,
		PublishedAt: nextRouteTime(publishedHour),
		Sessions: []SessionRouteCandidate{
			{
				SeriesID:       seriesID,
				InstanceID:     instanceID,
				SessionID:      sessionID,
				SessionStatus:  SessionStatusPublished,
				SessionStartAt: nextRouteTime(publishedHour + 1),
				SortOrder:      2,
			},
		},
	}
}

func nextRouteUUID(lastByte byte) uuid.UUID {
	var value uuid.UUID
	value[len(value)-1] = lastByte
	return value
}

func nextRouteTime(hour int) time.Time {
	return time.Date(2026, time.September, 16, hour, 0, 0, 0, time.UTC)
}
