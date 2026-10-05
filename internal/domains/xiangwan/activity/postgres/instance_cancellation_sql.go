package activitypostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	refundpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund/postgres"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
)

type instanceCancellationSQLResolver struct {
	db *sql.DB
}

func (resolver instanceCancellationSQLResolver) resolveInstanceCancellationTarget(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) (instanceCancellationTarget, error) {
	var target instanceCancellationTarget
	err := resolver.db.QueryRowContext(ctx, `
SELECT tenant_id, series_id, id
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
`, tenantID, instanceID).Scan(
		&target.TenantID,
		&target.SeriesID,
		&target.InstanceID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return instanceCancellationTarget{}, errInstanceCancellationTargetNotFound
	}
	if err != nil {
		return instanceCancellationTarget{}, fmt.Errorf(
			"resolve xiangwan Instance cancellation target: %w",
			err,
		)
	}
	return target, nil
}

type instanceCancellationSQLTransactionStarter struct {
	db            *sql.DB
	authorization InstanceCancellationAuthorization
}

func (starter instanceCancellationSQLTransactionStarter) beginInstanceCancellationTx(
	ctx context.Context,
	options *sql.TxOptions,
) (instanceCancellationTransaction, error) {
	sqlTx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	base := &sessionCancellationSQLTransaction{
		tx:            sqlTx,
		activities:    NewRepository(sqlTx),
		registrations: registrationpostgres.NewRepository(sqlTx),
		payments:      paymentpostgres.NewRepository(sqlTx),
		refunds:       refundpostgres.NewRepository(sqlTx),
	}
	return &instanceCancellationSQLTransaction{
		sessionCancellationSQLTransaction: base,
		authorization:                     starter.authorization,
	}, nil
}

type instanceCancellationSQLTransaction struct {
	*sessionCancellationSQLTransaction
	authorization InstanceCancellationAuthorization
}

func (tx *instanceCancellationSQLTransaction) authorizeInstanceCancellation(
	ctx context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	identityLinkID uuid.UUID,
) error {
	if tx.authorization == nil {
		return nil
	}
	if err := tx.authorization(ctx, tx.tx, tenantID, actorID, identityLinkID); err != nil {
		return err
	}
	return nil
}

func (tx *instanceCancellationSQLTransaction) lockInstanceRecord(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (activity.Instance, error) {
	value, err := scanInstance(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, issue_no, title, status,
    activity_type, TO_JSON(quick_tag_codes), cover_image_url, detail_blocks,
    publication_version, presentation_revision,
    scheduled_at, published_at, completed_at, version, created_at, updated_at
FROM xiangwan_activity_instances
WHERE tenant_id = $1
  AND series_id = $2
  AND id = $3
FOR UPDATE
`, tenantID, seriesID, instanceID))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Instance{}, ErrInstanceCancellationTransaction
	}
	if err != nil {
		return activity.Instance{}, fmt.Errorf(
			"lock xiangwan cancellation Instance record: %w",
			err,
		)
	}
	return value, nil
}

func (tx *instanceCancellationSQLTransaction) listInstanceSessions(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) ([]activity.Session, error) {
	rows, err := tx.tx.QueryContext(ctx, `
SELECT
    id, tenant_id, instance_id, title, status,
    registration_start_at, registration_end_at, session_start_at, session_end_at,
    capacity, group_minimum, low_stock_threshold, price_cents,
    delivery_mode, area_code, venue_name, address, longitude, latitude,
    online_participation_mode, online_participation_compliant,
    confirmed_registration_count, active_hold_count,
    sort_order, published_at, version, created_at, updated_at
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2
ORDER BY id
FOR UPDATE
`, tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf(
			"lock xiangwan Instance cancellation Sessions: %w",
			err,
		)
	}
	defer func() { _ = rows.Close() }()
	values := make([]activity.Session, 0)
	for rows.Next() {
		value, scanErr := scanSession(rows)
		if scanErr != nil {
			return nil, fmt.Errorf(
				"scan xiangwan Instance cancellation Session: %w",
				scanErr,
			)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate xiangwan Instance cancellation Sessions: %w",
			err,
		)
	}
	return values, nil
}

func (tx *instanceCancellationSQLTransaction) getInstancePreviewByKey(
	ctx context.Context,
	tenantID uuid.UUID,
	idempotencyKey string,
) (activity.InstanceCancellationPreview, error) {
	return tx.queryInstancePreview(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, requested_by,
    idempotency_key, snapshot_digest, expected_instance_version,
    session_count, target_session_count, already_cancelled_session_count,
    cancelled_registration_count, confirmed_registration_count,
    active_hold_count, free_registration_count,
    paid_refund_registration_count, pending_order_count,
    unknown_payment_count, refund_case_count, requested_refund_cents,
    coupon_adjustment_count, cancellation_reason,
    notification_strategy, session_impacts,
    expires_at, consumed_at, created_at
FROM xiangwan_instance_cancellation_previews
WHERE tenant_id = $1 AND idempotency_key = $2
FOR UPDATE
`, tenantID, idempotencyKey)
}

func (tx *instanceCancellationSQLTransaction) lockInstancePreview(
	ctx context.Context,
	tenantID uuid.UUID,
	previewID uuid.UUID,
) (activity.InstanceCancellationPreview, error) {
	return tx.queryInstancePreview(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, requested_by,
    idempotency_key, snapshot_digest, expected_instance_version,
    session_count, target_session_count, already_cancelled_session_count,
    cancelled_registration_count, confirmed_registration_count,
    active_hold_count, free_registration_count,
    paid_refund_registration_count, pending_order_count,
    unknown_payment_count, refund_case_count, requested_refund_cents,
    coupon_adjustment_count, cancellation_reason,
    notification_strategy, session_impacts,
    expires_at, consumed_at, created_at
FROM xiangwan_instance_cancellation_previews
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, previewID)
}

func (tx *instanceCancellationSQLTransaction) queryInstancePreview(
	ctx context.Context,
	query string,
	arguments ...any,
) (activity.InstanceCancellationPreview, error) {
	value, err := scanInstanceCancellationPreview(
		tx.tx.QueryRowContext(ctx, query, arguments...),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.InstanceCancellationPreview{},
			errInstanceCancellationPreviewNotFound
	}
	if err != nil {
		return activity.InstanceCancellationPreview{}, fmt.Errorf(
			"read xiangwan Instance cancellation preview: %w",
			err,
		)
	}
	return value, nil
}

func (tx *instanceCancellationSQLTransaction) createInstancePreview(
	ctx context.Context,
	value activity.InstanceCancellationPreview,
) (activity.InstanceCancellationPreview, error) {
	impacts, err := json.Marshal(value.SessionImpacts)
	if err != nil {
		return activity.InstanceCancellationPreview{}, fmt.Errorf(
			"encode xiangwan Instance cancellation impacts: %w",
			err,
		)
	}
	created, err := scanInstanceCancellationPreview(tx.tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_instance_cancellation_previews (
    id, tenant_id, series_id, instance_id, requested_by,
    idempotency_key, snapshot_digest, expected_instance_version,
    session_count, target_session_count, already_cancelled_session_count,
    cancelled_registration_count, confirmed_registration_count,
    active_hold_count, free_registration_count,
    paid_refund_registration_count, pending_order_count,
    unknown_payment_count, refund_case_count, requested_refund_cents,
    coupon_adjustment_count, cancellation_reason,
    notification_strategy, session_impacts,
    expires_at, consumed_at, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11,
    $12, $13,
    $14, $15,
    $16, $17,
    $18, $19, $20,
    $21, $22, $23, $24,
    $25, $26, $27
)
RETURNING
    id, tenant_id, series_id, instance_id, requested_by,
    idempotency_key, snapshot_digest, expected_instance_version,
    session_count, target_session_count, already_cancelled_session_count,
    cancelled_registration_count, confirmed_registration_count,
    active_hold_count, free_registration_count,
    paid_refund_registration_count, pending_order_count,
    unknown_payment_count, refund_case_count, requested_refund_cents,
    coupon_adjustment_count, cancellation_reason,
    notification_strategy, session_impacts,
    expires_at, consumed_at, created_at
`,
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.RequestedBy,
		value.IdempotencyKey,
		value.SnapshotDigest,
		value.ExpectedInstanceVersion,
		value.SessionCount,
		value.TargetSessionCount,
		value.AlreadyCancelledSessionCount,
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
		value.CancellationReason,
		value.NotificationStrategy,
		impacts,
		value.ExpiresAt,
		value.ConsumedAt,
		value.CreatedAt,
	))
	if err != nil {
		return activity.InstanceCancellationPreview{}, fmt.Errorf(
			"create xiangwan Instance cancellation preview: %w",
			err,
		)
	}
	return created, nil
}

func (tx *instanceCancellationSQLTransaction) consumeInstancePreview(
	ctx context.Context,
	tenantID uuid.UUID,
	previewID uuid.UUID,
	consumedAt time.Time,
) (activity.InstanceCancellationPreview, error) {
	updated, err := scanInstanceCancellationPreview(tx.tx.QueryRowContext(ctx, `
UPDATE xiangwan_instance_cancellation_previews
SET consumed_at = $3
WHERE tenant_id = $1
  AND id = $2
  AND consumed_at IS NULL
  AND created_at <= $3
  AND expires_at >= $3
RETURNING
    id, tenant_id, series_id, instance_id, requested_by,
    idempotency_key, snapshot_digest, expected_instance_version,
    session_count, target_session_count, already_cancelled_session_count,
    cancelled_registration_count, confirmed_registration_count,
    active_hold_count, free_registration_count,
    paid_refund_registration_count, pending_order_count,
    unknown_payment_count, refund_case_count, requested_refund_cents,
    coupon_adjustment_count, cancellation_reason,
    notification_strategy, session_impacts,
    expires_at, consumed_at, created_at
`, tenantID, previewID, consumedAt))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.InstanceCancellationPreview{},
			ErrInstanceCancellationPreviewConflict
	}
	if err != nil {
		return activity.InstanceCancellationPreview{}, fmt.Errorf(
			"consume xiangwan Instance cancellation preview: %w",
			err,
		)
	}
	return updated, nil
}

func (tx *instanceCancellationSQLTransaction) getInstanceReceipt(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) (activity.InstanceCancellationReceipt, error) {
	value, err := scanInstanceCancellationReceipt(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, preview_id,
    idempotency_key, cancelled_by, cancellation_reason,
    notification_strategy, session_count, newly_cancelled_session_count,
    already_cancelled_session_count, cancelled_registration_count,
    released_confirmed_count, released_hold_count,
    closed_pending_order_count, refund_case_count, requested_refund_cents,
    coupon_adjustment_count,
    cancelled_at, resulting_instance_version, created_at
FROM xiangwan_instance_cancellation_receipts
WHERE tenant_id = $1 AND instance_id = $2
`, tenantID, instanceID))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.InstanceCancellationReceipt{},
			errInstanceCancellationReceiptNotFound
	}
	if err != nil {
		return activity.InstanceCancellationReceipt{}, fmt.Errorf(
			"get xiangwan Instance cancellation receipt: %w",
			err,
		)
	}
	return value, nil
}

func (tx *instanceCancellationSQLTransaction) updateCancelledInstance(
	ctx context.Context,
	value activity.Instance,
	expectedVersion int64,
) (activity.Instance, error) {
	updated, err := scanInstance(tx.tx.QueryRowContext(ctx, `
UPDATE xiangwan_activity_instances
SET status = $4,
    version = version + 1,
    updated_at = $5
WHERE tenant_id = $1
  AND series_id = $2
  AND id = $3
  AND version = $6
  AND status = 'published'
RETURNING
    id, tenant_id, series_id, issue_no, title, status,
    activity_type, TO_JSON(quick_tag_codes), cover_image_url, detail_blocks,
    publication_version, presentation_revision,
    scheduled_at, published_at, completed_at, version, created_at, updated_at
`,
		value.TenantID,
		value.SeriesID,
		value.ID,
		value.Status,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Instance{}, ErrInstanceCancellationTransaction
	}
	if err != nil {
		return activity.Instance{}, fmt.Errorf(
			"update xiangwan cancelled Instance: %w",
			err,
		)
	}
	return updated, nil
}

func (tx *instanceCancellationSQLTransaction) createInstanceReceipt(
	ctx context.Context,
	value activity.InstanceCancellationReceipt,
) (activity.InstanceCancellationReceipt, error) {
	created, err := scanInstanceCancellationReceipt(tx.tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_instance_cancellation_receipts (
    id, tenant_id, series_id, instance_id, preview_id,
    idempotency_key, cancelled_by, cancellation_reason,
    notification_strategy, session_count, newly_cancelled_session_count,
    already_cancelled_session_count, cancelled_registration_count,
    released_confirmed_count, released_hold_count,
    closed_pending_order_count, refund_case_count, requested_refund_cents,
    coupon_adjustment_count,
    cancelled_at, resulting_instance_version, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11,
    $12, $13,
    $14, $15,
    $16, $17, $18,
    $19,
    $20, $21, $22
)
RETURNING
    id, tenant_id, series_id, instance_id, preview_id,
    idempotency_key, cancelled_by, cancellation_reason,
    notification_strategy, session_count, newly_cancelled_session_count,
    already_cancelled_session_count, cancelled_registration_count,
    released_confirmed_count, released_hold_count,
    closed_pending_order_count, refund_case_count, requested_refund_cents,
    coupon_adjustment_count,
    cancelled_at, resulting_instance_version, created_at
`,
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.PreviewID,
		value.IdempotencyKey,
		value.CancelledBy,
		value.CancellationReason,
		value.NotificationStrategy,
		value.SessionCount,
		value.NewlyCancelledSessionCount,
		value.AlreadyCancelledSessionCount,
		value.CancelledRegistrationCount,
		value.ReleasedConfirmedCount,
		value.ReleasedHoldCount,
		value.ClosedPendingOrderCount,
		value.RefundCaseCount,
		value.RequestedRefundCents,
		value.CouponAdjustmentCount,
		value.CancelledAt,
		value.ResultingInstanceVersion,
		value.CreatedAt,
	))
	if err != nil {
		return activity.InstanceCancellationReceipt{}, fmt.Errorf(
			"create xiangwan Instance cancellation receipt: %w",
			err,
		)
	}
	return created, nil
}

func scanInstanceCancellationPreview(
	row rowScanner,
) (activity.InstanceCancellationPreview, error) {
	var value activity.InstanceCancellationPreview
	var sessionImpacts []byte
	var consumedAt sql.NullTime
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.SeriesID,
		&value.InstanceID,
		&value.RequestedBy,
		&value.IdempotencyKey,
		&value.SnapshotDigest,
		&value.ExpectedInstanceVersion,
		&value.SessionCount,
		&value.TargetSessionCount,
		&value.AlreadyCancelledSessionCount,
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
		&value.CancellationReason,
		&value.NotificationStrategy,
		&sessionImpacts,
		&value.ExpiresAt,
		&consumedAt,
		&value.CreatedAt,
	)
	if err != nil {
		return activity.InstanceCancellationPreview{}, err
	}
	if err := json.Unmarshal(sessionImpacts, &value.SessionImpacts); err != nil {
		return activity.InstanceCancellationPreview{}, fmt.Errorf(
			"decode xiangwan Instance cancellation impacts: %w",
			err,
		)
	}
	value.ConsumedAt = nullTimePointer(consumedAt)
	return value, nil
}

func scanInstanceCancellationReceipt(
	row rowScanner,
) (activity.InstanceCancellationReceipt, error) {
	var value activity.InstanceCancellationReceipt
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.SeriesID,
		&value.InstanceID,
		&value.PreviewID,
		&value.IdempotencyKey,
		&value.CancelledBy,
		&value.CancellationReason,
		&value.NotificationStrategy,
		&value.SessionCount,
		&value.NewlyCancelledSessionCount,
		&value.AlreadyCancelledSessionCount,
		&value.CancelledRegistrationCount,
		&value.ReleasedConfirmedCount,
		&value.ReleasedHoldCount,
		&value.ClosedPendingOrderCount,
		&value.RefundCaseCount,
		&value.RequestedRefundCents,
		&value.CouponAdjustmentCount,
		&value.CancelledAt,
		&value.ResultingInstanceVersion,
		&value.CreatedAt,
	)
	if err != nil {
		return activity.InstanceCancellationReceipt{}, err
	}
	return value, nil
}
