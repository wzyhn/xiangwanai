package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPublishPersistsExactApprovedBinding(t *testing.T) {
	t.Parallel()

	value := knownPublication(t)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		exec: func(query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return fakeResourceResult(1), nil
		},
	}}
	result, err := repository.Publish(context.Background(), value)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.Duplicate || result.Publication.ID != value.ID ||
		!reflect.DeepEqual(capturedArgs, []any{
			value.ID, value.TenantID, value.RelationID, value.ContentID,
			value.ContentRevision, value.ApprovalObservationID,
			value.AccessPolicy, value.ExpectedTargetVersion,
			value.PublishedBy, value.IdempotencyKey, value.PublishedAt,
		}) {
		t.Fatalf("Publish() = %+v args=%#v", result, capturedArgs)
	}
	for _, fragment := range []string{
		"INSERT INTO xiangwan_resource_publications",
		"approval_observation_id",
		"expected_target_version",
		"ON CONFLICT (tenant_id, published_by, idempotency_key) DO NOTHING",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("insert query does not contain %q", fragment)
		}
	}
}

func TestPublishReturnsExactReplayAndRejectsChangedIntent(t *testing.T) {
	t.Parallel()

	requested := knownPublication(t)
	existing := requested
	existing.ID = uuid.New()
	existing.PublishedAt = existing.PublishedAt.Add(time.Second)
	repository := &Repository{db: &fakeResourceExecutor{
		exec: func(string, ...any) (sql.Result, error) {
			t.Fatal("exact publication replay attempted another INSERT")
			return nil, nil
		},
		queryRow: func(string, ...any) rowScanner {
			return publicationRow(existing)
		},
	}}
	result, err := repository.Publish(context.Background(), requested)
	if err != nil || !result.Duplicate ||
		result.Publication.ID != existing.ID {
		t.Fatalf("Publish(replay) = %+v, %v", result, err)
	}

	existing.ApprovalObservationID = uuid.New()
	if _, err := repository.Publish(
		context.Background(),
		requested,
	); !errors.Is(err, resource.ErrPublicationIntentConflict) {
		t.Fatalf("Publish(changed approval) error = %v", err)
	}
}

func TestPublishRejectsInvalidInputBeforeSQL(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		exec: func(string, ...any) (sql.Result, error) {
			t.Fatal("invalid publication reached PostgreSQL")
			return nil, nil
		},
	}}
	value := knownPublication(t)
	value.ApprovalObservationID = uuid.Nil
	if _, err := repository.Publish(
		context.Background(),
		value,
	); !errors.Is(err, resource.ErrInvalidPublication) {
		t.Fatalf("Publish(invalid) error = %v", err)
	}
}

func TestGetPublicationIsTenantScopedAndValidatesFacts(t *testing.T) {
	t.Parallel()

	value := knownPublication(t)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return publicationRow(value)
		},
	}}
	got, err := repository.GetPublicationByRelation(
		context.Background(),
		value.TenantID,
		value.RelationID,
	)
	if err != nil || got.ID != value.ID {
		t.Fatalf("GetPublicationByRelation() = %+v, %v", got, err)
	}
	if !strings.Contains(
		capturedQuery,
		"tenant_id = $1 AND relation_id = $2",
	) || !reflect.DeepEqual(
		capturedArgs,
		[]any{value.TenantID, value.RelationID},
	) {
		t.Fatalf(
			"GetPublicationByRelation() query=%q args=%#v",
			capturedQuery,
			capturedArgs,
		)
	}

	value.ExpectedTargetVersion = 0
	if _, err := scanPublication(publicationRow(value)); !errors.Is(
		err,
		ErrPublicationFactsConflict,
	) {
		t.Fatalf("scanPublication(invalid) error = %v", err)
	}
}

func TestClassifyPublicationWriteError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code string
		want error
	}{
		{code: "23505", want: ErrPublicationExists},
		{code: "23503", want: ErrPublicationFactsConflict},
		{code: "23514", want: ErrPublicationFactsConflict},
		{code: "40001", want: ErrPublicationStateChanged},
		{code: "40P01", want: ErrPublicationFactsConflict},
	}
	for _, test := range tests {
		test := test
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()
			err := classifyPublicationWriteError(
				&pgconn.PgError{Code: test.code},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("classifyPublicationWriteError() = %v", err)
			}
		})
	}
}

func knownPublication(t *testing.T) resource.Publication {
	t.Helper()
	now := time.Date(2026, time.September, 13, 14, 0, 0, 0, time.UTC)
	value, err := resource.NewPublication(resource.PublishCommand{
		TenantID:              uuid.New(),
		RelationID:            uuid.New(),
		ContentID:             uuid.New(),
		ContentRevision:       now.Add(-time.Minute),
		ApprovalObservationID: uuid.New(),
		AccessPolicy:          resource.AccessPolicyPublic,
		ExpectedTargetVersion: 3,
		PublishedBy:           uuid.New(),
		IdempotencyKey:        "resource-publication:operation-001",
		PublishedAt:           now,
	})
	if err != nil {
		t.Fatalf("resource.NewPublication() error = %v", err)
	}
	return value
}

func publicationRow(value resource.Publication) rowScanner {
	return &fakeResourceRow{values: []any{
		value.ID, value.TenantID, value.RelationID, value.ContentID,
		value.ContentRevision, value.ApprovalObservationID,
		value.AccessPolicy, value.ExpectedTargetVersion,
		value.PublishedBy, value.IdempotencyKey, value.PublishedAt,
	}}
}
