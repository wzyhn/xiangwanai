package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

type sqlLifecycleTransactionStarter struct {
	db *sql.DB
}

func (starter sqlLifecycleTransactionStarter) beginLifecycleTx(
	ctx context.Context,
	options *sql.TxOptions,
) (lifecycleTransaction, error) {
	if starter.db == nil {
		return nil, ErrInvalidLifecycleCommand
	}
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlLifecycleTransaction{
		tx:         tx,
		repository: NewRepository(tx),
	}, nil
}

type sqlLifecycleTransaction struct {
	tx         *sql.Tx
	repository *Repository
}

func (tx *sqlLifecycleTransaction) lockOrderUseFacts(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	orderID uuid.UUID,
) (orderUseFacts, error) {
	var value orderUseFacts
	var paidAt sql.NullTime
	err := tx.tx.QueryRowContext(ctx, `
SELECT
    order_record.id,
    order_record.tenant_id,
    order_record.registration_id,
    order_record.series_id,
    order_record.instance_id,
    order_record.principal_id,
    activity_instance.activity_type,
    order_record.original_price_cents,
    order_record.discount_cents,
    order_record.payment_status,
    order_record.paid_at,
    capacity_hold.hold_status,
    capacity_hold.expires_at,
    registration_record.participation_status
FROM xiangwan_orders AS order_record
JOIN xiangwan_capacity_holds AS capacity_hold
  ON capacity_hold.tenant_id = order_record.tenant_id
 AND capacity_hold.order_id = order_record.id
JOIN xiangwan_registrations AS registration_record
  ON registration_record.tenant_id = order_record.tenant_id
 AND registration_record.id = order_record.registration_id
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = order_record.tenant_id
 AND activity_instance.id = order_record.instance_id
WHERE order_record.tenant_id = $1
  AND order_record.principal_id = $2
  AND order_record.id = $3
FOR UPDATE OF order_record, capacity_hold, registration_record
`, tenantID, principalID, orderID).Scan(
		&value.ID,
		&value.TenantID,
		&value.RegistrationID,
		&value.SeriesID,
		&value.InstanceID,
		&value.PrincipalID,
		&value.ActivityType,
		&value.OriginalPriceCents,
		&value.DiscountCents,
		&value.PaymentStatus,
		&paidAt,
		&value.CapacityHoldStatus,
		&value.CapacityHoldExpiresAt,
		&value.ParticipationStatus,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return orderUseFacts{}, ErrCouponOrderUnavailable
	}
	if err != nil {
		return orderUseFacts{}, fmt.Errorf(
			`lock xiangwan Coupon Order facts: %w`,
			err,
		)
	}
	if paidAt.Valid {
		value.PaidAt = &paidAt.Time
	}
	return value, nil
}

func (tx *sqlLifecycleTransaction) getLedgerForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	couponID uuid.UUID,
) (Ledger, error) {
	return tx.repository.GetLedgerForUpdate(
		ctx,
		tenantID,
		principalID,
		couponID,
	)
}

func (tx *sqlLifecycleTransaction) appendLifecycleEntry(
	ctx context.Context,
	value coupon.Entry,
) (coupon.Entry, error) {
	return tx.repository.AppendLifecycleEntry(ctx, value)
}

func (tx *sqlLifecycleTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlLifecycleTransaction) Rollback() error {
	return tx.tx.Rollback()
}
