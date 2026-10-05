package activity

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidatePastActivityItemAcceptsCompletedAndArchivedHistory(t *testing.T) {
	t.Parallel()

	for _, status := range []InstanceStatus{
		InstanceStatusCompleted,
		InstanceStatusArchived,
	} {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			if err := ValidatePastActivityItem(validPastActivityItem(status)); err != nil {
				t.Fatalf("ValidatePastActivityItem() error = %v", err)
			}
		})
	}
}

func TestValidatePastActivityItemAcceptsCustomActivityType(t *testing.T) {
	t.Parallel()

	item := validPastActivityItem(InstanceStatusCompleted)
	item.ActivityType = ActivityTypeCustom
	if err := ValidatePastActivityItem(item); err != nil {
		t.Fatalf("ValidatePastActivityItem() error = %v", err)
	}
}

func TestValidatePastActivityItemRejectsCancelledOrMalformedHistory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*PastActivityItem)
	}{
		{name: "cancelled", mutate: func(item *PastActivityItem) { item.InstanceStatus = InstanceStatusCancelled }},
		{name: "all type", mutate: func(item *PastActivityItem) { item.ActivityType = ActivityTypeAll }},
		{name: "blank title", mutate: func(item *PastActivityItem) { item.InstanceTitle = " " }},
		{name: "unpublished", mutate: func(item *PastActivityItem) { item.PublicationVersion = 0 }},
		{name: "completion before publication", mutate: func(item *PastActivityItem) { item.CompletedAt = item.PublishedAt.Add(-time.Second) }},
		{name: "negative count", mutate: func(item *PastActivityItem) { item.HistoricalRegistrationCount = -1 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			item := validPastActivityItem(InstanceStatusCompleted)
			test.mutate(&item)
			if err := ValidatePastActivityItem(item); !errors.Is(err, ErrInvalidPastActivityFacts) {
				t.Fatalf("ValidatePastActivityItem() error = %v", err)
			}
		})
	}
}

func validPastActivityItem(status InstanceStatus) PastActivityItem {
	publishedAt := time.Date(2026, time.September, 1, 2, 0, 0, 0, time.UTC)
	return PastActivityItem{
		SeriesID:                         uuid.New(),
		SeriesTitle:                      "AI Community Nights",
		SuccessfulPublishedInstanceCount: 3,
		HistoricalRegistrationCount:      42,
		InstanceID:                       uuid.New(),
		InstanceTitle:                    "September Night",
		InstanceStatus:                   status,
		ActivityType:                     ActivityTypeAIRoundtable,
		PublicationVersion:               2,
		PublishedAt:                      publishedAt,
		CompletedAt:                      publishedAt.Add(4 * time.Hour),
	}
}
