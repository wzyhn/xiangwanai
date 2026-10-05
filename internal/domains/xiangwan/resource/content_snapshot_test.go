package resource

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidateContentSnapshotAcceptsFrozenPayload(t *testing.T) {
	t.Parallel()

	value := validContentSnapshot()
	if err := ValidateContentSnapshot(value); err != nil {
		t.Fatalf("ValidateContentSnapshot() error = %v", err)
	}
}

func TestValidateContentSnapshotFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*ContentSnapshot)
	}{
		{
			name: "relation absent",
			mutate: func(value *ContentSnapshot) {
				value.RelationID = uuid.Nil
			},
		},
		{
			name: "snapshot predates revision",
			mutate: func(value *ContentSnapshot) {
				value.CapturedAt = value.ContentRevision.Add(-time.Second)
			},
		},
		{
			name: "digest empty",
			mutate: func(value *ContentSnapshot) {
				value.SubjectDigest = Digest{}
			},
		},
		{
			name: "schema unknown",
			mutate: func(value *ContentSnapshot) {
				value.Schema = "other"
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := validContentSnapshot()
			test.mutate(&value)
			if err := ValidateContentSnapshot(value); !errors.Is(
				err,
				ErrInvalidContentSnapshot,
			) {
				t.Fatalf("ValidateContentSnapshot() error = %v", err)
			}
		})
	}
}

func validContentSnapshot() ContentSnapshot {
	now := time.Date(2026, time.September, 13, 15, 0, 0, 0, time.UTC)
	digest := sha256.Sum256([]byte("canonical snapshot payload"))
	return ContentSnapshot{
		TenantID:        uuid.New(),
		RelationID:      uuid.New(),
		ContentID:       uuid.New(),
		ContentRevision: now.Add(-time.Minute),
		Schema:          ContentSnapshotSchema,
		SubjectDigest:   digest,
		CapturedAt:      now,
	}
}
