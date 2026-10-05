package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestResolvePublicReviewTargetRequiresEligibleExactHierarchy(t *testing.T) {
	t.Parallel()

	tenantID := publicReviewAdapterUUID(70)
	seriesID := publicReviewAdapterUUID(71)
	instanceID := publicReviewAdapterUUID(72)
	sessionID := publicReviewAdapterUUID(73)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeResourceRow{values: []any{
				seriesID,
				instanceID,
				uuid.NullUUID{UUID: sessionID, Valid: true},
			}}
		},
	}}

	target, err := repository.ResolvePublicReviewTarget(
		context.Background(),
		tenantID,
		instanceID,
		&sessionID,
		publicReviewTargetAsOf(),
	)
	if err != nil || target.SeriesID != seriesID ||
		target.InstanceID != instanceID || target.SessionID == nil ||
		*target.SessionID != sessionID {
		t.Fatalf("ResolvePublicReviewTarget() = %+v, %v", target, err)
	}
	if !reflect.DeepEqual(capturedArgs, []any{
		tenantID,
		instanceID,
		sessionID,
		publicReviewTargetAsOf(),
	}) {
		t.Fatalf("ResolvePublicReviewTarget() args = %#v", capturedArgs)
	}
	for _, fragment := range []string{
		"activity_instance.tenant_id = $1",
		"activity_instance.id = $2",
		"target_session.id = $3",
		"activity_instance.status IN ('completed', 'archived')",
		"activity_instance.completed_at <= $4",
		"activity_series.status IN ('active', 'archived')",
		"target_session.status IN ('ended', 'archived')",
		"($3::uuid IS NULL OR target_session.id IS NOT NULL)",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("target query does not contain %q", fragment)
		}
	}
}

func TestResolvePublicReviewTargetSupportsInstanceCommonScope(t *testing.T) {
	t.Parallel()

	seriesID := publicReviewAdapterUUID(74)
	instanceID := publicReviewAdapterUUID(75)
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		queryRow: func(_ string, args ...any) rowScanner {
			capturedArgs = append([]any(nil), args...)
			return &fakeResourceRow{values: []any{
				seriesID,
				instanceID,
				uuid.NullUUID{},
			}}
		},
	}}

	target, err := repository.ResolvePublicReviewTarget(
		context.Background(),
		publicReviewAdapterUUID(76),
		instanceID,
		nil,
		publicReviewTargetAsOf(),
	)
	if err != nil || target.SessionID != nil || capturedArgs[2] != nil {
		t.Fatalf("ResolvePublicReviewTarget(common) = %+v, %v args=%#v", target, err, capturedArgs)
	}
}

func TestResolvePublicReviewTargetReturnsOpaqueNotFound(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeResourceRow{err: sql.ErrNoRows}
		},
	}}
	_, err := repository.ResolvePublicReviewTarget(
		context.Background(),
		uuid.New(),
		uuid.New(),
		nil,
		publicReviewTargetAsOf(),
	)
	if !errors.Is(err, ErrPublicReviewTargetNotFound) {
		t.Fatalf("ResolvePublicReviewTarget(missing) error = %v", err)
	}
}

func TestResolvePublicReviewTargetRejectsInvalidOrConflictingFacts(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		queryRow: func(string, ...any) rowScanner {
			t.Fatal("invalid review target reached PostgreSQL")
			return nil
		},
	}}
	if _, err := repository.ResolvePublicReviewTarget(
		context.Background(),
		uuid.Nil,
		uuid.New(),
		nil,
		publicReviewTargetAsOf(),
	); !errors.Is(err, ErrInvalidPublicReviewQuery) {
		t.Fatalf("ResolvePublicReviewTarget(invalid) error = %v", err)
	}

	instanceID := uuid.New()
	repository = &Repository{db: &fakeResourceExecutor{
		queryRow: func(string, ...any) rowScanner {
			return &fakeResourceRow{values: []any{
				uuid.New(),
				uuid.New(),
				uuid.NullUUID{},
			}}
		},
	}}
	if _, err := repository.ResolvePublicReviewTarget(
		context.Background(),
		uuid.New(),
		instanceID,
		nil,
		publicReviewTargetAsOf(),
	); !errors.Is(err, ErrPublicReviewFactsConflict) {
		t.Fatalf("ResolvePublicReviewTarget(conflict) error = %v", err)
	}
}

func publicReviewTargetAsOf() time.Time {
	return time.Date(2026, time.September, 13, 9, 0, 0, 0, time.UTC)
}
