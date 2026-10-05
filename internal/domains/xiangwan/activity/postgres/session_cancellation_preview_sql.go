package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func (tx *sessionCancellationSQLTransaction) getPreviewByKey(
	ctx context.Context,
	tenantID uuid.UUID,
	idempotencyKey string,
) (activity.SessionCancellationPreview, error) {
	value, err := scanSessionCancellationPreview(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id,
    requested_by, idempotency_key, snapshot_digest,
    expected_session_version, cancelled_registration_count,
    confirmed_registration_count, active_hold_count,
    free_registration_count, paid_refund_registration_count,
    pending_order_count, unknown_payment_count,
    refund_case_count, requested_refund_cents, coupon_adjustment_count,
    notification_strategy, expires_at, consumed_at, created_at,
    parent_instance_preview_id
FROM xiangwan_session_cancellation_previews
WHERE tenant_id = $1 AND idempotency_key = $2
FOR UPDATE
`, tenantID, idempotencyKey))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.SessionCancellationPreview{},
			errSessionCancellationPreviewNotFound
	}
	if err != nil {
		return activity.SessionCancellationPreview{}, fmt.Errorf(
			"get xiangwan Session cancellation preview by key: %w",
			err,
		)
	}
	return value, nil
}

func (tx *sessionCancellationSQLTransaction) lockPreview(
	ctx context.Context,
	tenantID uuid.UUID,
	previewID uuid.UUID,
) (activity.SessionCancellationPreview, error) {
	value, err := scanSessionCancellationPreview(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id,
    requested_by, idempotency_key, snapshot_digest,
    expected_session_version, cancelled_registration_count,
    confirmed_registration_count, active_hold_count,
    free_registration_count, paid_refund_registration_count,
    pending_order_count, unknown_payment_count,
    refund_case_count, requested_refund_cents, coupon_adjustment_count,
    notification_strategy, expires_at, consumed_at, created_at,
    parent_instance_preview_id
FROM xiangwan_session_cancellation_previews
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, previewID))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.SessionCancellationPreview{},
			errSessionCancellationPreviewNotFound
	}
	if err != nil {
		return activity.SessionCancellationPreview{}, fmt.Errorf(
			"lock xiangwan Session cancellation preview: %w",
			err,
		)
	}
	return value, nil
}

func (tx *sessionCancellationSQLTransaction) createPreview(
	ctx context.Context,
	value activity.SessionCancellationPreview,
) (activity.SessionCancellationPreview, error) {
	created, err := scanSessionCancellationPreview(tx.tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_session_cancellation_previews (
    id, tenant_id, series_id, instance_id, session_id,
    requested_by, idempotency_key, snapshot_digest,
    expected_session_version, cancelled_registration_count,
    confirmed_registration_count, active_hold_count,
    free_registration_count, paid_refund_registration_count,
    pending_order_count, unknown_payment_count,
    refund_case_count, requested_refund_cents, coupon_adjustment_count,
    notification_strategy, expires_at, consumed_at, created_at,
    parent_instance_preview_id
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10,
    $11, $12,
    $13, $14,
    $15, $16,
    $17, $18, $19,
    $20, $21, $22, $23, $24
)
RETURNING
    id, tenant_id, series_id, instance_id, session_id,
    requested_by, idempotency_key, snapshot_digest,
    expected_session_version, cancelled_registration_count,
    confirmed_registration_count, active_hold_count,
    free_registration_count, paid_refund_registration_count,
    pending_order_count, unknown_payment_count,
    refund_case_count, requested_refund_cents, coupon_adjustment_count,
    notification_strategy, expires_at, consumed_at, created_at,
    parent_instance_preview_id
`,
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.RequestedBy,
		value.IdempotencyKey,
		value.SnapshotDigest,
		value.ExpectedSessionVersion,
		value.CancelledRegistrationCount,
		value.ConfirmedRegistrationCount,
		value.ActiveHoldCount,
		value.FreeRegistrationCount,
		value.PaidRefundRegistrationCount,
		value.PendingOrderCount,
		value.UnknownPaymentCount,
		value.RefundCaseCount,
		value.RequestedRefundCents,
		value.CouponAdjustmentCount,
		value.NotificationStrategy,
		value.ExpiresAt,
		value.ConsumedAt,
		value.CreatedAt,
		value.ParentInstancePreviewID,
	))
	if err != nil {
		return activity.SessionCancellationPreview{}, fmt.Errorf(
			"create xiangwan Session cancellation preview: %w",
			err,
		)
	}
	return created, nil
}

func (tx *sessionCancellationSQLTransaction) consumePreview(
	ctx context.Context,
	tenantID uuid.UUID,
	previewID uuid.UUID,
	consumedAt time.Time,
) (activity.SessionCancellationPreview, error) {
	updated, err := scanSessionCancellationPreview(tx.tx.QueryRowContext(ctx, `
UPDATE xiangwan_session_cancellation_previews
SET consumed_at = $3
WHERE tenant_id = $1
  AND id = $2
  AND consumed_at IS NULL
  AND created_at <= $3
  AND expires_at >= $3
RETURNING
    id, tenant_id, series_id, instance_id, session_id,
    requested_by, idempotency_key, snapshot_digest,
    expected_session_version, cancelled_registration_count,
    confirmed_registration_count, active_hold_count,
    free_registration_count, paid_refund_registration_count,
    pending_order_count, unknown_payment_count,
    refund_case_count, requested_refund_cents, coupon_adjustment_count,
    notification_strategy, expires_at, consumed_at, created_at,
    parent_instance_preview_id
`, tenantID, previewID, consumedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.SessionCancellationPreview{},
			ErrSessionCancellationPreviewConflict
	}
	if err != nil {
		return activity.SessionCancellationPreview{}, fmt.Errorf(
			"consume xiangwan Session cancellation preview: %w",
			err,
		)
	}
	return updated, nil
}

func scanSessionCancellationPreview(
	row rowScanner,
) (activity.SessionCancellationPreview, error) {
	var value activity.SessionCancellationPreview
	var consumedAt sql.NullTime
	var parentInstancePreviewID uuid.NullUUID
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.RequestedBy,
		&value.IdempotencyKey,
		&value.SnapshotDigest,
		&value.ExpectedSessionVersion,
		&value.CancelledRegistrationCount,
		&value.ConfirmedRegistrationCount,
		&value.ActiveHoldCount,
		&value.FreeRegistrationCount,
		&value.PaidRefundRegistrationCount,
		&value.PendingOrderCount,
		&value.UnknownPaymentCount,
		&value.RefundCaseCount,
		&value.RequestedRefundCents,
		&value.CouponAdjustmentCount,
		&value.NotificationStrategy,
		&value.ExpiresAt,
		&consumedAt,
		&value.CreatedAt,
		&parentInstancePreviewID,
	)
	if err != nil {
		return activity.SessionCancellationPreview{}, err
	}
	value.ConsumedAt = nullTimePointer(consumedAt)
	if parentInstancePreviewID.Valid {
		value.ParentInstancePreviewID = &parentInstancePreviewID.UUID
	}
	return value, nil
}
