package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const MaxCapacityHoldExpiryBatch = 100

var (
	ErrInvalidCapacityHoldExpirer     = errors.New("invalid xiangwan capacity hold expirer")
	ErrInvalidCapacityHoldExpiryBatch = errors.New("invalid xiangwan capacity hold expiry batch")
	ErrCapacityHoldExpiryTransaction  = errors.New("xiangwan capacity hold expiry transaction conflict")
)

type ExpiredCapacityHold struct {
	Registration registration.Registration
	Order        payment.Order
	Hold         payment.CapacityHold
	Coupon       *AppliedCoupon
}

// CapacityHoldExpirer releases due capacity in bounded, independently committed
// PostgreSQL transactions. It resolves candidate identities without locking,
// then follows the canonical Series → Instance → Session → Registration →
// Order → hold lock order shared with payment confirmation.
type CapacityHoldExpirer struct {
	transactions paidRegistrationTransactionStarter
	now          func() time.Time
}

func NewCapacityHoldExpirer(db *sql.DB) *CapacityHoldExpirer {
	if db == nil {
		return &CapacityHoldExpirer{now: time.Now}
	}
	return &CapacityHoldExpirer{
		transactions: sqlPaidRegistrationTransactionStarter{db: db},
		now:          time.Now,
	}
}

func (expirer *CapacityHoldExpirer) ExpireDue(
	ctx context.Context,
	limit int,
) ([]ExpiredCapacityHold, error) {
	if limit < 1 || limit > MaxCapacityHoldExpiryBatch {
		return nil, ErrInvalidCapacityHoldExpiryBatch
	}
	if expirer == nil || expirer.transactions == nil || expirer.now == nil || ctx == nil {
		return nil, ErrInvalidCapacityHoldExpirer
	}

	expired := make([]ExpiredCapacityHold, 0, limit)
	for len(expired) < limit {
		result, err := expirer.expireNext(ctx)
		if err != nil {
			return expired, err
		}
		if result == nil {
			break
		}
		expired = append(expired, *result)
	}
	return expired, nil
}

func (expirer *CapacityHoldExpirer) expireNext(
	ctx context.Context,
) (*ExpiredCapacityHold, error) {
	cutoff := expirer.now().UTC()
	if cutoff.IsZero() {
		return nil, ErrInvalidCapacityHoldExpiryBatch
	}

	tx, err := expirer.transactions.beginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin xiangwan capacity hold expiry transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	candidate, err := findDueCapacityHold(ctx, tx, cutoff)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return nil, classifyCapacityHoldExpiryCommitError(err)
		}
		committed = true
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if _, err := lockPaymentConfirmationSeries(
		ctx,
		tx,
		candidate.tenantID,
		candidate.seriesID,
	); err != nil {
		return nil, err
	}
	if _, err := lockPaymentConfirmationInstance(
		ctx,
		tx,
		candidate.tenantID,
		candidate.seriesID,
		candidate.instanceID,
	); err != nil {
		return nil, err
	}
	session, err := lockPaymentConfirmationSession(
		ctx,
		tx,
		candidate.tenantID,
		candidate.instanceID,
		candidate.sessionID,
	)
	if err != nil {
		return nil, err
	}
	currentRegistration, err := lockPaymentConfirmationRegistration(
		ctx,
		tx,
		candidate.tenantID,
		candidate.registrationID,
	)
	if err != nil {
		return nil, err
	}
	repository := &Repository{db: tx}
	currentOrder, err := repository.GetOrderForUpdate(
		ctx,
		candidate.tenantID,
		candidate.orderID,
	)
	if err != nil {
		return nil, err
	}
	currentHold, err := repository.GetCapacityHoldByOrderForUpdate(
		ctx,
		candidate.tenantID,
		candidate.orderID,
	)
	if err != nil {
		return nil, err
	}
	if currentHold.ID != candidate.holdID ||
		!paymentContextMatchesRegistration(currentOrder, currentHold, currentRegistration) {
		return nil, ErrCapacityHoldExpiryTransaction
	}
	couponRepository := tx.couponOrderRepository()
	var couponLedger *couponpostgres.Ledger
	if currentOrder.DiscountCents > 0 {
		couponLedger, err = lockOrderCoupon(
			ctx,
			couponRepository,
			currentOrder.TenantID,
			currentOrder.ID,
		)
		if err != nil {
			return nil, err
		}
	}

	processedAt := expirer.now().UTC()
	if currentHold.HoldStatus != payment.CapacityHoldStatusActive ||
		currentHold.ExpiresAt.After(processedAt) {
		if err := tx.Commit(); err != nil {
			return nil, classifyCapacityHoldExpiryCommitError(err)
		}
		committed = true
		return nil, nil
	}
	if currentOrder.PaymentStatus == payment.OrderStatusPaidConfirmed ||
		currentRegistration.ParticipationStatus == registration.ParticipationStatusConfirmed {
		return nil, ErrCapacityHoldExpiryTransaction
	}

	updatedOrder := currentOrder
	if currentOrder.PaymentStatus == payment.OrderStatusPending {
		var changed bool
		updatedOrder, changed, err = payment.CloseOrderUnpaid(currentOrder, processedAt)
		if err != nil || !changed {
			return nil, fmt.Errorf(
				"%w: close pending Order: %v",
				ErrCapacityHoldExpiryTransaction,
				err,
			)
		}
		updatedOrder, err = repository.UpdateOrderPayment(ctx, updatedOrder, currentOrder.Version)
		if err != nil {
			return nil, classifyCapacityHoldExpiryWriteError(err)
		}
	} else if currentOrder.PaymentStatus != payment.OrderStatusUnknown &&
		currentOrder.PaymentStatus != payment.OrderStatusClosedUnpaid {
		return nil, ErrCapacityHoldExpiryTransaction
	}

	expiredHold, changed, err := payment.ExpireCapacityHold(
		currentHold,
		"payment_hold_expired",
		processedAt,
	)
	if err != nil || !changed {
		return nil, fmt.Errorf(
			"%w: expire hold: %v",
			ErrCapacityHoldExpiryTransaction,
			err,
		)
	}
	expiredHold, err = repository.UpdateCapacityHold(ctx, expiredHold, currentHold.Version)
	if err != nil {
		return nil, classifyCapacityHoldExpiryWriteError(err)
	}

	updatedRegistration := currentRegistration
	if currentRegistration.ParticipationStatus == registration.ParticipationStatusPendingPayment {
		updatedRegistration, changed, err = registration.CancelRegistration(
			currentRegistration,
			"payment_hold_expired",
			processedAt,
		)
		if err != nil || !changed {
			return nil, fmt.Errorf(
				"%w: cancel pending Registration: %v",
				ErrCapacityHoldExpiryTransaction,
				err,
			)
		}
		updatedRegistration, err = updatePaymentConfirmationRegistration(
			ctx,
			tx,
			updatedRegistration,
			currentRegistration.Version,
		)
		if err != nil {
			return nil, classifyCapacityHoldExpiryWriteError(err)
		}
	} else if currentRegistration.ParticipationStatus != registration.ParticipationStatusCancelled {
		return nil, ErrCapacityHoldExpiryTransaction
	}

	if err := decrementExpiredHoldCapacity(
		ctx,
		tx,
		candidate.tenantID,
		candidate.instanceID,
		candidate.sessionID,
		session.version,
		processedAt,
	); err != nil {
		return nil, err
	}
	releasedCoupon, err := releaseOrderCoupon(
		ctx,
		couponRepository,
		couponLedger,
		currentOrder.ID,
		"payment_hold_expired",
		processedAt,
	)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, classifyCapacityHoldExpiryCommitError(err)
	}
	committed = true
	return &ExpiredCapacityHold{
		Registration: updatedRegistration,
		Order:        updatedOrder,
		Hold:         expiredHold,
		Coupon:       appliedCoupon(couponLedger, releasedCoupon),
	}, nil
}

type dueCapacityHold struct {
	tenantID       uuid.UUID
	holdID         uuid.UUID
	orderID        uuid.UUID
	registrationID uuid.UUID
	seriesID       uuid.UUID
	instanceID     uuid.UUID
	sessionID      uuid.UUID
}

func findDueCapacityHold(
	ctx context.Context,
	tx paidRegistrationTransaction,
	cutoff time.Time,
) (dueCapacityHold, error) {
	var candidate dueCapacityHold
	err := tx.queryRowContext(ctx, `
SELECT
    capacity_hold.tenant_id,
    capacity_hold.id,
    capacity_hold.order_id,
    capacity_hold.registration_id,
    activity_order.series_id,
    activity_order.instance_id,
    capacity_hold.session_id
FROM xiangwan_capacity_holds AS capacity_hold
JOIN xiangwan_orders AS activity_order
  ON activity_order.tenant_id = capacity_hold.tenant_id
 AND activity_order.id = capacity_hold.order_id
WHERE capacity_hold.hold_status = 'active'
  AND capacity_hold.expires_at <= $1
ORDER BY capacity_hold.expires_at, capacity_hold.tenant_id, capacity_hold.id
LIMIT 1
`, cutoff).Scan(
		&candidate.tenantID,
		&candidate.holdID,
		&candidate.orderID,
		&candidate.registrationID,
		&candidate.seriesID,
		&candidate.instanceID,
		&candidate.sessionID,
	)
	if err != nil {
		return dueCapacityHold{}, err
	}
	return candidate, nil
}

func decrementExpiredHoldCapacity(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
	expectedVersion int64,
	now time.Time,
) error {
	var newVersion int64
	err := tx.queryRowContext(ctx, `
UPDATE xiangwan_activity_sessions
SET active_hold_count = active_hold_count - 1,
    version = version + 1,
    updated_at = $4
WHERE tenant_id = $1
  AND instance_id = $2
  AND id = $3
  AND version = $5
  AND active_hold_count > 0
RETURNING version
`, tenantID, instanceID, sessionID, now, expectedVersion).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCapacityHoldExpiryTransaction
	}
	if err != nil {
		return fmt.Errorf("decrement expired capacity hold: %w", err)
	}
	return nil
}

func classifyCapacityHoldExpiryWriteError(err error) error {
	if errors.Is(err, ErrOrderVersionConflict) ||
		errors.Is(err, ErrCapacityHoldVersionConflict) ||
		errors.Is(err, ErrPaymentConfirmationTransaction) {
		return ErrCapacityHoldExpiryTransaction
	}
	return err
}

func classifyCapacityHoldExpiryCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "40001" {
		return fmt.Errorf("%w: %v", ErrCapacityHoldExpiryTransaction, err)
	}
	return fmt.Errorf("commit xiangwan capacity hold expiry transaction: %w", err)
}
