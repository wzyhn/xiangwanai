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

func TestResolveNextInstanceSessionRouteUsesPublishedSameSeriesTarget(
	t *testing.T,
) {
	t.Parallel()

	tenantID := nextRouteAdapterUUID(1)
	seriesID := nextRouteAdapterUUID(2)
	sourceID := nextRouteAdapterUUID(3)
	targetID := nextRouteAdapterUUID(4)
	firstSessionID := nextRouteAdapterUUID(5)
	secondSessionID := nextRouteAdapterUUID(6)
	rows := &fakeRows{rows: [][]any{
		nextRouteAdapterRow(
			seriesID,
			sourceID,
			targetID,
			firstSessionID,
			nextRouteAdapterTime(14),
			1,
		),
		nextRouteAdapterRow(
			seriesID,
			sourceID,
			targetID,
			secondSessionID,
			nextRouteAdapterTime(15),
			2,
		),
	}}
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return rows, nil
		},
	}}

	got, err := repository.ResolveNextInstanceSessionRoute(
		context.Background(),
		tenantID,
		sourceID,
	)
	if err != nil || got.SeriesID != seriesID ||
		got.SourceInstanceID != sourceID || got.TargetInstanceID == nil ||
		*got.TargetInstanceID != targetID ||
		got.SessionRoute.Kind != activity.SessionRouteSelectionRequired ||
		got.SessionRoute.SessionID != nil ||
		!reflect.DeepEqual(
			got.SessionRoute.CandidateSessionIDs,
			[]uuid.UUID{firstSessionID, secondSessionID},
		) {
		t.Fatalf("ResolveNextInstanceSessionRoute() = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID, sourceID}) {
		t.Fatalf("ResolveNextInstanceSessionRoute() args = %#v", capturedArgs)
	}
	for _, fragment := range []string{
		"source_instance.tenant_id = $1",
		"source_instance.id = $2",
		"source_instance.status IN ('completed', 'archived')",
		"activity_series.status IN ('active', 'archived')",
		"source.series_status = 'active'",
		"activity_series.is_recurring",
		"source.is_recurring",
		"candidate_instance.series_id = source.series_id",
		"candidate_instance.status = 'published'",
		"candidate_instance.published_at > source.published_at",
		"activity_session.status = 'published'",
		"ORDER BY candidate_instance.published_at ASC, candidate_instance.id ASC",
		"LIMIT 1",
		"LIMIT 201",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("next-Instance route query does not contain %q", fragment)
		}
	}
}

func TestResolveNextInstanceSessionRouteReturnsUnavailableWithoutPublishedTarget(
	t *testing.T,
) {
	t.Parallel()

	seriesID := nextRouteAdapterUUID(10)
	sourceID := nextRouteAdapterUUID(11)
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return &fakeRows{rows: [][]any{
				nextRouteAdapterEmptyRow(seriesID, sourceID),
			}}, nil
		},
	}}

	got, err := repository.ResolveNextInstanceSessionRoute(
		context.Background(),
		nextRouteAdapterUUID(12),
		sourceID,
	)
	if err != nil || got.SeriesID != seriesID ||
		got.SourceInstanceID != sourceID || got.TargetInstanceID != nil ||
		got.SessionRoute.Kind != activity.SessionRouteUnavailable ||
		got.SessionRoute.SessionID != nil {
		t.Fatalf("ResolveNextInstanceSessionRoute(empty) = %+v, %v", got, err)
	}
}

func TestResolveNextInstanceSessionRouteDistinguishesMissingSource(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return &fakeRows{}, nil
		},
	}}
	_, err := repository.ResolveNextInstanceSessionRoute(
		context.Background(),
		nextRouteAdapterUUID(20),
		nextRouteAdapterUUID(21),
	)
	if !errors.Is(err, ErrNextInstanceRouteSourceNotFound) {
		t.Fatalf("ResolveNextInstanceSessionRoute(missing) error = %v", err)
	}
}

func TestResolveNextInstanceSessionRouteRejectsInvalidInputBeforeSQL(
	t *testing.T,
) {
	t.Parallel()

	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			t.Fatal("invalid next-Instance route scope reached PostgreSQL")
			return nil, nil
		},
	}}
	if _, err := repository.ResolveNextInstanceSessionRoute(
		context.Background(),
		uuid.Nil,
		nextRouteAdapterUUID(30),
	); !errors.Is(err, ErrInvalidNextInstanceRouteQuery) {
		t.Fatalf("ResolveNextInstanceSessionRoute(invalid) error = %v", err)
	}
}

func TestScanNextInstanceRouteRejectsUnboundedSessionSet(t *testing.T) {
	t.Parallel()

	values := make([][]any, maxNextInstanceRouteSessions+1)
	for index := range values {
		values[index] = nextRouteAdapterRow(
			nextRouteAdapterUUID(40),
			nextRouteAdapterUUID(41),
			nextRouteAdapterUUID(42),
			uuid.New(),
			nextRouteAdapterTime(14),
			index,
		)
	}
	if _, err := scanNextInstanceRoute(
		&fakeRows{rows: values},
	); !errors.Is(err, ErrNextInstanceRouteFactsConflict) {
		t.Fatalf("scanNextInstanceRoute(unbounded) error = %v", err)
	}
}

func nextRouteAdapterRow(
	seriesID uuid.UUID,
	sourceID uuid.UUID,
	targetID uuid.UUID,
	sessionID uuid.UUID,
	sessionStartAt time.Time,
	sortOrder int,
) []any {
	return []any{
		seriesID,
		sourceID,
		nextRouteAdapterTime(10),
		uuid.NullUUID{UUID: seriesID, Valid: true},
		uuid.NullUUID{UUID: targetID, Valid: true},
		sql.NullString{String: string(activity.InstanceStatusPublished), Valid: true},
		sql.NullTime{Time: nextRouteAdapterTime(12), Valid: true},
		uuid.NullUUID{UUID: sessionID, Valid: true},
		sql.NullString{String: string(activity.SessionStatusPublished), Valid: true},
		sql.NullTime{Time: sessionStartAt, Valid: true},
		sql.NullInt64{Int64: int64(sortOrder), Valid: true},
	}
}

func nextRouteAdapterEmptyRow(seriesID uuid.UUID, sourceID uuid.UUID) []any {
	return []any{
		seriesID,
		sourceID,
		nextRouteAdapterTime(10),
		uuid.NullUUID{},
		uuid.NullUUID{},
		sql.NullString{},
		sql.NullTime{},
		uuid.NullUUID{},
		sql.NullString{},
		sql.NullTime{},
		sql.NullInt64{},
	}
}

func nextRouteAdapterUUID(lastByte byte) uuid.UUID {
	var value uuid.UUID
	value[len(value)-1] = lastByte
	return value
}

func nextRouteAdapterTime(hour int) time.Time {
	return time.Date(2026, time.September, 17, hour, 0, 0, 0, time.UTC)
}
