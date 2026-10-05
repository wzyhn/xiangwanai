package resource

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const ContentSnapshotSchema = "xiangwan-resource-snapshot-v1"

type ContentSnapshot struct {
	TenantID        uuid.UUID
	RelationID      uuid.UUID
	ContentID       uuid.UUID
	ContentRevision time.Time
	Schema          string
	SubjectDigest   Digest
	CapturedAt      time.Time
}

var ErrInvalidContentSnapshot = errors.New(
	"invalid xiangwan resource Content snapshot",
)

func ValidateContentSnapshot(value ContentSnapshot) error {
	if value.TenantID == uuid.Nil || value.RelationID == uuid.Nil ||
		value.ContentID == uuid.Nil || value.ContentRevision.IsZero() ||
		value.CapturedAt.IsZero() ||
		value.ContentRevision.After(value.CapturedAt) ||
		value.Schema != ContentSnapshotSchema || zeroDigest(value.SubjectDigest) {
		return ErrInvalidContentSnapshot
	}
	return nil
}
