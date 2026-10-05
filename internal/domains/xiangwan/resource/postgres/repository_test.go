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

func TestCreateDraftPersistsExactBinding(t *testing.T) {
	t.Parallel()

	value := knownRelation(t)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		exec: func(query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return fakeResourceResult(1), nil
		},
	}}
	result, err := repository.CreateDraft(context.Background(), value)
	if err != nil {
		t.Fatalf("CreateDraft() error = %v", err)
	}
	if result.Duplicate || result.Relation.ID != value.ID ||
		!reflect.DeepEqual(capturedArgs, []any{
			value.ID, value.TenantID, value.SeriesID, value.InstanceID,
			value.SessionID, value.Kind, value.ContentID,
			value.ContentRevision, value.AccessPolicy, value.SortOrder,
			value.ExpectedTargetVersion, value.CreatedBy,
			value.IdempotencyKey, value.CreatedAt,
		}) {
		t.Fatalf("CreateDraft() = %+v args=%#v", result, capturedArgs)
	}
	for _, fragment := range []string{
		"INSERT INTO xiangwan_resource_relations",
		"content_revision_at",
		"expected_target_version",
		"ON CONFLICT (tenant_id, created_by, idempotency_key) DO NOTHING",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("insert query does not contain %q", fragment)
		}
	}
}

func TestCreateDraftReturnsExactReplayAndRejectsChangedIntent(t *testing.T) {
	t.Parallel()

	value := knownRelation(t)
	existing := value
	existing.ID = uuid.New()
	existing.CreatedAt = existing.CreatedAt.Add(time.Second)
	repository := &Repository{db: &fakeResourceExecutor{
		exec: func(string, ...any) (sql.Result, error) {
			return fakeResourceResult(0), nil
		},
		queryRow: func(string, ...any) rowScanner {
			return relationRow(existing)
		},
	}}
	result, err := repository.CreateDraft(context.Background(), value)
	if err != nil {
		t.Fatalf("CreateDraft(replay) error = %v", err)
	}
	if !result.Duplicate || result.Relation.ID != existing.ID {
		t.Fatalf("CreateDraft(replay) = %+v", result)
	}

	existing.ContentID = uuid.New()
	if _, err := repository.CreateDraft(
		context.Background(),
		value,
	); !errors.Is(err, resource.ErrRelationIntentConflict) {
		t.Fatalf("CreateDraft(changed intent) error = %v", err)
	}
}

func TestCreateDraftRejectsInvalidInputBeforeSQL(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		exec: func(string, ...any) (sql.Result, error) {
			t.Fatal("invalid relation reached PostgreSQL")
			return nil, nil
		},
	}}
	value := knownRelation(t)
	value.ContentID = uuid.Nil
	if _, err := repository.CreateDraft(
		context.Background(),
		value,
	); !errors.Is(err, resource.ErrInvalidRelation) {
		t.Fatalf("CreateDraft(invalid) error = %v", err)
	}
}

func TestGetRelationIsTenantScopedAndValidatesFacts(t *testing.T) {
	t.Parallel()

	value := knownRelation(t)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return relationRow(value)
		},
	}}
	got, err := repository.GetByID(
		context.Background(),
		value.TenantID,
		value.ID,
	)
	if err != nil || got.ID != value.ID {
		t.Fatalf("GetByID() = %+v, %v", got, err)
	}
	if !strings.Contains(capturedQuery, "tenant_id = $1 AND id = $2") ||
		!reflect.DeepEqual(capturedArgs, []any{value.TenantID, value.ID}) {
		t.Fatalf("GetByID() query=%q args=%#v", capturedQuery, capturedArgs)
	}

	value.ExpectedTargetVersion = 0
	if _, err := scanRelation(relationRow(value)); !errors.Is(
		err,
		ErrRelationFactsConflict,
	) {
		t.Fatalf("scanRelation(invalid) error = %v", err)
	}
}

func TestClassifyRelationWriteError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code string
		want error
	}{
		{code: "23505", want: ErrRelationExists},
		{code: "23503", want: ErrRelationFactsConflict},
		{code: "23514", want: ErrRelationFactsConflict},
		{code: "40001", want: ErrRelationTargetChanged},
		{code: "40P01", want: ErrRelationFactsConflict},
	}
	for _, test := range tests {
		test := test
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()
			err := classifyRelationWriteError(&pgconn.PgError{Code: test.code})
			if !errors.Is(err, test.want) {
				t.Fatalf("classifyRelationWriteError() = %v", err)
			}
		})
	}
}

func knownRelation(t *testing.T) resource.Relation {
	t.Helper()
	now := time.Date(2026, time.September, 13, 10, 0, 0, 0, time.UTC)
	value, err := resource.NewDraft(resource.CreateDraftCommand{
		TenantID:              uuid.New(),
		SeriesID:              uuid.New(),
		InstanceID:            uuid.New(),
		Kind:                  resource.RelationKindInstanceReview,
		ContentID:             uuid.New(),
		ContentRevision:       now.Add(-time.Minute),
		AccessPolicy:          resource.AccessPolicyPublic,
		SortOrder:             1,
		ExpectedTargetVersion: 4,
		CreatedBy:             uuid.New(),
		IdempotencyKey:        "resource-relation:operation-001",
		CreatedAt:             now,
	})
	if err != nil {
		t.Fatalf("resource.NewDraft() error = %v", err)
	}
	return value
}

func relationRow(value resource.Relation) rowScanner {
	sessionID := uuid.NullUUID{}
	if value.SessionID != nil {
		sessionID = uuid.NullUUID{UUID: *value.SessionID, Valid: true}
	}
	return &fakeResourceRow{values: []any{
		value.ID, value.TenantID, value.SeriesID, value.InstanceID,
		sessionID, value.Kind, value.ContentID, value.ContentRevision,
		value.AccessPolicy, value.SortOrder, value.ExpectedTargetVersion,
		value.CreatedBy, value.IdempotencyKey, value.CreatedAt,
	}}
}

type fakeResourceExecutor struct {
	exec     func(string, ...any) (sql.Result, error)
	query    func(string, ...any) (resourceRowsScanner, error)
	queryRow func(string, ...any) rowScanner
}

func (executor *fakeResourceExecutor) execContext(
	_ context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	return executor.exec(query, args...)
}

func (executor *fakeResourceExecutor) queryRowContext(
	_ context.Context,
	query string,
	args ...any,
) rowScanner {
	if executor.queryRow == nil {
		return &fakeResourceRow{err: sql.ErrNoRows}
	}
	return executor.queryRow(query, args...)
}

func (executor *fakeResourceExecutor) queryContext(
	_ context.Context,
	query string,
	args ...any,
) (resourceRowsScanner, error) {
	if executor.query == nil {
		return &fakePublishedResourceRows{}, nil
	}
	return executor.query(query, args...)
}

type fakeResourceResult int64

func (result fakeResourceResult) LastInsertId() (int64, error) {
	return 0, errors.New("unsupported")
}

func (result fakeResourceResult) RowsAffected() (int64, error) {
	return int64(result), nil
}

type fakeResourceRow struct {
	values []any
	err    error
}

func (row *fakeResourceRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return errors.New("fake ResourceRelation row destination mismatch")
	}
	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		value := reflect.ValueOf(row.values[index])
		if target.Kind() != reflect.Pointer || !value.IsValid() ||
			!value.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("fake ResourceRelation row value mismatch")
		}
		target.Elem().Set(value)
	}
	return nil
}
