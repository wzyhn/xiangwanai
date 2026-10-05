package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

var (
	ErrContentSnapshotNotFound = errors.New(
		"xiangwan resource Content snapshot not found",
	)
	ErrContentSnapshotFactsConflict = errors.New(
		"xiangwan resource Content snapshot facts conflict",
	)
)

func (repository *Repository) GetContentSnapshotByRelation(
	ctx context.Context,
	tenantID uuid.UUID,
	relationID uuid.UUID,
) (resource.ContentSnapshot, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || relationID == uuid.Nil {
		return resource.ContentSnapshot{}, resource.ErrInvalidContentSnapshot
	}
	return scanContentSnapshot(repository.db.queryRowContext(ctx, `
SELECT
    tenant_id, relation_id, content_id, content_revision_at,
    snapshot_schema, subject_digest, captured_at
FROM xiangwan_resource_content_snapshots
WHERE tenant_id = $1 AND relation_id = $2
`, tenantID, relationID))
}

func scanContentSnapshot(row rowScanner) (resource.ContentSnapshot, error) {
	var value resource.ContentSnapshot
	var subjectDigest []byte
	if err := row.Scan(
		&value.TenantID, &value.RelationID, &value.ContentID,
		&value.ContentRevision, &value.Schema, &subjectDigest,
		&value.CapturedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resource.ContentSnapshot{}, ErrContentSnapshotNotFound
		}
		return resource.ContentSnapshot{}, fmt.Errorf(
			"scan xiangwan resource Content snapshot: %w",
			err,
		)
	}
	if len(subjectDigest) != resource.DigestSize {
		return resource.ContentSnapshot{}, ErrContentSnapshotFactsConflict
	}
	copy(value.SubjectDigest[:], subjectDigest)
	value.ContentRevision = value.ContentRevision.UTC()
	value.CapturedAt = value.CapturedAt.UTC()
	if resource.ValidateContentSnapshot(value) != nil {
		return resource.ContentSnapshot{}, ErrContentSnapshotFactsConflict
	}
	return value, nil
}
