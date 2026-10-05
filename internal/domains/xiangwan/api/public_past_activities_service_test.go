package xiangwanapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestPublicPastActivitiesServiceFixesTenantAndClock(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(70)
	asOf := time.Date(2026, time.September, 15, 6, 0, 0, 0, time.UTC)
	want := activity.PastActivitiesPage{
		ActiveActivityType: activity.ActivityTypeCourse,
		AsOf:               asOf,
	}
	reader := &fakePublicPastActivitiesReader{page: want}
	service, err := NewPublicPastActivitiesService(
		tenantID,
		reader,
		func() time.Time { return asOf },
	)
	if err != nil {
		t.Fatalf("NewPublicPastActivitiesService() error = %v", err)
	}
	request := PublicPastActivitiesRequest{
		ActivityType: activity.ActivityTypeCourse,
		Cursor:       "opaque",
		Limit:        12,
	}
	got, err := service.Read(context.Background(), request)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %+v, %v", got, err)
	}
	if reader.filter.TenantID != tenantID ||
		reader.filter.ActivityType != request.ActivityType ||
		reader.filter.Cursor != request.Cursor || reader.filter.Limit != 12 ||
		!reader.filter.At.Equal(asOf) {
		t.Fatalf("reader filter = %+v", reader.filter)
	}
}

func TestPublicPastActivitiesServiceRejectsInvalidInputBeforeReader(t *testing.T) {
	t.Parallel()

	reader := &fakePublicPastActivitiesReader{}
	service, _ := NewPublicPastActivitiesService(
		apiUUID(71),
		reader,
		time.Now,
	)
	requests := []PublicPastActivitiesRequest{
		{ActivityType: "unknown"},
		{Limit: activity.MaxPastActivitiesLimit + 1},
		{Cursor: string(make([]byte, 2049))},
	}
	for _, request := range requests {
		if _, err := service.Read(context.Background(), request); !errors.Is(err, ErrInvalidPublicPastActivitiesRequest) {
			t.Fatalf("Read(%+v) error = %v", request, err)
		}
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls = %d", reader.calls)
	}
}

func TestNewPublicPastActivitiesServiceRequiresAllWiring(t *testing.T) {
	t.Parallel()

	if _, err := NewPublicPastActivitiesService(uuid.Nil, nil, nil); !errors.Is(err, ErrInvalidPublicPastActivitiesService) {
		t.Fatalf("NewPublicPastActivitiesService() error = %v", err)
	}
}

func TestPublicPastActivitiesServiceRejectsCrossFilterOrUnorderedPage(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 15, 6, 0, 0, 0, time.UTC)
	first := publicReviewSummary(apiUUID(74), apiUUID(75))
	first.ActivityType = activity.ActivityTypeCourse
	first.CompletedAt = asOf.Add(-2 * time.Hour)
	first.PublishedAt = first.CompletedAt.Add(-time.Hour)
	second := publicReviewSummary(apiUUID(76), apiUUID(77))
	second.ActivityType = activity.ActivityTypeCourse
	second.CompletedAt = asOf.Add(-time.Hour)
	second.PublishedAt = second.CompletedAt.Add(-time.Hour)
	reader := &fakePublicPastActivitiesReader{page: activity.PastActivitiesPage{
		Items:              []activity.PastActivityItem{first, second},
		ActiveActivityType: activity.ActivityTypeCourse,
		AsOf:               asOf,
	}}
	service, _ := NewPublicPastActivitiesService(
		apiUUID(78),
		reader,
		func() time.Time { return asOf },
	)
	if _, err := service.Read(context.Background(), PublicPastActivitiesRequest{
		ActivityType: activity.ActivityTypeCourse,
	}); !errors.Is(err, ErrPublicPastActivitiesResponseConflict) {
		t.Fatalf("Read(unordered) error = %v", err)
	}
}

type fakePublicPastActivitiesReader struct {
	page   activity.PastActivitiesPage
	err    error
	filter activity.PastActivitiesFilter
	calls  int
}

func (fake *fakePublicPastActivitiesReader) ListPastActivities(
	_ context.Context,
	filter activity.PastActivitiesFilter,
) (activity.PastActivitiesPage, error) {
	fake.calls++
	fake.filter = filter
	return fake.page, fake.err
}
