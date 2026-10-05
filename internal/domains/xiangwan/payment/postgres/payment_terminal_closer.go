package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
)

const paymentProviderTerminalReason = "payment_provider_terminal"

// CloseTrustedUnpaidPayment records an authoritative provider terminal fact
// and releases the still-pending local participation in one serializable
// transaction. A later trusted SUCCESS remains admissible and enters the
// existing refund-required path.
func (confirmer *PaymentConfirmer) CloseTrustedUnpaidPayment(
	ctx context.Context,
	command payment.TrustedUnpaidPaymentClosure,
) (payment.PaymentConvergence, error) {
	observation := command.Observation
	if confirmer == nil || confirmer.transactions == nil || confirmer.now == nil ||
		ctx == nil || payment.ValidateTransactionObservation(observation) != nil ||
		observation.ObservationSource !=
			payment.TransactionObservationSourceMerchantQuery ||
		!payment.IsProviderTerminalUnpaidState(observation.TradeState) {
		return payment.PaymentConvergence{}, payment.ErrInvalidPaymentQuery
	}

	return confirmer.closeUnpaidPayment(ctx, payment.RejectedPrepayClosure{
		TenantID: observation.TenantID, OrderID: observation.OrderID, PrincipalID: observation.PrincipalID,
	}, &observation)
}

func (confirmer *PaymentConfirmer) CloseRejectedPrepay(ctx context.Context, command payment.RejectedPrepayClosure) (payment.PaymentConvergence, error) {
	if confirmer == nil || confirmer.transactions == nil || confirmer.now == nil || ctx == nil ||
		command.TenantID == uuid.Nil || command.OrderID == uuid.Nil || command.PrincipalID == uuid.Nil {
		return payment.PaymentConvergence{}, payment.ErrInvalidPrepayAttempt
	}
	return confirmer.closeUnpaidPayment(ctx, command, nil)
}

// A nil observation selects the durable, definitely rejected first prepay path.
// It never fabricates a provider CLOSED transaction observation.
func (confirmer *PaymentConfirmer) closeUnpaidPayment(ctx context.Context, command payment.RejectedPrepayClosure, observation *payment.TransactionObservation) (payment.PaymentConvergence, error) {
	reason := paymentProviderTerminalReason
	if observation == nil {
		reason = "payment_prepay_rejected"
	}

	tx, err := confirmer.transactions.beginTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return payment.PaymentConvergence{}, fmt.Errorf(
			"begin xiangwan terminal payment transaction: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	repository := &Repository{db: tx}
	resolvedOrder, err := repository.GetOrder(ctx, command.TenantID, command.OrderID)
	if errors.Is(err, ErrOrderNotFound) {
		return payment.PaymentConvergence{}, ErrPaymentConfirmationNotFound
	}
	if err != nil {
		return payment.PaymentConvergence{}, err
	}
	if resolvedOrder.PrincipalID != command.PrincipalID || (observation != nil && !terminalObservationMatchesOrder(*observation, resolvedOrder)) {
		return payment.PaymentConvergence{}, ErrPaymentConfirmationConflict
	}

	if _, err = lockPaymentConfirmationSeries(
		ctx,
		tx,
		resolvedOrder.TenantID,
		resolvedOrder.SeriesID,
	); err != nil {
		return payment.PaymentConvergence{}, err
	}
	if _, err = lockPaymentConfirmationInstance(
		ctx,
		tx,
		resolvedOrder.TenantID,
		resolvedOrder.SeriesID,
		resolvedOrder.InstanceID,
	); err != nil {
		return payment.PaymentConvergence{}, err
	}
	session, err := lockPaymentConfirmationSession(
		ctx,
		tx,
		resolvedOrder.TenantID,
		resolvedOrder.InstanceID,
		resolvedOrder.SessionID,
	)
	if err != nil {
		return payment.PaymentConvergence{}, err
	}
	currentRegistration, err := lockPaymentConfirmationRegistration(
		ctx,
		tx,
		resolvedOrder.TenantID,
		resolvedOrder.RegistrationID,
	)
	if err != nil {
		return payment.PaymentConvergence{}, err
	}
	currentOrder, err := repository.GetOrderForUpdate(ctx, command.TenantID, command.OrderID)
	if err != nil {
		return payment.PaymentConvergence{}, err
	}
	if currentOrder.ID != resolvedOrder.ID ||
		currentOrder.PrincipalID != command.PrincipalID ||
		(observation != nil && !terminalObservationMatchesOrder(*observation, currentOrder)) {
		return payment.PaymentConvergence{}, ErrPaymentConfirmationConflict
	}
	if err := confirmer.requireMerchantConfigForOrder(ctx, tx, currentOrder, true); err != nil {
		return payment.PaymentConvergence{}, err
	}
	currentHold, err := repository.GetCapacityHoldByOrderForUpdate(
		ctx,
		currentOrder.TenantID,
		currentOrder.ID,
	)
	if errors.Is(err, ErrCapacityHoldNotFound) {
		return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
	}
	if err != nil {
		return payment.PaymentConvergence{}, err
	}
	if !paymentContextMatchesRegistration(
		currentOrder,
		currentHold,
		currentRegistration,
	) {
		return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
	}
	if observation != nil {
		if err := insertPaymentTransactionObservation(ctx, tx, *observation); err != nil {
			return payment.PaymentConvergence{}, err
		}
	} else if err := requireDefinitivelyRejectedPrepay(ctx, tx, currentOrder); err != nil {
		return payment.PaymentConvergence{}, err
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
			return payment.PaymentConvergence{}, err
		}
	}

	processedAt := confirmer.now().UTC()
	if processedAt.IsZero() || (observation != nil && observation.ObservedAt.After(processedAt)) {
		return payment.PaymentConvergence{}, payment.ErrInvalidPaymentQuery
	}
	updatedOrder := currentOrder
	switch currentOrder.PaymentStatus {
	case payment.OrderStatusPending, payment.OrderStatusUnknown:
		updatedOrder, _, err = payment.CloseOrderUnpaid(currentOrder, processedAt)
		if err != nil {
			return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
		}
		updatedOrder, err = repository.UpdateOrderPayment(
			ctx,
			updatedOrder,
			currentOrder.Version,
		)
		if err != nil {
			return payment.PaymentConvergence{}, classifyPaymentConfirmationWriteError(err)
		}
	case payment.OrderStatusClosedUnpaid:
	case payment.OrderStatusPaidConfirmed, payment.OrderStatusSettledZero:
		return payment.PaymentConvergence{}, ErrPaymentConfirmationConflict
	default:
		return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
	}

	switch currentHold.HoldStatus {
	case payment.CapacityHoldStatusActive:
		if currentRegistration.ParticipationStatus !=
			registration.ParticipationStatusPendingPayment {
			return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
		}
		var finishedHold payment.CapacityHold
		if processedAt.Before(currentHold.ExpiresAt) {
			finishedHold, _, err = payment.ReleaseCapacityHold(
				currentHold,
				reason,
				processedAt,
			)
		} else {
			finishedHold, _, err = payment.ExpireCapacityHold(
				currentHold,
				reason,
				processedAt,
			)
		}
		if err != nil {
			return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
		}
		if _, err = repository.UpdateCapacityHold(
			ctx,
			finishedHold,
			currentHold.Version,
		); err != nil {
			return payment.PaymentConvergence{}, classifyPaymentConfirmationWriteError(err)
		}

		cancelled, _, cancelErr := registration.CancelRegistration(
			currentRegistration,
			reason,
			processedAt,
		)
		if cancelErr != nil {
			return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
		}
		if _, err = updatePaymentConfirmationRegistration(
			ctx,
			tx,
			cancelled,
			currentRegistration.Version,
		); err != nil {
			return payment.PaymentConvergence{}, err
		}
		if err = releasePaymentConfirmationCapacity(
			ctx,
			tx,
			currentOrder.TenantID,
			currentOrder.InstanceID,
			currentOrder.SessionID,
			session.version,
			processedAt,
		); err != nil {
			return payment.PaymentConvergence{}, err
		}
	case payment.CapacityHoldStatusReleased, payment.CapacityHoldStatusExpired:
		if currentRegistration.ParticipationStatus !=
			registration.ParticipationStatusCancelled {
			return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
		}
	case payment.CapacityHoldStatusConverted:
		return payment.PaymentConvergence{}, ErrPaymentConfirmationConflict
	default:
		return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
	}

	if _, err = releaseOrderCoupon(
		ctx,
		couponRepository,
		couponLedger,
		currentOrder.ID,
		reason,
		processedAt,
	); err != nil {
		return payment.PaymentConvergence{}, err
	}
	if err := tx.Commit(); err != nil {
		return payment.PaymentConvergence{}, classifyPaymentConfirmationCommitError(err)
	}
	committed = true
	return payment.PaymentConvergence{Order: updatedOrder}, nil
}

func terminalObservationMatchesOrder(
	observation payment.TransactionObservation,
	order payment.Order,
) bool {
	return observation.TenantID == order.TenantID &&
		observation.OrderID == order.ID &&
		observation.PrincipalID == order.PrincipalID &&
		observation.PaymentAppID == order.PaymentAppID &&
		observation.PaymentMerchantID == order.PaymentMerchantID &&
		observation.OutTradeNo == order.MerchantOrderNo &&
		observation.AmountCents == order.PayableCents
}

// Version 2 is exactly the completion of the original version-1 lease. A
// takeover advances the version before another provider invocation, so even a
// missing first response cannot be mistaken for a definitely rejected order.
func requireDefinitivelyRejectedPrepay(ctx context.Context, tx paidRegistrationTransaction, order payment.Order) error {
	var code string
	err := tx.queryRowContext(ctx, `
SELECT observation.provider_code
FROM xiangwan_payment_attempts AS attempt
JOIN xiangwan_payment_observations AS observation
  ON observation.tenant_id = attempt.tenant_id AND observation.attempt_id = attempt.id
WHERE attempt.tenant_id = $1 AND attempt.order_id = $2
  AND attempt.principal_id = $3 AND attempt.payment_app_id = $4
  AND attempt.payment_merchant_id = $5 AND attempt.out_trade_no = $6
  AND attempt.amount_cents = $7
  AND attempt.version = 2 AND attempt.attempt_status = 'unknown'
  AND attempt.prepay_id IS NULL AND attempt.owner_token IS NULL
  AND attempt.last_error_class = 'provider_rejected'
  AND observation.observation_kind = 'prepay_ambiguous'
  AND observation.error_class = 'provider_rejected'
  AND (SELECT count(*) FROM xiangwan_payment_observations AS all_observations
       WHERE all_observations.tenant_id = attempt.tenant_id AND all_observations.attempt_id = attempt.id) = 1
  AND NOT EXISTS (SELECT 1 FROM xiangwan_payment_transaction_observations AS transactions
                  WHERE transactions.tenant_id = attempt.tenant_id AND transactions.order_id = attempt.order_id)
FOR UPDATE OF attempt
`, order.TenantID, order.ID, order.PrincipalID, order.PaymentAppID,
		order.PaymentMerchantID, order.MerchantOrderNo, order.PayableCents).Scan(&code)
	if errors.Is(err, sql.ErrNoRows) {
		return payment.ErrPrepayRejectionUnproven
	}
	if err != nil {
		return fmt.Errorf("verify xiangwan rejected prepay evidence: %w", err)
	}
	if !payment.IsDefinitivePrepayRejectionCode(code) {
		return payment.ErrPrepayRejectionUnproven
	}
	return nil
}
