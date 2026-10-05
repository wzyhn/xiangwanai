package resourcepostgres

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

func TestReadPastHighlightContextUsesCompletePublicEvidenceAndStableScope(
	t *testing.T,
) {
	t.Parallel()

	tenantID := pastHighlightAdapterUUID(1)
	anchorID := pastHighlightAdapterUUID(2)
	seriesID := pastHighlightAdapterUUID(3)
	previousID := pastHighlightAdapterUUID(4)
	onlineID := pastHighlightAdapterUUID(5)
	offlineID := pastHighlightAdapterUUID(6)
	anchorPublishedAt := pastHighlightAdapterTime(20)
	previousPublishedAt := pastHighlightAdapterTime(10)
	previousCompletedAt := pastHighlightAdapterTime(12)
	rows := &fakePublishedResourceRows{values: [][]any{
		pastHighlightAdapterRow(
			seriesID,
			anchorID,
			anchorPublishedAt,
			previousID,
			previousPublishedAt,
			previousCompletedAt,
			false,
			onlineID,
			activity.DeliveryModeOnline,
			50,
			2,
			1,
		),
		pastHighlightAdapterRow(
			seriesID,
			anchorID,
			anchorPublishedAt,
			previousID,
			previousPublishedAt,
			previousCompletedAt,
			false,
			offlineID,
			activity.DeliveryModeOffline,
			2,
			3,
			2,
		),
	}}
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(query string, args ...any) (resourceRowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return rows, nil
		},
	}}

	got, err := repository.ReadPastHighlightContext(
		context.Background(),
		tenantID,
		anchorID,
	)
	if err != nil || got == nil || got.PreviousInstanceID != previousID ||
		got.FeaturedSessionID == nil || *got.FeaturedSessionID != offlineID {
		t.Fatalf("ReadPastHighlightContext() = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID, anchorID}) {
		t.Fatalf("ReadPastHighlightContext() args = %#v", capturedArgs)
	}
	for _, fragment := range []string{
		"activity_instance.tenant_id = $1",
		"activity_series.is_recurring",
		"candidate.series_id = anchor.series_id",
		"candidate.status IN ('completed', 'archived')",
		"candidate.published_at < anchor.published_at",
		"JOIN xiangwan_resource_content_snapshots AS snapshot",
		"approval.subject_digest = snapshot.subject_digest",
		"approval.decision = 'approved'",
		"relation.access_policy = 'public'",
		"publication.access_policy = 'public'",
		"public_context.relation_kind = 'session_resources'",
		"public_context.expected_target_version = candidate_session.version",
		"candidate_session.status IN ('ended', 'archived')",
		"candidate_session.confirmed_registration_count DESC",
		"LIMIT 201",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("past-highlight query does not contain %q", fragment)
		}
	}
}

func TestReadPastHighlightContextReturnsEmptyForValidAnchorWithoutContent(
	t *testing.T,
) {
	t.Parallel()

	anchorID := pastHighlightAdapterUUID(10)
	rows := &fakePublishedResourceRows{values: [][]any{
		pastHighlightEmptyAdapterRow(
			pastHighlightAdapterUUID(11),
			anchorID,
			pastHighlightAdapterTime(20),
		),
	}}
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			return rows, nil
		},
	}}

	got, err := repository.ReadPastHighlightContext(
		context.Background(),
		pastHighlightAdapterUUID(12),
		anchorID,
	)
	if err != nil || got != nil {
		t.Fatalf("ReadPastHighlightContext(empty) = %+v, %v", got, err)
	}
}

func TestReadPastHighlightContextDistinguishesMissingAnchor(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			return &fakePublishedResourceRows{}, nil
		},
	}}
	_, err := repository.ReadPastHighlightContext(
		context.Background(),
		pastHighlightAdapterUUID(20),
		pastHighlightAdapterUUID(21),
	)
	if !errors.Is(err, ErrPastHighlightAnchorNotFound) {
		t.Fatalf("ReadPastHighlightContext(missing) error = %v", err)
	}
}

func TestReadPastHighlightContextRejectsInvalidInputBeforeSQL(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			t.Fatal("invalid past-highlight scope reached PostgreSQL")
			return nil, nil
		},
	}}
	if _, err := repository.ReadPastHighlightContext(
		context.Background(),
		uuid.Nil,
		pastHighlightAdapterUUID(30),
	); !errors.Is(err, ErrInvalidPastHighlightQuery) {
		t.Fatalf("ReadPastHighlightContext(invalid) error = %v", err)
	}
}

func TestScanPastHighlightContextRejectsUnboundedSessions(t *testing.T) {
	t.Parallel()

	values := make([][]any, maxPastHighlightSessionCandidates+1)
	for index := range values {
		values[index] = pastHighlightAdapterRow(
			pastHighlightAdapterUUID(40),
			pastHighlightAdapterUUID(41),
			pastHighlightAdapterTime(20),
			pastHighlightAdapterUUID(42),
			pastHighlightAdapterTime(10),
			pastHighlightAdapterTime(12),
			false,
			uuid.New(),
			activity.DeliveryModeOffline,
			1,
			2,
			int64(index),
		)
	}
	if _, err := scanPastHighlightContext(
		&fakePublishedResourceRows{values: values},
	); !errors.Is(err, ErrPastHighlightFactsConflict) {
		t.Fatalf("scanPastHighlightContext(unbounded) error = %v", err)
	}
}

func pastHighlightAdapterRow(
	seriesID uuid.UUID,
	anchorID uuid.UUID,
	anchorPublishedAt time.Time,
	previousID uuid.UUID,
	previousPublishedAt time.Time,
	previousCompletedAt time.Time,
	hasInstanceContent bool,
	sessionID uuid.UUID,
	deliveryMode activity.DeliveryMode,
	confirmedCount int64,
	startHour int,
	sortOrder int64,
) []any {
	return []any{
		seriesID,
		anchorID,
		anchorPublishedAt,
		uuid.NullUUID{UUID: previousID, Valid: true},
		uuid.NullUUID{UUID: seriesID, Valid: true},
		sql.NullString{String: string(activity.InstanceStatusCompleted), Valid: true},
		sql.NullTime{Time: previousPublishedAt, Valid: true},
		sql.NullTime{Time: previousCompletedAt, Valid: true},
		sql.NullBool{Bool: hasInstanceContent, Valid: true},
		uuid.NullUUID{UUID: sessionID, Valid: true},
		sql.NullString{String: string(deliveryMode), Valid: true},
		sql.NullInt64{Int64: confirmedCount, Valid: true},
		sql.NullTime{Time: pastHighlightAdapterTime(startHour), Valid: true},
		sql.NullInt64{Int64: sortOrder, Valid: true},
	}
}

func pastHighlightEmptyAdapterRow(
	seriesID uuid.UUID,
	anchorID uuid.UUID,
	anchorPublishedAt time.Time,
) []any {
	return []any{
		seriesID,
		anchorID,
		anchorPublishedAt,
		uuid.NullUUID{},
		uuid.NullUUID{},
		sql.NullString{},
		sql.NullTime{},
		sql.NullTime{},
		sql.NullBool{},
		uuid.NullUUID{},
		sql.NullString{},
		sql.NullInt64{},
		sql.NullTime{},
		sql.NullInt64{},
	}
}

func pastHighlightAdapterUUID(lastByte byte) uuid.UUID {
	var value uuid.UUID
	value[len(value)-1] = lastByte
	return value
}

func pastHighlightAdapterTime(hour int) time.Time {
	return time.Date(2026, time.September, 15, hour, 0, 0, 0, time.UTC)
}
