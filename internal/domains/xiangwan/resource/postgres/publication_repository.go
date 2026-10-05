package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrPublicationNotFound = errors.New(
		"xiangwan resource publication not found",
	)
	ErrPublicationExists = errors.New(
		"xiangwan ResourceRelation is already published",
	)
	ErrPublicationFactsConflict = errors.New(
		"xiangwan resource publication facts conflict",
	)
	ErrPublicationStateChanged = errors.New(
		"xiangwan resource publication state changed",
	)
)

type PublishResult struct {
	Publication resource.Publication
	Duplicate   bool
}

func (repository *Repository) Publish(
	ctx context.Context,
	value resource.Publication,
) (PublishResult, error) {
	if repository == nil || repository.db == nil ||
		resource.ValidatePublication(value) != nil {
		return PublishResult{}, resource.ErrInvalidPublication
	}
	existing, err := repository.GetPublicationByIdempotencyKey(
		ctx,
		value.TenantID,
		value.PublishedBy,
		value.IdempotencyKey,
	)
	switch {
	case err == nil:
		return replayPublishResult(existing, value)
	case errors.Is(err, ErrPublicationNotFound):
	case err != nil:
		return PublishResult{}, err
	}
	if _, err := repository.GetPublicationByRelation(
		ctx,
		value.TenantID,
		value.RelationID,
	); err == nil {
		return PublishResult{}, ErrPublicationExists
	} else if !errors.Is(err, ErrPublicationNotFound) {
		return PublishResult{}, err
	}
	result, err := repository.db.execContext(ctx, `
INSERT INTO xiangwan_resource_publications (
    id, tenant_id, relation_id, content_id, content_revision_at,
    approval_observation_id, access_policy, expected_target_version,
    published_by, idempotency_key, published_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11
)
ON CONFLICT (tenant_id, published_by, idempotency_key) DO NOTHING
`,
		value.ID, value.TenantID, value.RelationID, value.ContentID,
		value.ContentRevision, value.ApprovalObservationID,
		value.AccessPolicy, value.ExpectedTargetVersion, value.PublishedBy,
		value.IdempotencyKey, value.PublishedAt,
	)
	if err != nil {
		return PublishResult{}, classifyPublicationWriteError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return PublishResult{}, fmt.Errorf(
			"read xiangwan publication insert result: %w",
			err,
		)
	}
	if affected == 1 {
		return PublishResult{Publication: value}, nil
	}
	if affected != 0 {
		return PublishResult{}, ErrPublicationFactsConflict
	}
	existing, err = repository.GetPublicationByIdempotencyKey(
		ctx,
		value.TenantID,
		value.PublishedBy,
		value.IdempotencyKey,
	)
	if err != nil {
		return PublishResult{}, err
	}
	return replayPublishResult(existing, value)
}

func replayPublishResult(
	existing resource.Publication,
	requested resource.Publication,
) (PublishResult, error) {
	if !resource.SamePublicationIntent(existing, requested) {
		return PublishResult{}, resource.ErrPublicationIntentConflict
	}
	return PublishResult{Publication: existing, Duplicate: true}, nil
}

func (repository *Repository) GetPublicationByID(
	ctx context.Context,
	tenantID uuid.UUID,
	publicationID uuid.UUID,
) (resource.Publication, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || publicationID == uuid.Nil {
		return resource.Publication{}, resource.ErrInvalidPublication
	}
	return scanPublication(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, relation_id, content_id, content_revision_at,
    approval_observation_id, access_policy, expected_target_version,
    published_by, idempotency_key, published_at
FROM xiangwan_resource_publications
WHERE tenant_id = $1 AND id = $2
`, tenantID, publicationID))
}

func (repository *Repository) GetPublicationByRelation(
	ctx context.Context,
	tenantID uuid.UUID,
	relationID uuid.UUID,
) (resource.Publication, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || relationID == uuid.Nil {
		return resource.Publication{}, resource.ErrInvalidPublication
	}
	return scanPublication(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, relation_id, content_id, content_revision_at,
    approval_observation_id, access_policy, expected_target_version,
    published_by, idempotency_key, published_at
FROM xiangwan_resource_publications
WHERE tenant_id = $1 AND relation_id = $2
`, tenantID, relationID))
}

func (repository *Repository) GetPublicationByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	publishedBy uuid.UUID,
	idempotencyKey string,
) (resource.Publication, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || publishedBy == uuid.Nil ||
		idempotencyKey == "" {
		return resource.Publication{}, resource.ErrInvalidPublication
	}
	return scanPublication(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, relation_id, content_id, content_revision_at,
    approval_observation_id, access_policy, expected_target_version,
    published_by, idempotency_key, published_at
FROM xiangwan_resource_publications
WHERE tenant_id = $1
  AND published_by = $2
  AND idempotency_key = $3
`, tenantID, publishedBy, idempotencyKey))
}

func scanPublication(row rowScanner) (resource.Publication, error) {
	var value resource.Publication
	if err := row.Scan(
		&value.ID, &value.TenantID, &value.RelationID, &value.ContentID,
		&value.ContentRevision, &value.ApprovalObservationID,
		&value.AccessPolicy, &value.ExpectedTargetVersion,
		&value.PublishedBy, &value.IdempotencyKey, &value.PublishedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resource.Publication{}, ErrPublicationNotFound
		}
		return resource.Publication{}, fmt.Errorf(
			"scan xiangwan resource publication: %w",
			err,
		)
	}
	value.ContentRevision = value.ContentRevision.UTC()
	value.PublishedAt = value.PublishedAt.UTC()
	if resource.ValidatePublication(value) != nil {
		return resource.Publication{}, ErrPublicationFactsConflict
	}
	return value, nil
}

func classifyPublicationWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return fmt.Errorf("%w: %v", ErrPublicationExists, err)
		case "23503", "23514", "40P01":
			return fmt.Errorf("%w: %v", ErrPublicationFactsConflict, err)
		case "40001":
			return fmt.Errorf("%w: %v", ErrPublicationStateChanged, err)
		}
	}
	return err
}
