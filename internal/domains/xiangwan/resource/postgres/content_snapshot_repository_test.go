package resourcepostgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestGetContentSnapshotIsTenantScopedAndReturnsDigest(t *testing.T) {
	t.Parallel()

	value := knownContentSnapshot()
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return contentSnapshotRow(value)
		},
	}}
	got, err := repository.GetContentSnapshotByRelation(
		context.Background(),
		value.TenantID,
		value.RelationID,
	)
	if err != nil || got.RelationID != value.RelationID ||
		got.SubjectDigest != value.SubjectDigest ||
		got.Schema != resource.ContentSnapshotSchema {
		t.Fatalf("GetContentSnapshotByRelation() = %+v, %v", got, err)
	}
	if !strings.Contains(
		capturedQuery,
		"WHERE tenant_id = $1 AND relation_id = $2",
	) || !reflect.DeepEqual(
		capturedArgs,
		[]any{value.TenantID, value.RelationID},
	) {
		t.Fatalf(
			"GetContentSnapshotByRelation() query=%q args=%#v",
			capturedQuery,
			capturedArgs,
		)
	}
}

func TestScanContentSnapshotRejectsCorruptFacts(t *testing.T) {
	t.Parallel()

	value := knownContentSnapshot()
	row := contentSnapshotRow(value).(*fakeResourceRow)
	row.values[5] = []byte("short")
	if _, err := scanContentSnapshot(row); !errors.Is(
		err,
		ErrContentSnapshotFactsConflict,
	) {
		t.Fatalf("scanContentSnapshot(short digest) error = %v", err)
	}

	row = contentSnapshotRow(value).(*fakeResourceRow)
	row.values[4] = "other"
	if _, err := scanContentSnapshot(row); !errors.Is(
		err,
		ErrContentSnapshotFactsConflict,
	) {
		t.Fatalf("scanContentSnapshot(invalid payload) error = %v", err)
	}
}

func knownContentSnapshot() resource.ContentSnapshot {
	now := time.Date(2026, time.September, 13, 16, 0, 0, 0, time.UTC)
	digest := sha256.Sum256([]byte("canonical snapshot payload"))
	return resource.ContentSnapshot{
		TenantID:        uuid.New(),
		RelationID:      uuid.New(),
		ContentID:       uuid.New(),
		ContentRevision: now.Add(-time.Minute),
		Schema:          resource.ContentSnapshotSchema,
		SubjectDigest:   digest,
		CapturedAt:      now,
	}
}

func contentSnapshotRow(value resource.ContentSnapshot) rowScanner {
	return &fakeResourceRow{values: []any{
		value.TenantID, value.RelationID, value.ContentID,
		value.ContentRevision, value.Schema,
		append([]byte(nil), value.SubjectDigest[:]...), value.CapturedAt,
	}}
}
