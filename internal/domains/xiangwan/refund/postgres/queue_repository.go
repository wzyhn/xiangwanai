package refundpostgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

var (
	ErrInvalidRefundQueueFilter = errors.New("invalid xiangwan Refund queue filter")
	ErrInvalidRefundQueueCursor = errors.New("invalid xiangwan Refund queue cursor")
	ErrStaleRefundQueueCursor   = errors.New("stale xiangwan Refund queue cursor")
)

const refundQueueItemProjection = `
    refund_case.id,
    refund_case.tenant_id,
    refund_case.order_id,
    refund_case.registration_id,
    refund_case.series_id,
    refund_case.instance_id,
    refund_case.session_id,
    refund_case.principal_id,
    refund_case.refund_status,
    refund_case.reason_code,
    refund_case.idempotency_key,
    refund_case.requested_refund_cents,
    refund_case.successful_refund_cents,
    refund_case.processing_started_at,
    refund_case.resolved_at,
    refund_case.handled_by,
    refund_case.external_refund_id,
    refund_case.evidence_reference,
    refund_case.operator_note,
    refund_case.failure_reason,
    refund_case.version,
    refund_case.created_at,
    refund_case.updated_at,
    activity_series.title,
    activity_instance.title,
    activity_session.title
`

func (repository *Repository) ListQueue(
	ctx context.Context,
	filter refund.QueueFilter,
) (refund.QueuePage, error) {
	normalized, cursor, err := normalizeRefundQueueFilter(filter)
	if err != nil {
		return refund.QueuePage{}, err
	}

	var query strings.Builder
	query.WriteString("SELECT")
	query.WriteString(refundQueueItemProjection)
	query.WriteString(`
FROM xiangwan_refund_cases AS refund_case
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = refund_case.tenant_id
 AND activity_series.id = refund_case.series_id
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = refund_case.tenant_id
 AND activity_instance.series_id = refund_case.series_id
 AND activity_instance.id = refund_case.instance_id
JOIN xiangwan_activity_sessions AS activity_session
  ON activity_session.tenant_id = refund_case.tenant_id
 AND activity_session.instance_id = refund_case.instance_id
 AND activity_session.id = refund_case.session_id
WHERE refund_case.tenant_id = $1
  AND refund_case.refund_status IN ('pending_manual', 'processing', 'failed')
  AND refund_case.refund_status = $2
`)
	args := []any{normalized.TenantID, normalized.Status}
	if cursor != nil {
		query.WriteString(`  AND (refund_case.created_at, refund_case.id) > ($3, $4)
`)
		args = append(args, cursor.CreatedAt, cursor.RefundCaseID)
		query.WriteString(`ORDER BY refund_case.created_at ASC, refund_case.id ASC
LIMIT $5
`)
	} else {
		query.WriteString(`ORDER BY refund_case.created_at ASC, refund_case.id ASC
LIMIT $3
`)
	}
	args = append(args, normalized.Limit+1)

	rows, err := repository.db.queryContext(ctx, query.String(), args...)
	if err != nil {
		return refund.QueuePage{}, fmt.Errorf("list xiangwan Refund queue: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	items := make([]refund.QueueItem, 0, normalized.Limit+1)
	for rows.Next() {
		item, scanErr := scanRefundQueueItem(rows)
		if scanErr != nil {
			return refund.QueuePage{}, fmt.Errorf("scan xiangwan Refund queue: %w", scanErr)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return refund.QueuePage{}, fmt.Errorf("iterate xiangwan Refund queue: %w", err)
	}

	nextCursor := ""
	if len(items) > normalized.Limit {
		items = items[:normalized.Limit]
		nextCursor, err = encodeRefundQueueCursor(normalized, items[len(items)-1].Case)
		if err != nil {
			return refund.QueuePage{}, err
		}
	}
	return refund.QueuePage{
		Items:        items,
		ActiveStatus: normalized.Status,
		NextCursor:   nextCursor,
	}, nil
}

// GetCaseDetail reads the current projection first, then caps the event query
// at that version. A concurrent transition therefore cannot produce a timeline
// newer than the Case returned to the caller.
func (repository *Repository) GetCaseDetail(
	ctx context.Context,
	tenantID uuid.UUID,
	refundCaseID uuid.UUID,
) (refund.CaseDetail, error) {
	if tenantID == uuid.Nil || refundCaseID == uuid.Nil {
		return refund.CaseDetail{}, ErrInvalidRefundQueueFilter
	}
	item, err := scanRefundQueueItem(repository.db.queryRowContext(ctx, `
SELECT`+refundQueueItemProjection+`
FROM xiangwan_refund_cases AS refund_case
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = refund_case.tenant_id
 AND activity_series.id = refund_case.series_id
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = refund_case.tenant_id
 AND activity_instance.series_id = refund_case.series_id
 AND activity_instance.id = refund_case.instance_id
JOIN xiangwan_activity_sessions AS activity_session
  ON activity_session.tenant_id = refund_case.tenant_id
 AND activity_session.instance_id = refund_case.instance_id
 AND activity_session.id = refund_case.session_id
WHERE refund_case.tenant_id = $1
  AND refund_case.id = $2
`, tenantID, refundCaseID))
	if errors.Is(err, sql.ErrNoRows) {
		return refund.CaseDetail{}, ErrRefundCaseNotFound
	}
	if err != nil {
		return refund.CaseDetail{}, fmt.Errorf("get xiangwan Refund detail: %w", err)
	}

	rows, err := repository.db.queryContext(ctx, `
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
  AND resulting_refund_version <= $3
ORDER BY event_sequence ASC
`, tenantID, refundCaseID, item.Case.Version)
	if err != nil {
		return refund.CaseDetail{}, fmt.Errorf("list xiangwan Refund detail events: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	events := make([]refund.Event, 0)
	for rows.Next() {
		event, scanErr := scanRefundEvent(rows)
		if scanErr != nil {
			return refund.CaseDetail{}, fmt.Errorf(
				"scan xiangwan Refund detail event: %w",
				scanErr,
			)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return refund.CaseDetail{}, fmt.Errorf(
			"iterate xiangwan Refund detail events: %w",
			err,
		)
	}
	return refund.CaseDetail{Item: item, Events: events}, nil
}

func scanRefundQueueItem(row rowScanner) (refund.QueueItem, error) {
	var item refund.QueueItem
	var processingStartedAt sql.NullTime
	var resolvedAt sql.NullTime
	var handledBy uuid.NullUUID
	var externalRefundID sql.NullString
	var evidenceReference sql.NullString
	var operatorNote sql.NullString
	var failureReason sql.NullString
	err := row.Scan(
		&item.Case.ID,
		&item.Case.TenantID,
		&item.Case.OrderID,
		&item.Case.RegistrationID,
		&item.Case.SeriesID,
		&item.Case.InstanceID,
		&item.Case.SessionID,
		&item.Case.PrincipalID,
		&item.Case.RefundStatus,
		&item.Case.ReasonCode,
		&item.Case.IdempotencyKey,
		&item.Case.RequestedRefundCents,
		&item.Case.SuccessfulRefundCents,
		&processingStartedAt,
		&resolvedAt,
		&handledBy,
		&externalRefundID,
		&evidenceReference,
		&operatorNote,
		&failureReason,
		&item.Case.Version,
		&item.Case.CreatedAt,
		&item.Case.UpdatedAt,
		&item.SeriesTitle,
		&item.InstanceTitle,
		&item.SessionTitle,
	)
	if err != nil {
		return refund.QueueItem{}, err
	}
	item.Case.ProcessingStartedAt = nullTimePointer(processingStartedAt)
	item.Case.ResolvedAt = nullTimePointer(resolvedAt)
	item.Case.HandledBy = nullUUIDPointer(handledBy)
	item.Case.ExternalRefundID = nullStringPointer(externalRefundID)
	item.Case.EvidenceReference = nullStringPointer(evidenceReference)
	item.Case.OperatorNote = nullStringPointer(operatorNote)
	item.Case.FailureReason = nullStringPointer(failureReason)
	return item, nil
}

type decodedRefundQueueCursor struct {
	Version      int           `json:"v"`
	TenantID     uuid.UUID     `json:"tenant_id"`
	Status       refund.Status `json:"status"`
	CreatedAt    time.Time     `json:"created_at"`
	RefundCaseID uuid.UUID     `json:"refund_case_id"`
}

func normalizeRefundQueueFilter(
	filter refund.QueueFilter,
) (refund.QueueFilter, *decodedRefundQueueCursor, error) {
	if filter.TenantID == uuid.Nil {
		return refund.QueueFilter{}, nil, ErrInvalidRefundQueueFilter
	}
	if filter.Status == "" {
		filter.Status = refund.StatusPendingManual
	}
	if !actionableRefundQueueStatus(filter.Status) {
		return refund.QueueFilter{}, nil, ErrInvalidRefundQueueFilter
	}
	switch {
	case filter.Limit == 0:
		filter.Limit = refund.DefaultQueueLimit
	case filter.Limit < 1 || filter.Limit > refund.MaxQueueLimit:
		return refund.QueueFilter{}, nil, ErrInvalidRefundQueueFilter
	}
	if filter.Cursor == "" {
		return filter, nil, nil
	}
	cursor, err := decodeRefundQueueCursor(filter.Cursor)
	if err != nil {
		return refund.QueueFilter{}, nil, err
	}
	if cursor.TenantID != filter.TenantID || cursor.Status != filter.Status {
		return refund.QueueFilter{}, nil, ErrStaleRefundQueueCursor
	}
	return filter, &cursor, nil
}

func actionableRefundQueueStatus(status refund.Status) bool {
	switch status {
	case refund.StatusPendingManual, refund.StatusProcessing, refund.StatusFailed:
		return true
	default:
		return false
	}
}

func encodeRefundQueueCursor(
	filter refund.QueueFilter,
	value refund.Case,
) (string, error) {
	encoded, err := json.Marshal(decodedRefundQueueCursor{
		Version:      1,
		TenantID:     filter.TenantID,
		Status:       filter.Status,
		CreatedAt:    value.CreatedAt.UTC(),
		RefundCaseID: value.ID,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode", ErrInvalidRefundQueueCursor)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeRefundQueueCursor(value string) (decodedRefundQueueCursor, error) {
	if len(value) > 1024 {
		return decodedRefundQueueCursor{}, fmt.Errorf(
			"%w: payload is too large",
			ErrInvalidRefundQueueCursor,
		)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return decodedRefundQueueCursor{}, fmt.Errorf(
			"%w: malformed base64",
			ErrInvalidRefundQueueCursor,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var cursor decodedRefundQueueCursor
	if err := decoder.Decode(&cursor); err != nil {
		return decodedRefundQueueCursor{}, fmt.Errorf(
			"%w: malformed payload",
			ErrInvalidRefundQueueCursor,
		)
	}
	if err := ensureRefundQueueCursorEOF(decoder); err != nil {
		return decodedRefundQueueCursor{}, err
	}
	if cursor.Version != 1 ||
		cursor.TenantID == uuid.Nil ||
		!actionableRefundQueueStatus(cursor.Status) ||
		cursor.CreatedAt.IsZero() ||
		cursor.RefundCaseID == uuid.Nil {
		return decodedRefundQueueCursor{}, fmt.Errorf(
			"%w: invalid fields",
			ErrInvalidRefundQueueCursor,
		)
	}
	return cursor, nil
}

func ensureRefundQueueCursorEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing payload", ErrInvalidRefundQueueCursor)
	}
	return nil
}
