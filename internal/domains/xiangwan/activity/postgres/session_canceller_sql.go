package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	refundpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
)

type sessionCancellationSQLResolver struct {
	db *sql.DB
}

func (resolver sessionCancellationSQLResolver) resolveSessionCancellationTarget(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
) (sessionCancellationTarget, error) {
	var target sessionCancellationTarget
	err := resolver.db.QueryRowContext(ctx, `
SELECT
    activity_series.tenant_id,
    activity_series.id,
    activity_instance.id,
    activity_session.id
FROM xiangwan_activity_sessions AS activity_session
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = activity_session.tenant_id
 AND activity_instance.id = activity_session.instance_id
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_instance.tenant_id
 AND activity_series.id = activity_instance.series_id
WHERE activity_session.tenant_id = $1
  AND activity_session.id = $2
`, tenantID, sessionID).Scan(
		&target.TenantID,
		&target.SeriesID,
		&target.InstanceID,
		&target.SessionID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return sessionCancellationTarget{}, errSessionCancellationTargetNotFound
	}
	if err != nil {
		return sessionCancellationTarget{}, fmt.Errorf(
			"resolve xiangwan Session cancellation target: %w",
			err,
		)
	}
	return target, nil
}

type sessionCancellationSQLTransactionStarter struct {
	db *sql.DB
}

func (starter sessionCancellationSQLTransactionStarter) beginSessionCancellationTx(
	ctx context.Context,
	options *sql.TxOptions,
) (sessionCancellationTransaction, error) {
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sessionCancellationSQLTransaction{
		tx:            tx,
		activities:    NewRepository(tx),
		registrations: registrationpostgres.NewRepository(tx),
		payments:      paymentpostgres.NewRepository(tx),
		refunds:       refundpostgres.NewRepository(tx),
		coupons:       couponpostgres.NewRepository(tx),
	}, nil
}

type sessionCancellationSQLTransaction struct {
	tx            *sql.Tx
	activities    *Repository
	registrations *registrationpostgres.Repository
	payments      *paymentpostgres.Repository
	refunds       *refundpostgres.Repository
	coupons       *couponpostgres.Repository
}

func (tx *sessionCancellationSQLTransaction) cancellationSQLTx() *sql.Tx { return tx.tx }

func (tx *sessionCancellationSQLTransaction) lockSeries(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (activity.SeriesStatus, error) {
	var status activity.SeriesStatus
	err := tx.tx.QueryRowContext(ctx, `
SELECT status
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, seriesID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSessionCancellationTransaction
	}
	if err != nil {
		return "", fmt.Errorf("lock xiangwan cancellation Series: %w", err)
	}
	return status, nil
}

func (tx *sessionCancellationSQLTransaction) lockInstance(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (activity.InstanceStatus, error) {
	var status activity.InstanceStatus
	err := tx.tx.QueryRowContext(ctx, `
SELECT status
FROM xiangwan_activity_instances
WHERE tenant_id = $1
  AND series_id = $2
  AND id = $3
FOR UPDATE
`, tenantID, seriesID, instanceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSessionCancellationTransaction
	}
	if err != nil {
		return "", fmt.Errorf("lock xiangwan cancellation Instance: %w", err)
	}
	return status, nil
}

func (tx *sessionCancellationSQLTransaction) lockSession(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (activity.Session, error) {
	value, err := tx.activities.LockSession(ctx, tenantID, sessionID)
	if errors.Is(err, ErrSessionNotFound) {
		return activity.Session{}, ErrSessionCancellationNotFound
	}
	if err != nil {
		return activity.Session{}, err
	}
	if value.InstanceID != instanceID {
		return activity.Session{}, ErrSessionCancellationTransaction
	}
	return value, nil
}

func (tx *sessionCancellationSQLTransaction) getReceiptBySession(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
) (activity.SessionCancellationReceipt, error) {
	value, err := scanSessionCancellationReceipt(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id,
    COALESCE(preview_id, '00000000-0000-0000-0000-000000000000'::uuid),
    idempotency_key, cancelled_by, cancellation_reason,
    notification_strategy,
    cancelled_registration_count, released_confirmed_count,
    released_hold_count, closed_pending_order_count,
    refund_case_count, requested_refund_cents, coupon_adjustment_count,
    cancelled_at, resulting_session_version, created_at
FROM xiangwan_session_cancellation_receipts
WHERE tenant_id = $1 AND session_id = $2
`, tenantID, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.SessionCancellationReceipt{},
			errSessionCancellationReceiptNotFound
	}
	if err != nil {
		return activity.SessionCancellationReceipt{}, fmt.Errorf(
			"get xiangwan Session cancellation receipt: %w",
			err,
		)
	}
	return value, nil
}

func (tx *sessionCancellationSQLTransaction) listOpenRegistrations(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
) ([]registration.Registration, error) {
	rows, err := tx.tx.QueryContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
FROM xiangwan_registrations
WHERE tenant_id = $1
  AND session_id = $2
  AND participation_status IN ('pending_payment', 'confirmed')
ORDER BY id
FOR UPDATE
`, tenantID, sessionID)
	if err != nil {
		return nil, fmt.Errorf(
			"lock xiangwan cancellation Registrations: %w",
			err,
		)
	}
	defer func() {
		_ = rows.Close()
	}()

	values := make([]registration.Registration, 0)
	for rows.Next() {
		value, scanErr := scanSessionCancellationRegistration(rows)
		if scanErr != nil {
			return nil, fmt.Errorf(
				"scan xiangwan cancellation Registration: %w",
				scanErr,
			)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"iterate xiangwan cancellation Registrations: %w",
			err,
		)
	}
	return values, nil
}

func (tx *sessionCancellationSQLTransaction) lockOrderByRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (payment.Order, bool, error) {
	value, err := tx.payments.GetOrderByRegistrationForUpdate(
		ctx,
		tenantID,
		registrationID,
	)
	if errors.Is(err, paymentpostgres.ErrOrderNotFound) {
		return payment.Order{}, false, nil
	}
	if err != nil {
		return payment.Order{}, false, err
	}
	return value, true, nil
}

func (tx *sessionCancellationSQLTransaction) lockHoldByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (payment.CapacityHold, error) {
	value, err := tx.payments.GetCapacityHoldByOrderForUpdate(ctx, tenantID, orderID)
	if errors.Is(err, paymentpostgres.ErrCapacityHoldNotFound) {
		return payment.CapacityHold{}, ErrSessionCancellationTransaction
	}
	return value, err
}

func (tx *sessionCancellationSQLTransaction) lockRefundByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (refund.Case, error) {
	value, err := tx.refunds.GetByOrderForUpdate(ctx, tenantID, orderID)
	if errors.Is(err, refundpostgres.ErrRefundCaseNotFound) {
		return refund.Case{}, errSessionCancellationRefundNotFound
	}
	return value, err
}

func (tx *sessionCancellationSQLTransaction) lockCouponByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (couponpostgres.Ledger, error) {
	value, err := tx.coupons.GetLedgerByOrderForUpdate(ctx, tenantID, orderID)
	if errors.Is(err, couponpostgres.ErrCouponNotFound) ||
		errors.Is(err, couponpostgres.ErrGrantFactsConflict) ||
		errors.Is(err, coupon.ErrInvalidLedger) {
		return couponpostgres.Ledger{}, ErrSessionCancellationTransaction
	}
	return value, err
}

func (tx *sessionCancellationSQLTransaction) updateRegistration(
	ctx context.Context,
	value registration.Registration,
	expectedVersion int64,
) (registration.Registration, error) {
	updated, err := tx.registrations.UpdateParticipation(ctx, value, expectedVersion)
	if errors.Is(err, registrationpostgres.ErrRegistrationVersionConflict) {
		return registration.Registration{}, ErrSessionCancellationTransaction
	}
	return updated, err
}

func (tx *sessionCancellationSQLTransaction) updateOrder(
	ctx context.Context,
	value payment.Order,
	expectedVersion int64,
) (payment.Order, error) {
	updated, err := tx.payments.UpdateOrderPayment(ctx, value, expectedVersion)
	if errors.Is(err, paymentpostgres.ErrOrderVersionConflict) {
		return payment.Order{}, ErrSessionCancellationTransaction
	}
	return updated, err
}

func (tx *sessionCancellationSQLTransaction) updateHold(
	ctx context.Context,
	value payment.CapacityHold,
	expectedVersion int64,
) (payment.CapacityHold, error) {
	updated, err := tx.payments.UpdateCapacityHold(ctx, value, expectedVersion)
	if errors.Is(err, paymentpostgres.ErrCapacityHoldVersionConflict) {
		return payment.CapacityHold{}, ErrSessionCancellationTransaction
	}
	return updated, err
}

func (tx *sessionCancellationSQLTransaction) releaseCouponForOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
	reason string,
	at time.Time,
) error {
	_, err := tx.coupons.ReleaseOrderHold(ctx, tenantID, orderID, reason, at)
	if err != nil {
		return fmt.Errorf("%w: release Coupon hold: %v", ErrSessionCancellationTransaction, err)
	}
	return nil
}

func (tx *sessionCancellationSQLTransaction) appendCouponEntry(
	ctx context.Context,
	value coupon.Entry,
) (coupon.Entry, error) {
	return tx.coupons.AppendLifecycleEntry(ctx, value)
}

func (tx *sessionCancellationSQLTransaction) createRefund(
	ctx context.Context,
	value refund.Case,
) (refund.Case, error) {
	return tx.refunds.Create(ctx, value)
}

func (tx *sessionCancellationSQLTransaction) updateSession(
	ctx context.Context,
	value activity.Session,
	expectedVersion int64,
) (activity.Session, error) {
	updated, err := scanSession(tx.tx.QueryRowContext(ctx, `
UPDATE xiangwan_activity_sessions
SET status = $3,
    confirmed_registration_count = $4,
    active_hold_count = $5,
    version = version + 1,
    updated_at = $6
WHERE tenant_id = $1
  AND id = $2
  AND version = $7
  AND status = 'published'
RETURNING
    id, tenant_id, instance_id, title, status,
    registration_start_at, registration_end_at, session_start_at, session_end_at,
    capacity, group_minimum, low_stock_threshold, price_cents,
    delivery_mode, area_code, venue_name, address, longitude, latitude,
    online_participation_mode, online_participation_compliant,
    confirmed_registration_count, active_hold_count,
    sort_order, published_at, version, created_at, updated_at
`,
		value.TenantID,
		value.ID,
		value.Status,
		value.ConfirmedRegistrationCount,
		value.ActiveHoldCount,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Session{}, ErrSessionCancellationTransaction
	}
	if err != nil {
		return activity.Session{}, fmt.Errorf(
			"update xiangwan cancelled Session: %w",
			err,
		)
	}
	return updated, nil
}

func (tx *sessionCancellationSQLTransaction) createReceipt(
	ctx context.Context,
	value activity.SessionCancellationReceipt,
) (activity.SessionCancellationReceipt, error) {
	created, err := scanSessionCancellationReceipt(tx.tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_session_cancellation_receipts (
    id, tenant_id, series_id, instance_id, session_id,
    preview_id, idempotency_key, cancelled_by, cancellation_reason,
    notification_strategy,
    cancelled_registration_count, released_confirmed_count,
    released_hold_count, closed_pending_order_count,
    refund_case_count, requested_refund_cents, coupon_adjustment_count,
    cancelled_at, resulting_session_version, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10,
    $11, $12,
    $13, $14,
    $15, $16, $17,
    $18, $19, $20
)
RETURNING
    id, tenant_id, series_id, instance_id, session_id,
    preview_id, idempotency_key, cancelled_by, cancellation_reason,
    notification_strategy,
    cancelled_registration_count, released_confirmed_count,
    released_hold_count, closed_pending_order_count,
    refund_case_count, requested_refund_cents, coupon_adjustment_count,
    cancelled_at, resulting_session_version, created_at
`,
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PreviewID,
		value.IdempotencyKey,
		value.CancelledBy,
		value.CancellationReason,
		value.NotificationStrategy,
		value.CancelledRegistrationCount,
		value.ReleasedConfirmedCount,
		value.ReleasedHoldCount,
		value.ClosedPendingOrderCount,
		value.RefundCaseCount,
		value.RequestedRefundCents,
		value.CouponAdjustmentCount,
		value.CancelledAt,
		value.ResultingSessionVersion,
		value.CreatedAt,
	))
	if err != nil {
		return activity.SessionCancellationReceipt{}, fmt.Errorf(
			"create xiangwan Session cancellation receipt: %w",
			err,
		)
	}
	return created, nil
}

func (tx *sessionCancellationSQLTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sessionCancellationSQLTransaction) Rollback() error {
	return tx.tx.Rollback()
}

func scanSessionCancellationRegistration(
	row rowScanner,
) (registration.Registration, error) {
	var value registration.Registration
	var confirmedAt sql.NullTime
	var cancelledAt sql.NullTime
	var cancellationReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PrincipalID,
		&value.ParticipationStatus,
		&value.IdempotencyKey,
		&confirmedAt,
		&cancelledAt,
		&cancellationReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return registration.Registration{}, err
	}
	value.ConfirmedAt = nullTimePointer(confirmedAt)
	value.CancelledAt = nullTimePointer(cancelledAt)
	value.CancellationReason = nullStringPointer(cancellationReason)
	return value, nil
}

func scanSessionCancellationReceipt(
	row rowScanner,
) (activity.SessionCancellationReceipt, error) {
	var value activity.SessionCancellationReceipt
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PreviewID,
		&value.IdempotencyKey,
		&value.CancelledBy,
		&value.CancellationReason,
		&value.NotificationStrategy,
		&value.CancelledRegistrationCount,
		&value.ReleasedConfirmedCount,
		&value.ReleasedHoldCount,
		&value.ClosedPendingOrderCount,
		&value.RefundCaseCount,
		&value.RequestedRefundCents,
		&value.CouponAdjustmentCount,
		&value.CancelledAt,
		&value.ResultingSessionVersion,
		&value.CreatedAt,
	)
	if err != nil {
		return activity.SessionCancellationReceipt{}, err
	}
	return value, nil
}
