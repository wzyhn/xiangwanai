package activitypostgres

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestResolveSeriesSessionRouteUsesCurrentInstanceWithoutDefaulting(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.UTC)
	candidates := []activity.SessionRouteCandidate{
		{SeriesID: seriesID, InstanceID: instanceID, SessionID: uuid.New(), SessionTitle: "Evening Session", SessionStatus: activity.SessionStatusPublished, SessionStartAt: now.Add(time.Hour), SortOrder: 2},
		{SeriesID: seriesID, InstanceID: instanceID, SessionID: uuid.New(), SessionTitle: "Morning Session", SessionStatus: activity.SessionStatusPublished, SessionStartAt: now, SortOrder: 1},
	}
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRows{rows: [][]any{
				sessionRouteCandidateScanValues(candidates[0]),
				sessionRouteCandidateScanValues(candidates[1]),
			}}, nil
		},
	}}

	resolution, err := repository.ResolveSeriesSessionRoute(context.Background(), tenantID, seriesID)
	if err != nil {
		t.Fatalf("ResolveSeriesSessionRoute() error = %v", err)
	}
	if resolution.Kind != activity.SessionRouteSelectionRequired || resolution.SessionID != nil || len(resolution.CandidateSessionIDs) != 2 {
		t.Fatalf("ResolveSeriesSessionRoute() = %+v", resolution)
	}
	if !strings.Contains(capturedQuery, "activity_instance.id = activity_series.current_public_instance_id") ||
		!strings.Contains(capturedQuery, "activity_series.tenant_id = $1") ||
		!strings.Contains(capturedQuery, "activity_session.title") ||
		!strings.Contains(capturedQuery, "FALSE AS review_only") ||
		!strings.Contains(capturedQuery, "LIMIT 201") {
		t.Fatalf("Series route query is not current/tenant scoped: %s", capturedQuery)
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID, seriesID}) {
		t.Fatalf("Series route args = %#v", capturedArgs)
	}
}

func TestResolveInstanceSessionRouteUsesRequestedInstance(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	instanceID := uuid.New()
	candidate := activity.SessionRouteCandidate{
		SeriesID:       uuid.New(),
		InstanceID:     instanceID,
		SessionID:      uuid.New(),
		SessionTitle:   "Archived review Session",
		SessionStatus:  activity.SessionStatusArchived,
		ReviewOnly:     true,
		SessionStartAt: time.Now(),
	}
	var capturedQuery string
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, _ ...any) (rowsScanner, error) {
			capturedQuery = query
			return &fakeRows{rows: [][]any{sessionRouteCandidateScanValues(candidate)}}, nil
		},
	}}

	resolution, err := repository.ResolveInstanceSessionRoute(context.Background(), tenantID, instanceID)
	if err != nil {
		t.Fatalf("ResolveInstanceSessionRoute() error = %v", err)
	}
	if resolution.Kind != activity.SessionRouteDirect || resolution.SessionID == nil || *resolution.SessionID != candidate.SessionID {
		t.Fatalf("ResolveInstanceSessionRoute() = %+v", resolution)
	}
	if !strings.Contains(capturedQuery, "activity_instance.tenant_id = $1") ||
		!strings.Contains(capturedQuery, "activity_instance.id = $2") ||
		!strings.Contains(capturedQuery, "activity_series.status IN ('active', 'archived')") ||
		!strings.Contains(capturedQuery, "activity_instance.status IN ('published', 'completed', 'cancelled', 'archived')") ||
		!strings.Contains(capturedQuery, "activity_instance.published_at IS NOT NULL") ||
		!strings.Contains(capturedQuery, "activity_session.status IN ('published', 'ended', 'cancelled', 'archived')") ||
		!strings.Contains(capturedQuery, "activity_session.published_at IS NOT NULL") ||
		!strings.Contains(capturedQuery, "NOT (") ||
		!strings.Contains(capturedQuery, "activity_series.status = 'active'") ||
		!strings.Contains(capturedQuery, "activity_instance.status IN ('published', 'completed', 'cancelled')") ||
		!strings.Contains(capturedQuery, "activity_session.status IN ('published', 'ended', 'cancelled')") ||
		!strings.Contains(capturedQuery, "activity_instance.status IN ('completed', 'archived')") ||
		!strings.Contains(capturedQuery, "activity_session.status IN ('ended', 'archived')") ||
		!strings.Contains(capturedQuery, "AS review_only") ||
		!strings.Contains(capturedQuery, "LIMIT 201") ||
		strings.Contains(capturedQuery, "current_public_instance_id") {
		t.Fatalf("Instance route query has wrong scope: %s", capturedQuery)
	}
}

func TestResolveSessionRouteReturnsUnavailableWhenNoPublicCandidateExists(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return &fakeRows{}, nil
		},
	}}
	resolution, err := repository.ResolveSeriesSessionRoute(context.Background(), uuid.New(), uuid.New())
	if err != nil {
		t.Fatalf("ResolveSeriesSessionRoute() error = %v", err)
	}
	if resolution.Kind != activity.SessionRouteUnavailable || resolution.SessionID != nil {
		t.Fatalf("ResolveSeriesSessionRoute() = %+v", resolution)
	}
}

func sessionRouteCandidateScanValues(candidate activity.SessionRouteCandidate) []any {
	return []any{
		candidate.SeriesID,
		candidate.InstanceID,
		candidate.SessionID,
		candidate.SessionTitle,
		candidate.SessionStatus,
		sql.NullTime{Time: candidate.SessionStartAt, Valid: !candidate.SessionStartAt.IsZero()},
		candidate.SortOrder,
		candidate.ReviewOnly,
	}
}
