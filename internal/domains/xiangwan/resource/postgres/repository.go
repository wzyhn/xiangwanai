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
	ErrRelationNotFound = errors.New(
		"xiangwan ResourceRelation not found",
	)
	ErrRelationExists = errors.New(
		"xiangwan ResourceRelation already exists",
	)
	ErrRelationFactsConflict = errors.New(
		"xiangwan ResourceRelation facts conflict",
	)
	ErrRelationTargetChanged = errors.New(
		"xiangwan ResourceRelation target changed",
	)
)

type CreateResult struct {
	Relation  resource.Relation
	Duplicate bool
}

type SQLDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Repository struct {
	db resourceExecutor
}

func NewRepository(db SQLDB) *Repository {
	return &Repository{db: sqlResourceExecutor{db: db}}
}

func (repository *Repository) CreateDraft(
	ctx context.Context,
	value resource.Relation,
) (CreateResult, error) {
	if repository == nil || repository.db == nil ||
		resource.ValidateRelation(value) != nil {
		return CreateResult{}, resource.ErrInvalidRelation
	}
	existing, err := repository.GetByIdempotencyKey(
		ctx,
		value.TenantID,
		value.CreatedBy,
		value.IdempotencyKey,
	)
	switch {
	case err == nil:
		return replayCreateResult(existing, value)
	case errors.Is(err, ErrRelationNotFound):
	case err != nil:
		return CreateResult{}, err
	}
	result, err := repository.db.execContext(ctx, `
INSERT INTO xiangwan_resource_relations (
    id, tenant_id, series_id, instance_id, session_id,
    relation_kind, content_id, content_revision_at, access_policy,
    sort_order, expected_target_version, created_by,
    idempotency_key, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12,
    $13, $14
)
ON CONFLICT (tenant_id, created_by, idempotency_key) DO NOTHING
`,
		value.ID, value.TenantID, value.SeriesID, value.InstanceID,
		value.SessionID, value.Kind, value.ContentID,
		value.ContentRevision, value.AccessPolicy, value.SortOrder,
		value.ExpectedTargetVersion, value.CreatedBy,
		value.IdempotencyKey, value.CreatedAt,
	)
	if err != nil {
		return CreateResult{}, classifyRelationWriteError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CreateResult{}, fmt.Errorf(
			"read xiangwan ResourceRelation insert result: %w",
			err,
		)
	}
	if affected == 1 {
		return CreateResult{Relation: value}, nil
	}
	if affected != 0 {
		return CreateResult{}, ErrRelationFactsConflict
	}
	existing, err = repository.GetByIdempotencyKey(
		ctx,
		value.TenantID,
		value.CreatedBy,
		value.IdempotencyKey,
	)
	if err != nil {
		return CreateResult{}, err
	}
	return replayCreateResult(existing, value)
}

func replayCreateResult(
	existing resource.Relation,
	requested resource.Relation,
) (CreateResult, error) {
	if !resource.SameCreateIntent(existing, requested) {
		return CreateResult{}, resource.ErrRelationIntentConflict
	}
	return CreateResult{Relation: existing, Duplicate: true}, nil
}

func (repository *Repository) GetByID(
	ctx context.Context,
	tenantID uuid.UUID,
	relationID uuid.UUID,
) (resource.Relation, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || relationID == uuid.Nil {
		return resource.Relation{}, resource.ErrInvalidRelation
	}
	return scanRelation(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id,
    relation_kind, content_id, content_revision_at, access_policy,
    sort_order, expected_target_version, created_by,
    idempotency_key, created_at
FROM xiangwan_resource_relations
WHERE tenant_id = $1 AND id = $2
`, tenantID, relationID))
}

func (repository *Repository) GetByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	createdBy uuid.UUID,
	idempotencyKey string,
) (resource.Relation, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || createdBy == uuid.Nil ||
		idempotencyKey == "" {
		return resource.Relation{}, resource.ErrInvalidRelation
	}
	return scanRelation(repository.db.queryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id,
    relation_kind, content_id, content_revision_at, access_policy,
    sort_order, expected_target_version, created_by,
    idempotency_key, created_at
FROM xiangwan_resource_relations
WHERE tenant_id = $1
  AND created_by = $2
  AND idempotency_key = $3
`, tenantID, createdBy, idempotencyKey))
}

type resourceExecutor interface {
	execContext(context.Context, string, ...any) (sql.Result, error)
	queryContext(
		context.Context,
		string,
		...any,
	) (resourceRowsScanner, error)
	queryRowContext(context.Context, string, ...any) rowScanner
}

type resourceRowsScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

type rowScanner interface {
	Scan(...any) error
}

type sqlResourceExecutor struct {
	db SQLDB
}

func (executor sqlResourceExecutor) execContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	if executor.db == nil {
		return nil, resource.ErrInvalidRelation
	}
	return executor.db.ExecContext(ctx, query, args...)
}

func (executor sqlResourceExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	if executor.db == nil {
		return errorRow{err: resource.ErrInvalidRelation}
	}
	return executor.db.QueryRowContext(ctx, query, args...)
}

func (executor sqlResourceExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (resourceRowsScanner, error) {
	if executor.db == nil {
		return nil, resource.ErrInvalidPublishedResource
	}
	return executor.db.QueryContext(ctx, query, args...)
}

type errorRow struct {
	err error
}

func (row errorRow) Scan(...any) error {
	return row.err
}

func scanRelation(row rowScanner) (resource.Relation, error) {
	var value resource.Relation
	var sessionID uuid.NullUUID
	if err := row.Scan(
		&value.ID, &value.TenantID, &value.SeriesID, &value.InstanceID,
		&sessionID, &value.Kind, &value.ContentID,
		&value.ContentRevision, &value.AccessPolicy, &value.SortOrder,
		&value.ExpectedTargetVersion, &value.CreatedBy,
		&value.IdempotencyKey, &value.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return resource.Relation{}, ErrRelationNotFound
		}
		return resource.Relation{}, fmt.Errorf(
			"scan xiangwan ResourceRelation: %w",
			err,
		)
	}
	if sessionID.Valid {
		value.SessionID = &sessionID.UUID
	}
	value.ContentRevision = value.ContentRevision.UTC()
	value.CreatedAt = value.CreatedAt.UTC()
	if resource.ValidateRelation(value) != nil {
		return resource.Relation{}, ErrRelationFactsConflict
	}
	return value, nil
}

func classifyRelationWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return fmt.Errorf("%w: %v", ErrRelationExists, err)
		case "23503", "23514":
			return fmt.Errorf("%w: %v", ErrRelationFactsConflict, err)
		case "40001":
			return fmt.Errorf("%w: %v", ErrRelationTargetChanged, err)
		case "40P01":
			return fmt.Errorf("%w: %v", ErrRelationFactsConflict, err)
		}
	}
	return err
}
