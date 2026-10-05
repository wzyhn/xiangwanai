package resource

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestSelectPastHighlightContextUsesLatestEligibleSameSeriesInstance(
	t *testing.T,
) {
	t.Parallel()

	seriesID := pastHighlightUUID(1)
	anchor := PastHighlightAnchor{
		SeriesID:    seriesID,
		InstanceID:  pastHighlightUUID(2),
		PublishedAt: pastHighlightTime(20),
	}
	older := pastHighlightCandidate(seriesID, 3, 8, true)
	latest := pastHighlightCandidate(seriesID, 4, 12, true)
	otherSeries := pastHighlightCandidate(pastHighlightUUID(5), 6, 15, true)
	withoutContent := pastHighlightCandidate(seriesID, 7, 18, false)
	notPrevious := pastHighlightCandidate(seriesID, 8, 21, true)

	got, err := SelectPastHighlightContext(anchor, []PastHighlightInstanceCandidate{
		otherSeries,
		withoutContent,
		older,
		notPrevious,
		latest,
	})
	if err != nil {
		t.Fatalf("SelectPastHighlightContext() error = %v", err)
	}
	if got == nil || got.SeriesID != seriesID ||
		got.AnchorInstanceID != anchor.InstanceID ||
		got.PreviousInstanceID != latest.InstanceID ||
		!got.PreviousInstancePublishedAt.Equal(latest.PublishedAt) ||
		!got.PreviousInstanceCompletedAt.Equal(latest.CompletedAt) {
		t.Fatalf("SelectPastHighlightContext() = %+v", got)
	}
}

func TestSelectPastHighlightContextRanksOnlyPublicSessionsDeterministically(
	t *testing.T,
) {
	t.Parallel()

	seriesID := pastHighlightUUID(10)
	candidate := pastHighlightCandidate(seriesID, 11, 12, false)
	candidate.Sessions = []PastHighlightSessionCandidate{
		pastHighlightSession(12, activity.DeliveryModeOnline, 99, 1, 1, true),
		pastHighlightSession(13, activity.DeliveryModeOffline, 4, 1, 1, false),
		pastHighlightSession(14, activity.DeliveryModeOffline, 5, 3, 1, true),
		pastHighlightSession(15, activity.DeliveryModeOffline, 5, 2, 5, true),
		pastHighlightSession(16, activity.DeliveryModeOffline, 5, 2, 1, true),
	}
	anchor := PastHighlightAnchor{
		SeriesID:    seriesID,
		InstanceID:  pastHighlightUUID(17),
		PublishedAt: pastHighlightTime(20),
	}

	got, err := SelectPastHighlightContext(
		anchor,
		[]PastHighlightInstanceCandidate{candidate},
	)
	if err != nil || got == nil || got.FeaturedSessionID == nil ||
		*got.FeaturedSessionID != pastHighlightUUID(16) {
		t.Fatalf("SelectPastHighlightContext() = %+v, %v", got, err)
	}
}

func TestSelectPastHighlightContextKeepsInstanceReviewContextWithoutSession(
	t *testing.T,
) {
	t.Parallel()

	seriesID := pastHighlightUUID(20)
	candidate := pastHighlightCandidate(seriesID, 21, 12, true)
	anchor := PastHighlightAnchor{
		SeriesID:    seriesID,
		InstanceID:  pastHighlightUUID(22),
		PublishedAt: pastHighlightTime(20),
	}

	got, err := SelectPastHighlightContext(
		anchor,
		[]PastHighlightInstanceCandidate{candidate},
	)
	if err != nil || got == nil || got.PreviousInstanceID != candidate.InstanceID ||
		got.FeaturedSessionID != nil {
		t.Fatalf("SelectPastHighlightContext() = %+v, %v", got, err)
	}
}

func TestSelectPastHighlightContextReturnsNoCrossSeriesFallback(t *testing.T) {
	t.Parallel()

	seriesID := pastHighlightUUID(30)
	anchor := PastHighlightAnchor{
		SeriesID:    seriesID,
		InstanceID:  pastHighlightUUID(31),
		PublishedAt: pastHighlightTime(20),
	}
	got, err := SelectPastHighlightContext(
		anchor,
		[]PastHighlightInstanceCandidate{
			pastHighlightCandidate(pastHighlightUUID(32), 33, 19, true),
		},
	)
	if err != nil || got != nil {
		t.Fatalf("SelectPastHighlightContext() = %+v, %v", got, err)
	}
}

func TestSelectPastHighlightContextRejectsInvalidOrDuplicateFacts(t *testing.T) {
	t.Parallel()

	seriesID := pastHighlightUUID(40)
	anchor := PastHighlightAnchor{
		SeriesID:    seriesID,
		InstanceID:  pastHighlightUUID(41),
		PublishedAt: pastHighlightTime(20),
	}
	candidate := pastHighlightCandidate(seriesID, 42, 12, true)
	candidate.Sessions = []PastHighlightSessionCandidate{
		pastHighlightSession(43, activity.DeliveryModeOffline, 1, 1, 1, true),
	}

	tests := []struct {
		name       string
		anchor     PastHighlightAnchor
		candidates []PastHighlightInstanceCandidate
	}{
		{name: "invalid anchor", anchor: PastHighlightAnchor{}},
		{name: "duplicate Instance", anchor: anchor, candidates: []PastHighlightInstanceCandidate{candidate, candidate}},
		{name: "duplicate Session", anchor: anchor, candidates: []PastHighlightInstanceCandidate{candidate, func() PastHighlightInstanceCandidate {
			other := pastHighlightCandidate(seriesID, 44, 10, true)
			other.Sessions = append([]PastHighlightSessionCandidate(nil), candidate.Sessions...)
			return other
		}()}},
		{name: "cancelled candidate", anchor: anchor, candidates: []PastHighlightInstanceCandidate{func() PastHighlightInstanceCandidate {
			invalid := candidate
			invalid.Status = activity.InstanceStatusCancelled
			return invalid
		}()}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := SelectPastHighlightContext(
				test.anchor,
				test.candidates,
			); !errors.Is(err, ErrInvalidPastHighlightFacts) {
				t.Fatalf("SelectPastHighlightContext() error = %v", err)
			}
		})
	}
}

func TestSelectPastHighlightContextDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	seriesID := pastHighlightUUID(50)
	candidates := []PastHighlightInstanceCandidate{
		pastHighlightCandidate(seriesID, 51, 8, true),
		pastHighlightCandidate(seriesID, 52, 12, true),
	}
	candidates[0].Sessions = []PastHighlightSessionCandidate{
		pastHighlightSession(53, activity.DeliveryModeOnline, 2, 2, 2, true),
		pastHighlightSession(54, activity.DeliveryModeOffline, 1, 1, 1, true),
	}
	want := append([]PastHighlightInstanceCandidate(nil), candidates...)
	want[0].Sessions = append(
		[]PastHighlightSessionCandidate(nil),
		candidates[0].Sessions...,
	)
	_, err := SelectPastHighlightContext(PastHighlightAnchor{
		SeriesID:    seriesID,
		InstanceID:  pastHighlightUUID(55),
		PublishedAt: pastHighlightTime(20),
	}, candidates)
	if err != nil || !reflect.DeepEqual(candidates, want) {
		t.Fatalf("SelectPastHighlightContext() mutated input: %+v, %v", candidates, err)
	}
}

func pastHighlightCandidate(
	seriesID uuid.UUID,
	instanceByte byte,
	publishedHour int,
	hasContent bool,
) PastHighlightInstanceCandidate {
	return PastHighlightInstanceCandidate{
		InstanceID:               pastHighlightUUID(instanceByte),
		SeriesID:                 seriesID,
		Status:                   activity.InstanceStatusCompleted,
		PublishedAt:              pastHighlightTime(publishedHour),
		CompletedAt:              pastHighlightTime(publishedHour + 1),
		HasInstancePublicContent: hasContent,
	}
}

func pastHighlightSession(
	idByte byte,
	deliveryMode activity.DeliveryMode,
	confirmedCount int,
	startHour int,
	sortOrder int,
	hasContent bool,
) PastHighlightSessionCandidate {
	return PastHighlightSessionCandidate{
		SessionID:                  pastHighlightUUID(idByte),
		DeliveryMode:               deliveryMode,
		ConfirmedRegistrationCount: confirmedCount,
		SessionStartAt:             pastHighlightTime(startHour),
		SortOrder:                  sortOrder,
		HasPublicContent:           hasContent,
	}
}

func pastHighlightUUID(lastByte byte) uuid.UUID {
	var value uuid.UUID
	value[len(value)-1] = lastByte
	return value
}

func pastHighlightTime(hour int) time.Time {
	return time.Date(2026, time.September, 14, hour, 0, 0, 0, time.UTC)
}
