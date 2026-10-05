package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type PaymentDisposition string

const (
	PaymentDispositionParticipationConfirmed PaymentDisposition = "participation_confirmed"
	PaymentDispositionRefundRequired         PaymentDisposition = "refund_required"
)

var (
	ErrInvalidPaymentConfirmationCommand = errors.New("invalid xiangwan payment confirmation command")
	ErrPaymentConfirmationNotFound       = errors.New("xiangwan payment confirmation Order not found")
	ErrPaymentConfirmationConflict       = errors.New("xiangwan payment confirmation conflicts with recorded fact")
	ErrPaymentConfirmationTransaction    = errors.New("xiangwan payment confirmation transaction conflict")
)

type ConfirmPaymentCommand struct {
	TenantID            uuid.UUID
	PaymentAppID        string
	PaymentMerchantID   string
	MerchantOrderNo     string
	WeChatTransactionID string
	ActualPaidCents     int64
	PaidAt              time.Time
	Observation         *payment.TransactionObservation
}

type PaymentConfirmationResult struct {
	Registration registration.Registration
	Order        payment.Order
	Hold         payment.CapacityHold
	Coupon       *AppliedCoupon
	Refund       *refund.Case
	Disposition  PaymentDisposition
}

// PaymentConfirmer records a trusted provider fact and resolves its capacity
// hold in one serializable PostgreSQL transaction. Signature verification and
// provider certificate validation belong to the transport adapter.
type PaymentConfirmer struct {
	transactions               paidRegistrationTransactionStarter
	merchantConfigGenerationID uuid.UUID
	strictMerchantConfig       bool
	now                        func() time.Time
}

func NewPaymentConfirmer(db *sql.DB) *PaymentConfirmer {
	return newPaymentConfirmer(db, uuid.Nil, false)
}

// NewPaymentConfirmerForMerchantConfig binds API payment convergence to the
// configured merchant configuration generation. The close worker keeps the
// legacy constructor because it has its own worker-scoped admission fence.
func NewPaymentConfirmerForMerchantConfig(
	db *sql.DB,
	merchantConfigGenerationID uuid.UUID,
) *PaymentConfirmer {
	return newPaymentConfirmer(db, merchantConfigGenerationID, true)
}

// NewPaymentConfirmerForMerchantConfigWithLegacy is reserved for the close
// worker drain.  It binds new Orders to the configured generation while still
// allowing pre-791 Orders whose immutable snapshot is NULL to finish their
// already queued close job.
func NewPaymentConfirmerForMerchantConfigWithLegacy(
	db *sql.DB,
	merchantConfigGenerationID uuid.UUID,
) *PaymentConfirmer {
	return newPaymentConfirmer(db, merchantConfigGenerationID, false)
}

func newPaymentConfirmer(
	db *sql.DB,
	merchantConfigGenerationID uuid.UUID,
	strictMerchantConfig bool,
) *PaymentConfirmer {
	return &PaymentConfirmer{
		transactions:               sqlPaidRegistrationTransactionStarter{db: db},
		merchantConfigGenerationID: merchantConfigGenerationID,
		strictMerchantConfig:       strictMerchantConfig,
		now:                        time.Now,
	}
}

func (confirmer *PaymentConfirmer) requireMerchantConfigForOrder(
	ctx context.Context,
	tx queryExecutor,
	order payment.Order,
	allowDraining bool,
) error {
	if order.MerchantConfigGenerationID == uuid.Nil {
		if confirmer == nil || !confirmer.strictMerchantConfig {
			return nil
		}
		return ErrPaymentMerchantConfigUnavailable
	}
	if confirmer == nil || confirmer.merchantConfigGenerationID == uuid.Nil ||
		order.MerchantConfigGenerationID != confirmer.merchantConfigGenerationID {
		return ErrPaymentMerchantConfigUnavailable
	}
	return requireMerchantConfigGeneration(
		ctx,
		tx,
		order.TenantID,
		confirmer.merchantConfigGenerationID,
		order.PaymentAppID,
		order.PaymentMerchantID,
		allowDraining,
	)
}

func (confirmer *PaymentConfirmer) Confirm(
	ctx context.Context,
	command ConfirmPaymentCommand,
) (PaymentConfirmationResult, error) {
	return confirmer.confirm(ctx, command, nil)
}

func (confirmer *PaymentConfirmer) confirm(
	ctx context.Context,
	command ConfirmPaymentCommand,
	notification *payment.TrustedPaymentNotification,
) (PaymentConfirmationResult, error) {
	if err := validateConfirmPaymentCommand(command); err != nil {
		return PaymentConfirmationResult{}, err
	}
	if notification != nil &&
		payment.ValidateTrustedPaymentNotification(*notification) != nil {
		return PaymentConfirmationResult{}, ErrInvalidPaymentConfirmationCommand
	}

	tx, err := confirmer.transactions.beginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return PaymentConfirmationResult{}, fmt.Errorf("begin xiangwan payment confirmation transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	repository := &Repository{db: tx}
	resolvedOrder, err := repository.GetOrderByMerchantIdentity(
		ctx,
		command.TenantID,
		command.PaymentAppID,
		command.PaymentMerchantID,
		command.MerchantOrderNo,
	)
	if errors.Is(err, ErrOrderNotFound) {
		return PaymentConfirmationResult{}, ErrPaymentConfirmationNotFound
	}
	if err != nil {
		return PaymentConfirmationResult{}, err
	}
	series, err := lockPaymentConfirmationSeries(
		ctx,
		tx,
		resolvedOrder.TenantID,
		resolvedOrder.SeriesID,
	)
	if err != nil {
		return PaymentConfirmationResult{}, err
	}
	instanceStatus, err := lockPaymentConfirmationInstance(
		ctx,
		tx,
		resolvedOrder.TenantID,
		resolvedOrder.SeriesID,
		resolvedOrder.InstanceID,
	)
	if err != nil {
		return PaymentConfirmationResult{}, err
	}
	session, err := lockPaymentConfirmationSession(
		ctx,
		tx,
		resolvedOrder.TenantID,
		resolvedOrder.InstanceID,
		resolvedOrder.SessionID,
	)
	if err != nil {
		return PaymentConfirmationResult{}, err
	}
	currentRegistration, err := lockPaymentConfirmationRegistration(
		ctx,
		tx,
		resolvedOrder.TenantID,
		resolvedOrder.RegistrationID,
	)
	if err != nil {
		return PaymentConfirmationResult{}, err
	}
	currentOrder, err := repository.GetOrderByMerchantIdentityForUpdate(
		ctx,
		command.TenantID,
		command.PaymentAppID,
		command.PaymentMerchantID,
		command.MerchantOrderNo,
	)
	if err != nil {
		return PaymentConfirmationResult{}, err
	}
	if currentOrder.ID != resolvedOrder.ID {
		return PaymentConfirmationResult{}, fmt.Errorf(
			"%w: Order identity changed during resolution",
			ErrPaymentConfirmationTransaction,
		)
	}
	if err := confirmer.requireMerchantConfigForOrder(ctx, tx, currentOrder, true); err != nil {
		return PaymentConfirmationResult{}, err
	}
	currentHold, err := repository.GetCapacityHoldByOrderForUpdate(
		ctx,
		currentOrder.TenantID,
		currentOrder.ID,
	)
	if errors.Is(err, ErrCapacityHoldNotFound) {
		return PaymentConfirmationResult{}, fmt.Errorf(
			"%w: Order has no capacity hold",
			ErrPaymentConfirmationTransaction,
		)
	}
	if err != nil {
		return PaymentConfirmationResult{}, err
	}
	if !paymentContextMatchesRegistration(currentOrder, currentHold, currentRegistration) {
		return PaymentConfirmationResult{}, fmt.Errorf(
			"%w: payment context identity mismatch",
			ErrPaymentConfirmationTransaction,
		)
	}
	observation := command.Observation
	if notification != nil {
		bound, bindErr := payment.NewPaymentNotificationObservation(
			*notification,
			currentOrder.ID,
			currentOrder.PrincipalID,
		)
		if bindErr != nil {
			return PaymentConfirmationResult{}, ErrInvalidPaymentConfirmationCommand
		}
		observation = &bound
	}
	if observation != nil {
		if observation.PrincipalID != currentOrder.PrincipalID ||
			observation.OrderID != currentOrder.ID {
			return PaymentConfirmationResult{}, ErrPaymentConfirmationConflict
		}
		if err := insertPaymentTransactionObservation(
			ctx,
			tx,
			*observation,
		); err != nil {
			return PaymentConfirmationResult{}, err
		}
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
			return PaymentConfirmationResult{}, err
		}
	}

	processedAt := confirmer.now().UTC()
	if processedAt.IsZero() || command.PaidAt.After(processedAt) {
		return PaymentConfirmationResult{}, fmt.Errorf(
			"%w: paid_at is in the future",
			ErrInvalidPaymentConfirmationCommand,
		)
	}
	updatedOrder, orderChanged, err := payment.ConfirmOrderPayment(
		currentOrder,
		payment.PaymentConfirmation{
			ActualPaidCents:     command.ActualPaidCents,
			WeChatTransactionID: command.WeChatTransactionID,
			PaidAt:              command.PaidAt,
		},
	)
	if errors.Is(err, payment.ErrPaymentConfirmation) {
		return PaymentConfirmationResult{}, ErrPaymentConfirmationConflict
	}
	if err != nil {
		return PaymentConfirmationResult{}, fmt.Errorf(
			"%w: %v",
			ErrInvalidPaymentConfirmationCommand,
			err,
		)
	}
	if orderChanged {
		updatedOrder, err = repository.UpdateOrderPayment(ctx, updatedOrder, currentOrder.Version)
		if err != nil {
			return PaymentConfirmationResult{}, classifyPaymentConfirmationWriteError(err)
		}
	}

	result := PaymentConfirmationResult{
		Registration: currentRegistration,
		Order:        updatedOrder,
		Hold:         currentHold,
	}
	switch {
	case currentHold.HoldStatus == payment.CapacityHoldStatusConverted &&
		currentRegistration.ParticipationStatus == registration.ParticipationStatusConfirmed:
		result.Disposition = PaymentDispositionParticipationConfirmed
	case currentHold.HoldStatus == payment.CapacityHoldStatusConverted &&
		currentRegistration.ParticipationStatus == registration.ParticipationStatusCancelled:
		result.Disposition = PaymentDispositionRefundRequired
	case currentHold.HoldStatus == payment.CapacityHoldStatusActive &&
		currentRegistration.ParticipationStatus == registration.ParticipationStatusPendingPayment &&
		command.PaidAt.Before(currentHold.ExpiresAt) &&
		series.status == activity.SeriesStatusActive &&
		instanceStatus == activity.InstanceStatusPublished &&
		session.status == activity.SessionStatusPublished:
		result, err = confirmHeldParticipation(
			ctx,
			tx,
			repository,
			result,
			series,
			session,
			command.PaidAt,
			processedAt,
		)
		if err != nil {
			return PaymentConfirmationResult{}, err
		}
	case currentHold.HoldStatus == payment.CapacityHoldStatusActive &&
		(currentRegistration.ParticipationStatus == registration.ParticipationStatusPendingPayment ||
			currentRegistration.ParticipationStatus == registration.ParticipationStatusCancelled):
		result, err = resolvePaymentRefundRequired(
			ctx,
			tx,
			repository,
			result,
			session,
			processedAt,
		)
		if err != nil {
			return PaymentConfirmationResult{}, err
		}
	case (currentHold.HoldStatus == payment.CapacityHoldStatusReleased ||
		currentHold.HoldStatus == payment.CapacityHoldStatusExpired) &&
		currentRegistration.ParticipationStatus == registration.ParticipationStatusCancelled:
		result.Disposition = PaymentDispositionRefundRequired
	default:
		return PaymentConfirmationResult{}, fmt.Errorf(
			"%w: Registration/hold state mismatch",
			ErrPaymentConfirmationTransaction,
		)
	}
	if result.Disposition == PaymentDispositionRefundRequired {
		refundCase, ensureErr := ensurePaymentRefundCase(
			ctx,
			tx,
			result,
			paymentConfirmationRefundReason(
				currentOrder,
				currentHold,
				currentRegistration,
				series.status,
				instanceStatus,
				session.status,
				command.PaidAt,
			),
			processedAt,
		)
		if ensureErr != nil {
			return PaymentConfirmationResult{}, ensureErr
		}
		result.Refund = &refundCase
	}
	if result.Disposition == PaymentDispositionParticipationConfirmed {
		redeemedEntry, redeemErr := redeemOrderCoupon(
			ctx,
			couponRepository,
			couponLedger,
			result.Order.ID,
			processedAt,
		)
		if redeemErr != nil {
			return PaymentConfirmationResult{}, redeemErr
		}
		result.Coupon = appliedCoupon(couponLedger, redeemedEntry)
	} else {
		couponEntry, releaseErr := resolveRefundRequiredOrderCoupon(
			ctx,
			couponRepository,
			couponLedger,
			result.Order.ID,
			processedAt,
		)
		if releaseErr != nil {
			return PaymentConfirmationResult{}, releaseErr
		}
		result.Coupon = appliedCoupon(couponLedger, couponEntry)
	}

	if err := tx.Commit(); err != nil {
		return PaymentConfirmationResult{}, classifyPaymentConfirmationCommitError(err)
	}
	committed = true
	return result, nil
}

func confirmHeldParticipation(
	ctx context.Context,
	tx paidRegistrationTransaction,
	repository *Repository,
	result PaymentConfirmationResult,
	series lockedPaymentConfirmationSeries,
	session lockedPaymentConfirmationSession,
	paidAt time.Time,
	processedAt time.Time,
) (PaymentConfirmationResult, error) {
	convertedHold, changed, err := payment.ConvertCapacityHold(result.Hold, paidAt)
	if err != nil || !changed {
		return PaymentConfirmationResult{}, fmt.Errorf(
			"%w: capacity hold conversion failed: %v",
			ErrPaymentConfirmationTransaction,
			err,
		)
	}
	convertedHold, err = repository.UpdateCapacityHold(ctx, convertedHold, result.Hold.Version)
	if err != nil {
		return PaymentConfirmationResult{}, classifyPaymentConfirmationWriteError(err)
	}

	confirmedRegistration, changed, err := registration.ConfirmRegistration(
		result.Registration,
		processedAt,
	)
	if err != nil || !changed {
		return PaymentConfirmationResult{}, fmt.Errorf(
			"%w: Registration confirmation failed: %v",
			ErrPaymentConfirmationTransaction,
			err,
		)
	}
	confirmedRegistration, err = updatePaymentConfirmationRegistration(
		ctx,
		tx,
		confirmedRegistration,
		result.Registration.Version,
	)
	if err != nil {
		return PaymentConfirmationResult{}, err
	}
	if err := convertPaymentConfirmationCapacity(
		ctx,
		tx,
		result.Order.TenantID,
		result.Order.InstanceID,
		result.Order.SessionID,
		session.version,
		processedAt,
	); err != nil {
		return PaymentConfirmationResult{}, err
	}
	if err := incrementPaymentConfirmationHistory(
		ctx,
		tx,
		result.Order.TenantID,
		result.Order.SeriesID,
		series.version,
		processedAt,
	); err != nil {
		return PaymentConfirmationResult{}, err
	}

	result.Registration = confirmedRegistration
	result.Hold = convertedHold
	result.Disposition = PaymentDispositionParticipationConfirmed
	return result, nil
}

func resolvePaymentRefundRequired(
	ctx context.Context,
	tx paidRegistrationTransaction,
	repository *Repository,
	result PaymentConfirmationResult,
	session lockedPaymentConfirmationSession,
	processedAt time.Time,
) (PaymentConfirmationResult, error) {
	var (
		finishedHold payment.CapacityHold
		changed      bool
		err          error
	)
	if processedAt.Before(result.Hold.ExpiresAt) {
		finishedHold, changed, err = payment.ReleaseCapacityHold(
			result.Hold,
			"payment_refund_required",
			processedAt,
		)
	} else {
		finishedHold, changed, err = payment.ExpireCapacityHold(
			result.Hold,
			"payment_refund_required",
			processedAt,
		)
	}
	if err != nil || !changed {
		return PaymentConfirmationResult{}, fmt.Errorf(
			"%w: capacity hold release failed: %v",
			ErrPaymentConfirmationTransaction,
			err,
		)
	}
	finishedHold, err = repository.UpdateCapacityHold(ctx, finishedHold, result.Hold.Version)
	if err != nil {
		return PaymentConfirmationResult{}, classifyPaymentConfirmationWriteError(err)
	}
	if err := releasePaymentConfirmationCapacity(
		ctx,
		tx,
		result.Order.TenantID,
		result.Order.InstanceID,
		result.Order.SessionID,
		session.version,
		processedAt,
	); err != nil {
		return PaymentConfirmationResult{}, err
	}

	finishedRegistration := result.Registration
	if finishedRegistration.ParticipationStatus == registration.ParticipationStatusPendingPayment {
		finishedRegistration, changed, err = registration.CancelRegistration(
			finishedRegistration,
			"payment_refund_required",
			processedAt,
		)
		if err != nil || !changed {
			return PaymentConfirmationResult{}, fmt.Errorf(
				"%w: Registration cancellation failed: %v",
				ErrPaymentConfirmationTransaction,
				err,
			)
		}
		finishedRegistration, err = updatePaymentConfirmationRegistration(
			ctx,
			tx,
			finishedRegistration,
			result.Registration.Version,
		)
		if err != nil {
			return PaymentConfirmationResult{}, err
		}
	}

	result.Registration = finishedRegistration
	result.Hold = finishedHold
	result.Disposition = PaymentDispositionRefundRequired
	return result, nil
}

type lockedPaymentConfirmationSeries struct {
	status  activity.SeriesStatus
	version int64
}

func lockPaymentConfirmationSeries(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (lockedPaymentConfirmationSeries, error) {
	var value lockedPaymentConfirmationSeries
	err := tx.queryRowContext(ctx, `
SELECT status, version
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, seriesID).Scan(&value.status, &value.version)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedPaymentConfirmationSeries{}, ErrPaymentConfirmationTransaction
	}
	if err != nil {
		return lockedPaymentConfirmationSeries{}, fmt.Errorf("lock payment confirmation Series: %w", err)
	}
	return value, nil
}

func lockPaymentConfirmationInstance(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (activity.InstanceStatus, error) {
	var status activity.InstanceStatus
	err := tx.queryRowContext(ctx, `
SELECT status
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND series_id = $2 AND id = $3
FOR UPDATE
`, tenantID, seriesID, instanceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrPaymentConfirmationTransaction
	}
	if err != nil {
		return "", fmt.Errorf("lock payment confirmation Instance: %w", err)
	}
	return status, nil
}

type lockedPaymentConfirmationSession struct {
	status  activity.SessionStatus
	version int64
}

func lockPaymentConfirmationSession(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (lockedPaymentConfirmationSession, error) {
	var value lockedPaymentConfirmationSession
	err := tx.queryRowContext(ctx, `
SELECT status, version
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2 AND id = $3
FOR UPDATE
`, tenantID, instanceID, sessionID).Scan(&value.status, &value.version)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedPaymentConfirmationSession{}, ErrPaymentConfirmationTransaction
	}
	if err != nil {
		return lockedPaymentConfirmationSession{}, fmt.Errorf("lock payment confirmation Session: %w", err)
	}
	return value, nil
}

func lockPaymentConfirmationRegistration(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (registration.Registration, error) {
	value, err := scanPaidRegistration(tx.queryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
FROM xiangwan_registrations
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, registrationID))
	if errors.Is(err, sql.ErrNoRows) {
		return registration.Registration{}, ErrPaymentConfirmationTransaction
	}
	if err != nil {
		return registration.Registration{}, fmt.Errorf("lock payment confirmation Registration: %w", err)
	}
	return value, nil
}

func updatePaymentConfirmationRegistration(
	ctx context.Context,
	tx paidRegistrationTransaction,
	value registration.Registration,
	expectedVersion int64,
) (registration.Registration, error) {
	updated, err := scanPaidRegistration(tx.queryRowContext(ctx, `
UPDATE xiangwan_registrations
SET participation_status = $3,
    confirmed_at = $4,
    cancelled_at = $5,
    cancellation_reason = $6,
    version = version + 1,
    updated_at = $7
WHERE tenant_id = $1
  AND id = $2
  AND version = $8
RETURNING
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
`,
		value.TenantID,
		value.ID,
		value.ParticipationStatus,
		value.ConfirmedAt,
		value.CancelledAt,
		value.CancellationReason,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return registration.Registration{}, ErrPaymentConfirmationTransaction
	}
	if err != nil {
		return registration.Registration{}, fmt.Errorf("update payment confirmation Registration: %w", err)
	}
	return updated, nil
}

func convertPaymentConfirmationCapacity(
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
    confirmed_registration_count = confirmed_registration_count + 1,
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
		return ErrPaymentConfirmationTransaction
	}
	if err != nil {
		return fmt.Errorf("convert payment confirmation Session capacity: %w", err)
	}
	return nil
}

func releasePaymentConfirmationCapacity(
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
		return ErrPaymentConfirmationTransaction
	}
	if err != nil {
		return fmt.Errorf("release payment confirmation Session capacity: %w", err)
	}
	return nil
}

func incrementPaymentConfirmationHistory(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	expectedVersion int64,
	now time.Time,
) error {
	var newVersion int64
	err := tx.queryRowContext(ctx, `
UPDATE xiangwan_activity_series
SET historical_registration_count = historical_registration_count + 1,
    version = version + 1,
    updated_at = $3
WHERE tenant_id = $1
  AND id = $2
  AND version = $4
RETURNING version
`, tenantID, seriesID, now, expectedVersion).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPaymentConfirmationTransaction
	}
	if err != nil {
		return fmt.Errorf("increment payment confirmation Series history: %w", err)
	}
	return nil
}

func validateConfirmPaymentCommand(command ConfirmPaymentCommand) error {
	switch {
	case command.TenantID == uuid.Nil:
	case invalidConfirmationText(command.PaymentAppID, 64):
	case invalidConfirmationText(command.PaymentMerchantID, 64):
	case invalidConfirmationText(command.MerchantOrderNo, 64):
	case invalidConfirmationText(command.WeChatTransactionID, 128):
	case command.ActualPaidCents <= 0:
	case command.PaidAt.IsZero():
	case command.Observation != nil &&
		!paymentConfirmationMatchesObservation(command, *command.Observation):
	default:
		return nil
	}
	return ErrInvalidPaymentConfirmationCommand
}

func paymentConfirmationMatchesObservation(
	command ConfirmPaymentCommand,
	observation payment.TransactionObservation,
) bool {
	return payment.ValidateTransactionObservation(observation) == nil &&
		observation.TenantID == command.TenantID &&
		observation.PaymentAppID == command.PaymentAppID &&
		observation.PaymentMerchantID == command.PaymentMerchantID &&
		observation.OutTradeNo == command.MerchantOrderNo &&
		observation.TransactionID == command.WeChatTransactionID &&
		observation.TradeState == payment.ProviderTradeStateSuccess &&
		observation.AmountCents == command.ActualPaidCents &&
		observation.SuccessAt != nil && observation.SuccessAt.Equal(command.PaidAt)
}

func invalidConfirmationText(value string, maxRunes int) bool {
	return value == "" ||
		value != strings.TrimSpace(value) ||
		len([]rune(value)) > maxRunes
}

func classifyPaymentConfirmationWriteError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) &&
		postgresError.Code == "23505" &&
		postgresError.ConstraintName == "uq_xiangwan_orders_wechat_transaction" {
		return ErrPaymentConfirmationConflict
	}
	if errors.Is(err, ErrOrderVersionConflict) ||
		errors.Is(err, ErrCapacityHoldVersionConflict) {
		return ErrPaymentConfirmationTransaction
	}
	return err
}

func classifyPaymentConfirmationCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "40001" {
		return fmt.Errorf("%w: %v", ErrPaymentConfirmationTransaction, err)
	}
	return fmt.Errorf("commit xiangwan payment confirmation transaction: %w", err)
}
