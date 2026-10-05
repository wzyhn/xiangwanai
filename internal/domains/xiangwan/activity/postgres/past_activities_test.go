package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestListPastActivitiesUsesTenantBoundStableKeyset(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	asOf := time.Date(2026, time.September, 15, 6, 0, 0, 0, time.UTC)
	first := knownPastActivityItem(asOf.Add(-time.Hour), activity.ActivityTypeCourse)
	second := knownPastActivityItem(asOf.Add(-2*time.Hour), activity.ActivityTypeCourse)
	third := knownPastActivityItem(asOf.Add(-3*time.Hour), activity.ActivityTypeCourse)

	var capturedQueries []string
	var capturedArgs [][]any
	call := 0
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQueries = append(capturedQueries, query)
			capturedArgs = append(capturedArgs, append([]any(nil), args...))
			call++
			if call == 1 {
				return &fakeRows{rows: [][]any{
					pastActivityScanValues(first),
					pastActivityScanValues(second),
					pastActivityScanValues(third),
				}}, nil
			}
			return &fakeRows{rows: [][]any{pastActivityScanValues(third)}}, nil
		},
	}}

	page, err := repository.ListPastActivities(
		context.Background(),
		activity.PastActivitiesFilter{
			TenantID:     tenantID,
			ActivityType: activity.ActivityTypeCourse,
			Limit:        2,
			At:           asOf,
		},
	)
	if err != nil {
		t.Fatalf("ListPastActivities() error = %v", err)
	}
	if !reflect.DeepEqual(page.Items, []activity.PastActivityItem{first, second}) ||
		page.NextCursor == "" || page.ActiveActivityType != activity.ActivityTypeCourse ||
		!page.AsOf.Equal(asOf) {
		t.Fatalf("ListPastActivities() = %+v", page)
	}
	for _, fragment := range []string{
		"activity_instance.tenant_id = $1",
		"activity_series.status IN ('active', 'archived')",
		"activity_instance.status IN ('completed', 'archived')",
		"activity_instance.completed_at <= $3",
		"activity_instance.completed_at DESC",
	} {
		if !strings.Contains(capturedQueries[0], fragment) {
			t.Fatalf("past query missing %q: %s", fragment, capturedQueries[0])
		}
	}
	if !reflect.DeepEqual(capturedArgs[0], []any{
		tenantID,
		activity.ActivityTypeCourse,
		asOf,
		3,
	}) {
		t.Fatalf("first query args = %#v", capturedArgs[0])
	}

	next, err := repository.ListPastActivities(
		context.Background(),
		activity.PastActivitiesFilter{
			TenantID:     tenantID,
			ActivityType: activity.ActivityTypeCourse,
			Limit:        2,
			Cursor:       page.NextCursor,
			At:           asOf.Add(time.Minute),
		},
	)
	if err != nil || !reflect.DeepEqual(next.Items, []activity.PastActivityItem{third}) ||
		!next.AsOf.Equal(asOf) {
		t.Fatalf("ListPastActivities(next) = %+v, %v", next, err)
	}
	if !strings.Contains(capturedQueries[1], ") < ($4, $5, $6)") ||
		len(capturedArgs[1]) != 7 || capturedArgs[1][0] != tenantID ||
		capturedArgs[1][1] != activity.ActivityTypeCourse ||
		capturedArgs[1][2] != asOf ||
		capturedArgs[1][3] != second.CompletedAt ||
		capturedArgs[1][4] != second.PublishedAt ||
		capturedArgs[1][5] != second.InstanceID || capturedArgs[1][6] != 3 {
		t.Fatalf("next query=%s args=%#v", capturedQueries[1], capturedArgs[1])
	}
}

func TestPastActivitiesCursorRejectsCrossTenantFilterAndExpiry(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	asOf := time.Date(2026, time.September, 15, 6, 0, 0, 0, time.UTC)
	item := knownPastActivityItem(asOf.Add(-time.Hour), activity.ActivityTypeCourse)
	cursor, err := encodePastActivitiesCursor(activity.PastActivitiesFilter{
		TenantID:     tenantID,
		ActivityType: activity.ActivityTypeCourse,
		At:           asOf,
	}, item)
	if err != nil {
		t.Fatal(err)
	}

	tests := []activity.PastActivitiesFilter{
		{TenantID: uuid.New(), ActivityType: activity.ActivityTypeCourse, Cursor: cursor, At: asOf},
		{TenantID: tenantID, ActivityType: activity.ActivityTypeCompetition, Cursor: cursor, At: asOf},
		{TenantID: tenantID, ActivityType: activity.ActivityTypeCourse, Cursor: cursor, At: asOf.Add(activity.MaxPastActivitiesCursorAge + time.Second)},
	}
	for _, filter := range tests {
		if _, _, err := normalizePastActivitiesFilter(filter); !errors.Is(err, activity.ErrStalePastActivitiesCursor) {
			t.Fatalf("normalizePastActivitiesFilter(%+v) error = %v", filter, err)
		}
	}
}

func TestReadPastActivityUsesExactTenantAndTranslatesMissing(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	item := knownPastActivityItem(time.Now().UTC(), activity.ActivityTypeSpecialEvent)
	asOf := item.CompletedAt.Add(time.Second)
	var query string
	var args []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(value string, values ...any) rowScanner {
			query = value
			args = append([]any(nil), values...)
			return &fakeRow{values: pastActivityScanValues(item)}
		},
	}}
	got, err := repository.ReadPastActivity(
		context.Background(),
		tenantID,
		item.InstanceID,
		asOf,
	)
	if err != nil || !reflect.DeepEqual(got, item) {
		t.Fatalf("ReadPastActivity() = %+v, %v", got, err)
	}
	if !strings.Contains(query, "activity_instance.tenant_id = $1") ||
		!strings.Contains(query, "activity_instance.completed_at <= $3") ||
		!reflect.DeepEqual(args, []any{tenantID, item.InstanceID, asOf}) {
		t.Fatalf("exact query=%s args=%#v", query, args)
	}

	missing := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner { return &fakeRow{err: sql.ErrNoRows} },
	}}
	if _, err := missing.ReadPastActivity(
		context.Background(),
		tenantID,
		item.InstanceID,
		asOf,
	); !errors.Is(err, activity.ErrPastActivityNotFound) {
		t.Fatalf("ReadPastActivity(missing) error = %v", err)
	}
}

func TestReadPastActivityDetailBlocksRequiresPublishedPastInstance(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	instanceID := uuid.New()
	asOf := time.Date(2026, time.September, 15, 6, 0, 0, 0, time.UTC)
	var query string
	var args []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(value string, values ...any) rowScanner {
			query = value
			args = append([]any(nil), values...)
			return &fakeRow{values: []any{[]byte(`[{"type":"text","title":"简介","body":"本期内容"},{"type":"image","url":"/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.webp","caption":"现场"}]`)}}
		},
	}}
	blocks, err := repository.ReadPastActivityDetailBlocks(
		context.Background(), tenantID, instanceID, asOf,
	)
	if err != nil || len(blocks) != 2 || blocks[0].Title != "简介" ||
		blocks[1].URL == "" {
		t.Fatalf("ReadPastActivityDetailBlocks() = %+v, %v", blocks, err)
	}
	for _, fragment := range []string{
		"activity_instance.tenant_id = $1",
		"activity_series.status IN ('active', 'archived')",
		"activity_instance.status IN ('completed', 'archived')",
		"activity_instance.publication_version >= 1",
		"activity_instance.completed_at <= $3",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("detail block query missing %q: %s", fragment, query)
		}
	}
	if !reflect.DeepEqual(args, []any{tenantID, instanceID, asOf}) {
		t.Fatalf("detail block query args = %#v", args)
	}

	missing := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner { return &fakeRow{err: sql.ErrNoRows} },
	}}
	if _, err := missing.ReadPastActivityDetailBlocks(
		context.Background(), tenantID, instanceID, asOf,
	); !errors.Is(err, activity.ErrPastActivityNotFound) {
		t.Fatalf("ReadPastActivityDetailBlocks(missing) error = %v", err)
	}
}

func TestScanPastActivityRejectsCancelledFacts(t *testing.T) {
	t.Parallel()

	item := knownPastActivityItem(time.Now().UTC(), activity.ActivityTypeCourse)
	item.InstanceStatus = activity.InstanceStatusCancelled
	if _, err := scanPastActivityItem(&fakeRow{values: pastActivityScanValues(item)}); !errors.Is(err, ErrPastActivityProjection) {
		t.Fatalf("scanPastActivityItem() error = %v", err)
	}
}

func TestScanPastActivityNormalizesLegacyTitleWhitespace(t *testing.T) {
	t.Parallel()

	item := knownPastActivityItem(time.Now().UTC(), activity.ActivityTypeCourse)
	item.SeriesTitle = "  AI Community Nights\t"
	item.InstanceTitle = "\nA finished gathering  "

	got, err := scanPastActivityItem(&fakeRow{values: pastActivityScanValues(item)})
	if err != nil {
		t.Fatalf("scanPastActivityItem() error = %v", err)
	}
	if got.SeriesTitle != "AI Community Nights" ||
		got.InstanceTitle != "A finished gathering" {
		t.Fatalf("scanPastActivityItem() = %+v", got)
	}
}

func knownPastActivityItem(
	completedAt time.Time,
	activityType activity.ActivityType,
) activity.PastActivityItem {
	return activity.PastActivityItem{
		SeriesID:                         uuid.New(),
		SeriesTitle:                      "AI Community Nights",
		SuccessfulPublishedInstanceCount: 4,
		HistoricalRegistrationCount:      86,
		InstanceID:                       uuid.New(),
		InstanceTitle:                    "A finished gathering",
		InstanceStatus:                   activity.InstanceStatusCompleted,
		ActivityType:                     activityType,
		CoverImageURL:                    "https://cdn.example.com/covers/september-night.webp",
		PublicationVersion:               3,
		PublishedAt:                      completedAt.Add(-24 * time.Hour),
		CompletedAt:                      completedAt,
	}
}

func pastActivityScanValues(item activity.PastActivityItem) []any {
	return []any{
		item.SeriesID,
		item.SeriesTitle,
		item.SuccessfulPublishedInstanceCount,
		item.HistoricalRegistrationCount,
		item.InstanceID,
		item.InstanceTitle,
		item.InstanceStatus,
		item.ActivityType,
		item.CoverImageURL,
		item.PublicationVersion,
		item.PublishedAt,
		item.CompletedAt,
	}
}
