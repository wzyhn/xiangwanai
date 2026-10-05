package resourcepostgres

import (
	"context"
	"crypto/sha256"
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

func TestRecordModerationPersistsExactEvidence(t *testing.T) {
	t.Parallel()

	value := knownModerationObservation(t)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		exec: func(query string, args ...any) (sql.Result, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return fakeResourceResult(1), nil
		},
	}}
	result, err := repository.RecordModeration(context.Background(), value)
	if err != nil {
		t.Fatalf("RecordModeration() error = %v", err)
	}
	if result.Duplicate || result.Observation.ID != value.ID ||
		len(capturedArgs) != 16 ||
		!reflect.DeepEqual(capturedArgs[0:10], []any{
			value.ID, value.TenantID, value.RelationID, value.ContentID,
			value.ContentRevision, value.Provider, value.ProviderReference,
			value.PolicyVersion, value.Source, value.Decision,
		}) ||
		!reflect.DeepEqual(capturedArgs[10], value.SubjectDigest[:]) ||
		!reflect.DeepEqual(capturedArgs[11], value.PayloadDigest[:]) {
		t.Fatalf("RecordModeration() = %+v args=%#v", result, capturedArgs)
	}
	for _, fragment := range []string{
		"INSERT INTO xiangwan_resource_moderation_observations",
		"subject_digest",
		"payload_digest",
		"ON CONFLICT (tenant_id, provider, provider_reference) DO NOTHING",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("insert query does not contain %q", fragment)
		}
	}
}

func TestRecordModerationReplaysExactProviderReference(t *testing.T) {
	t.Parallel()

	requested := knownModerationObservation(t)
	existing := requested
	existing.ID = uuid.New()
	existing.RecordedAt = existing.RecordedAt.Add(time.Second)
	repository := &Repository{db: &fakeResourceExecutor{
		exec: func(string, ...any) (sql.Result, error) {
			t.Fatal("exact replay attempted another INSERT")
			return nil, nil
		},
		queryRow: func(string, ...any) rowScanner {
			return moderationObservationRow(existing)
		},
	}}
	result, err := repository.RecordModeration(
		context.Background(),
		requested,
	)
	if err != nil || !result.Duplicate || result.Observation.ID != existing.ID {
		t.Fatalf("RecordModeration(replay) = %+v, %v", result, err)
	}

	existing.Decision = resource.ModerationDecisionRejected
	if _, err := repository.RecordModeration(
		context.Background(),
		requested,
	); !errors.Is(err, resource.ErrModerationIntentConflict) {
		t.Fatalf("RecordModeration(changed provider fact) error = %v", err)
	}
}

func TestRecordModerationRejectsInvalidInputBeforeSQL(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		exec: func(string, ...any) (sql.Result, error) {
			t.Fatal("invalid moderation observation reached PostgreSQL")
			return nil, nil
		},
	}}
	value := knownModerationObservation(t)
	value.SubjectDigest = resource.Digest{}
	if _, err := repository.RecordModeration(
		context.Background(),
		value,
	); !errors.Is(err, resource.ErrInvalidModerationObservation) {
		t.Fatalf("RecordModeration(invalid) error = %v", err)
	}
}

func TestGetModerationIsTenantScopedAndValidatesFacts(t *testing.T) {
	t.Parallel()

	value := knownModerationObservation(t)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return moderationObservationRow(value)
		},
	}}
	got, err := repository.GetModerationByID(
		context.Background(),
		value.TenantID,
		value.ID,
	)
	if err != nil || got.ID != value.ID {
		t.Fatalf("GetModerationByID() = %+v, %v", got, err)
	}
	if !strings.Contains(capturedQuery, "tenant_id = $1 AND id = $2") ||
		!reflect.DeepEqual(capturedArgs, []any{value.TenantID, value.ID}) {
		t.Fatalf(
			"GetModerationByID() query=%q args=%#v",
			capturedQuery,
			capturedArgs,
		)
	}

	row := moderationObservationRow(value).(*fakeResourceRow)
	row.values[10] = []byte("short")
	if _, err := scanModerationObservation(row); !errors.Is(
		err,
		ErrModerationFactsConflict,
	) {
		t.Fatalf("scanModerationObservation(invalid) error = %v", err)
	}
}

func TestClassifyModerationWriteError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code string
		want error
	}{
		{code: "23505", want: ErrModerationExists},
		{code: "23503", want: ErrModerationFactsConflict},
		{code: "23514", want: ErrModerationFactsConflict},
		{code: "40001", want: ErrModerationFactsConflict},
		{code: "40P01", want: ErrModerationFactsConflict},
	}
	for _, test := range tests {
		test := test
		t.Run(test.code, func(t *testing.T) {
			t.Parallel()
			err := classifyModerationWriteError(
				&pgconn.PgError{Code: test.code},
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("classifyModerationWriteError() = %v", err)
			}
		})
	}
}

func knownModerationObservation(t *testing.T) resource.ModerationObservation {
	t.Helper()
	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	subjectDigest := sha256.Sum256([]byte("review-media-and-text"))
	payloadDigest := sha256.Sum256([]byte("verified-provider-payload"))
	value, err := resource.NewModerationObservation(
		resource.RecordModerationCommand{
			TenantID:          uuid.New(),
			RelationID:        uuid.New(),
			ContentID:         uuid.New(),
			ContentRevision:   now.Add(-time.Minute),
			Provider:          "wechat-content-security",
			ProviderReference: "callback:moderation-001",
			PolicyVersion:     "wechat-policy-v1",
			Source:            resource.ModerationSourceSignedCallback,
			Decision:          resource.ModerationDecisionApproved,
			SubjectDigest:     subjectDigest[:],
			PayloadDigest:     payloadDigest[:],
			ObservedAt:        now,
			RecordedAt:        now.Add(time.Second),
		},
	)
	if err != nil {
		t.Fatalf("resource.NewModerationObservation() error = %v", err)
	}
	return value
}

func moderationObservationRow(
	value resource.ModerationObservation,
) rowScanner {
	actorID := uuid.NullUUID{}
	if value.ActorID != nil {
		actorID = uuid.NullUUID{UUID: *value.ActorID, Valid: true}
	}
	reason := sql.NullString{}
	if value.Reason != nil {
		reason = sql.NullString{String: *value.Reason, Valid: true}
	}
	return &fakeResourceRow{values: []any{
		value.ID, value.TenantID, value.RelationID, value.ContentID,
		value.ContentRevision, value.Provider, value.ProviderReference,
		value.PolicyVersion, value.Source, value.Decision,
		append([]byte(nil), value.SubjectDigest[:]...),
		append([]byte(nil), value.PayloadDigest[:]...),
		actorID, reason, value.ObservedAt, value.RecordedAt,
	}}
}
