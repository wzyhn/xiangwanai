// Package refundpostgres persists Xiangwan manual Refund cases in PostgreSQL.
package refundpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

var (
	ErrRefundCaseNotFound        = errors.New("xiangwan Refund case not found")
	ErrRefundCaseVersionConflict = errors.New("xiangwan Refund case version conflict")
)

type DBTX interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Repository struct {
	db queryExecutor
}

func NewRepository(db DBTX) *Repository {
	return &Repository{db: sqlQueryExecutor{db: db}}
}

type rowScanner interface {
	Scan(...any) error
}

type rowsScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

type queryExecutor interface {
	queryContext(context.Context, string, ...any) (rowsScanner, error)
	queryRowContext(context.Context, string, ...any) rowScanner
}

type sqlQueryExecutor struct {
	db DBTX
}

func (executor sqlQueryExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return executor.db.QueryContext(ctx, query, args...)
}

func (executor sqlQueryExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return executor.db.QueryRowContext(ctx, query, args...)
}

func (repository *Repository) Create(
	ctx context.Context,
	value refund.Case,
) (refund.Case, error) {
	created, err := scanRefundCase(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_refund_cases (
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13,
    $14, $15, $16,
    $17, $18, $19, $20,
    $21, $22, $23
)
RETURNING
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
`,
		value.ID,
		value.TenantID,
		value.OrderID,
		value.RegistrationID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.RefundStatus,
		value.ReasonCode,
		value.IdempotencyKey,
		value.RequestedRefundCents,
		value.SuccessfulRefundCents,
		value.ProcessingStartedAt,
		value.ResolvedAt,
		value.HandledBy,
		value.ExternalRefundID,
		value.EvidenceReference,
		value.OperatorNote,
		value.FailureReason,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return refund.Case{}, fmt.Errorf("create xiangwan Refund case: %w", err)
	}
	return created, nil
}

func (repository *Repository) Get(
	ctx context.Context,
	tenantID uuid.UUID,
	refundCaseID uuid.UUID,
) (refund.Case, error) {
	return repository.get(ctx, `
SELECT
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
FROM xiangwan_refund_cases
WHERE tenant_id = $1 AND id = $2
`, tenantID, refundCaseID)
}

func (repository *Repository) GetForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	refundCaseID uuid.UUID,
) (refund.Case, error) {
	return repository.get(ctx, `
SELECT
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
FROM xiangwan_refund_cases
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, refundCaseID)
}

func (repository *Repository) GetByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (refund.Case, error) {
	return repository.getByOrder(ctx, tenantID, orderID, false)
}

func (repository *Repository) GetByOrderForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (refund.Case, error) {
	return repository.getByOrder(ctx, tenantID, orderID, true)
}

func (repository *Repository) GetByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	idempotencyKey string,
) (refund.Case, error) {
	return repository.get(ctx, `
SELECT
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
FROM xiangwan_refund_cases
WHERE tenant_id = $1 AND idempotency_key = $2
`, tenantID, idempotencyKey)
}

func (repository *Repository) Update(
	ctx context.Context,
	value refund.Case,
	expectedVersion int64,
) (refund.Case, error) {
	updated, err := scanRefundCase(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_refund_cases
SET refund_status = $3,
    successful_refund_cents = $4,
    processing_started_at = $5,
    resolved_at = $6,
    handled_by = $7,
    external_refund_id = $8,
    evidence_reference = $9,
    operator_note = $10,
    failure_reason = $11,
    version = version + 1,
    updated_at = $12
WHERE tenant_id = $1
  AND id = $2
  AND version = $13
RETURNING
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
`,
		value.TenantID,
		value.ID,
		value.RefundStatus,
		value.SuccessfulRefundCents,
		value.ProcessingStartedAt,
		value.ResolvedAt,
		value.HandledBy,
		value.ExternalRefundID,
		value.EvidenceReference,
		value.OperatorNote,
		value.FailureReason,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return refund.Case{}, ErrRefundCaseVersionConflict
	}
	if err != nil {
		return refund.Case{}, fmt.Errorf("update xiangwan Refund case: %w", err)
	}
	return updated, nil
}

func (repository *Repository) getByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
	forUpdate bool,
) (refund.Case, error) {
	query := `
SELECT
    id, tenant_id, order_id, registration_id, series_id, instance_id,
    session_id, principal_id, refund_status, reason_code, idempotency_key,
    requested_refund_cents, successful_refund_cents,
    processing_started_at, resolved_at, handled_by,
    external_refund_id, evidence_reference, operator_note, failure_reason,
    version, created_at, updated_at
FROM xiangwan_refund_cases
WHERE tenant_id = $1 AND order_id = $2
`
	if forUpdate {
		query += "FOR UPDATE\n"
	}
	return repository.get(ctx, query, tenantID, orderID)
}

func (repository *Repository) get(
	ctx context.Context,
	query string,
	args ...any,
) (refund.Case, error) {
	value, err := scanRefundCase(repository.db.queryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return refund.Case{}, ErrRefundCaseNotFound
	}
	if err != nil {
		return refund.Case{}, fmt.Errorf("get xiangwan Refund case: %w", err)
	}
	return value, nil
}

func scanRefundCase(row rowScanner) (refund.Case, error) {
	var value refund.Case
	var processingStartedAt sql.NullTime
	var resolvedAt sql.NullTime
	var handledBy uuid.NullUUID
	var externalRefundID sql.NullString
	var evidenceReference sql.NullString
	var operatorNote sql.NullString
	var failureReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.OrderID,
		&value.RegistrationID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PrincipalID,
		&value.RefundStatus,
		&value.ReasonCode,
		&value.IdempotencyKey,
		&value.RequestedRefundCents,
		&value.SuccessfulRefundCents,
		&processingStartedAt,
		&resolvedAt,
		&handledBy,
		&externalRefundID,
		&evidenceReference,
		&operatorNote,
		&failureReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return refund.Case{}, err
	}
	value.ProcessingStartedAt = nullTimePointer(processingStartedAt)
	value.ResolvedAt = nullTimePointer(resolvedAt)
	value.HandledBy = nullUUIDPointer(handledBy)
	value.ExternalRefundID = nullStringPointer(externalRefundID)
	value.EvidenceReference = nullStringPointer(evidenceReference)
	value.OperatorNote = nullStringPointer(operatorNote)
	value.FailureReason = nullStringPointer(failureReason)
	return value, nil
}

func nullUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	return &value.UUID
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
