package refundpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

var ErrRefundEventNotFound = errors.New("xiangwan Refund event not found")

func (repository *Repository) CreateEvent(
	ctx context.Context,
	value refund.Event,
) (refund.Event, error) {
	created, err := scanRefundEvent(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_refund_events (
    id, tenant_id, refund_case_id, order_id,
    event_sequence, event_type, idempotency_key,
    from_status, to_status, actor_id,
    successful_refund_cents, external_refund_id, evidence_reference,
    operator_note, failure_reason,
    occurred_at, resulting_refund_version, created_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7,
    $8, $9, $10,
    $11, $12, $13,
    $14, $15,
    $16, $17, $18
)
RETURNING
    id, tenant_id, refund_case_id, order_id,
    event_sequence, event_type, idempotency_key,
    from_status, to_status, actor_id,
    successful_refund_cents, external_refund_id, evidence_reference,
    operator_note, failure_reason,
    occurred_at, resulting_refund_version, created_at
`,
		value.ID,
		value.TenantID,
		value.RefundCaseID,
		value.OrderID,
		value.EventSequence,
		value.EventType,
		value.IdempotencyKey,
		value.FromStatus,
		value.ToStatus,
		value.ActorID,
		value.SuccessfulRefundCents,
		value.ExternalRefundID,
		value.EvidenceReference,
		value.OperatorNote,
		value.FailureReason,
		value.OccurredAt,
		value.ResultingRefundVersion,
		value.CreatedAt,
	))
	if err != nil {
		return refund.Event{}, fmt.Errorf("create xiangwan Refund event: %w", err)
	}
	return created, nil
}

func (repository *Repository) GetEvent(
	ctx context.Context,
	tenantID uuid.UUID,
	eventID uuid.UUID,
) (refund.Event, error) {
	return repository.getEvent(ctx, `
SELECT
    id, tenant_id, refund_case_id, order_id,
    event_sequence, event_type, idempotency_key,
    from_status, to_status, actor_id,
    successful_refund_cents, external_refund_id, evidence_reference,
    operator_note, failure_reason,
    occurred_at, resulting_refund_version, created_at
FROM xiangwan_refund_events
WHERE tenant_id = $1 AND id = $2
`, tenantID, eventID)
}

func (repository *Repository) GetEventByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	refundCaseID uuid.UUID,
	idempotencyKey string,
) (refund.Event, error) {
	return repository.getEvent(ctx, `
SELECT
    id, tenant_id, refund_case_id, order_id,
    event_sequence, event_type, idempotency_key,
    from_status, to_status, actor_id,
    successful_refund_cents, external_refund_id, evidence_reference,
    operator_note, failure_reason,
    occurred_at, resulting_refund_version, created_at
FROM xiangwan_refund_events
WHERE tenant_id = $1
  AND refund_case_id = $2
  AND idempotency_key = $3
`, tenantID, refundCaseID, idempotencyKey)
}

func (repository *Repository) getEvent(
	ctx context.Context,
	query string,
	args ...any,
) (refund.Event, error) {
	value, err := scanRefundEvent(repository.db.queryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return refund.Event{}, ErrRefundEventNotFound
	}
	if err != nil {
		return refund.Event{}, fmt.Errorf("get xiangwan Refund event: %w", err)
	}
	return value, nil
}

func scanRefundEvent(row rowScanner) (refund.Event, error) {
	var value refund.Event
	var externalRefundID sql.NullString
	var evidenceReference sql.NullString
	var operatorNote sql.NullString
	var failureReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.RefundCaseID,
		&value.OrderID,
		&value.EventSequence,
		&value.EventType,
		&value.IdempotencyKey,
		&value.FromStatus,
		&value.ToStatus,
		&value.ActorID,
		&value.SuccessfulRefundCents,
		&externalRefundID,
		&evidenceReference,
		&operatorNote,
		&failureReason,
		&value.OccurredAt,
		&value.ResultingRefundVersion,
		&value.CreatedAt,
	)
	if err != nil {
		return refund.Event{}, err
	}
	value.ExternalRefundID = nullStringPointer(externalRefundID)
	value.EvidenceReference = nullStringPointer(evidenceReference)
	value.OperatorNote = nullStringPointer(operatorNote)
	value.FailureReason = nullStringPointer(failureReason)
	return value, nil
}
