package registrationpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	refundpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type CancellationSource string

const (
	CancellationSourceSelf     CancellationSource = "self"
	CancellationSourceOperator CancellationSource = "operator"
)

var (
	ErrInvalidRegistrationCancellationCommand = errors.New("invalid xiangwan Registration cancellation command")
	ErrRegistrationCancellationNotFound       = errors.New("xiangwan Registration cancellation target not found")
	ErrRegistrationCancellationForbidden      = errors.New("xiangwan Registration cancellation actor is forbidden")
	ErrRegistrationCancellationPolicyMissing  = errors.New("xiangwan self-cancellation policy is unavailable")
	ErrRegistrationCouponPolicyMissing        = errors.New("xiangwan Registration cancellation Coupon policy is unavailable")
	ErrRegistrationCancellationNotAllowed     = errors.New("xiangwan Registration self-cancellation is not allowed")
	ErrRegistrationCancellationConflict       = errors.New("xiangwan Registration cancellation conflicts with recorded fact")
	ErrRegistrationCancellationTransaction    = errors.New("xiangwan Registration cancellation transaction conflict")
)

type CancelRegistrationCommand struct {
	TenantID       uuid.UUID
	RegistrationID uuid.UUID
	ActorID        uuid.UUID
	Source         CancellationSource
	Reason         string
}

type PaidSelfCancellationPolicyInput struct {
	TenantID       uuid.UUID
	RegistrationID uuid.UUID
	PrincipalID    uuid.UUID
	SessionID      uuid.UUID
	SessionStartAt time.Time
	OrderStatus    payment.OrderStatus
	EvaluatedAt    time.Time
}

type PaidSelfCancellationPolicyDecision struct {
	Allowed       bool
	FullRefund    bool
	PolicyVersion string
}

// PaidSelfCancellationPolicy must be a local or PostgreSQL-backed evaluator.
// The cancellation transaction never calls an external policy service while
// holding capacity and financial row locks.
type PaidSelfCancellationPolicy interface {
	EvaluatePaidSelfCancellation(
		context.Context,
		PaidSelfCancellationPolicyInput,
	) (PaidSelfCancellationPolicyDecision, error)
}

type RegistrationCancellationResult struct {
	Registration     registration.Registration
	Order            *payment.Order
	Hold             *payment.CapacityHold
	Refund           *refund.Case
	CouponAdjustment *coupon.Entry
	PolicyVersion    string
}

// RegistrationCanceller revokes participation and capacity immediately. Paid
// cancellations create the one manual Refund case in the same serializable
// PostgreSQL transaction; manual refund completion is deliberately separate.
type RegistrationCanceller struct {
	registrations cancellationRegistrationResolver
	transactions  cancellationTransactionStarter
	policy        PaidSelfCancellationPolicy
	selfPolicy    registration.SelfCancellationPolicy
	couponPolicy  coupon.RefundPolicyEvaluator
	now           func() time.Time
}

func NewRegistrationCanceller(
	db *sql.DB,
	policy PaidSelfCancellationPolicy,
) *RegistrationCanceller {
	return NewRegistrationCancellerWithCouponRefundPolicy(db, policy, nil)
}

func NewRegistrationCancellerWithCouponRefundPolicy(
	db *sql.DB,
	policy PaidSelfCancellationPolicy,
	couponPolicy coupon.RefundPolicyEvaluator,
) *RegistrationCanceller {
	selfPolicy, _ := policy.(registration.SelfCancellationPolicy)
	return &RegistrationCanceller{
		registrations: NewRepository(db),
		transactions: cancellationSQLTransactionStarter{
			db: db,
		},
		policy:       policy,
		selfPolicy:   selfPolicy,
		couponPolicy: couponPolicy,
		now:          time.Now,
	}
}

func (canceller *RegistrationCanceller) Cancel(
	ctx context.Context,
	command CancelRegistrationCommand,
) (RegistrationCancellationResult, error) {
	if err := validateCancellationCommand(command); err != nil {
		return RegistrationCancellationResult{}, err
	}

	located, err := canceller.registrations.Get(
		ctx,
		command.TenantID,
		command.RegistrationID,
	)
	if errors.Is(err, ErrRegistrationNotFound) {
		return RegistrationCancellationResult{}, ErrRegistrationCancellationNotFound
	}
	if err != nil {
		return RegistrationCancellationResult{}, err
	}

	tx, err := canceller.transactions.beginCancellationTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return RegistrationCancellationResult{}, fmt.Errorf(
			"begin xiangwan Registration cancellation transaction: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := tx.lockSeries(ctx, located.TenantID, located.SeriesID); err != nil {
		return RegistrationCancellationResult{}, err
	}
	if err := tx.lockInstance(
		ctx,
		located.TenantID,
		located.SeriesID,
		located.InstanceID,
	); err != nil {
		return RegistrationCancellationResult{}, err
	}
	session, err := tx.lockSession(
		ctx,
		located.TenantID,
		located.InstanceID,
		located.SessionID,
	)
	if err != nil {
		return RegistrationCancellationResult{}, err
	}
	current, err := tx.lockRegistration(
		ctx,
		located.TenantID,
		located.ID,
	)
	if err != nil {
		return RegistrationCancellationResult{}, err
	}
	if !sameCancellationRegistrationIdentity(located, current) {
		return RegistrationCancellationResult{}, ErrRegistrationCancellationTransaction
	}
	if err := authorizeCancellation(command, current); err != nil {
		return RegistrationCancellationResult{}, err
	}

	currentOrder, currentHold, hasPayment, err := loadCancellationPaymentContext(
		ctx,
		tx,
		current,
	)
	if err != nil {
		return RegistrationCancellationResult{}, err
	}
	var currentCoupon *couponpostgres.Ledger
	if hasPayment && currentOrder.PaymentStatus == payment.OrderStatusSettledZero {
		ledger, ledgerErr := tx.lockCouponByOrder(
			ctx,
			currentOrder.TenantID,
			currentOrder.ID,
		)
		if ledgerErr != nil {
			return RegistrationCancellationResult{}, ledgerErr
		}
		state, stateErr := coupon.InspectRefundPolicyOrder(
			ledger.Instrument,
			ledger.Entries,
			currentOrder.ID,
			current.ID,
		)
		if stateErr != nil ||
			ledger.Instrument.TenantID != current.TenantID ||
			ledger.Instrument.PrincipalID != current.PrincipalID {
			return RegistrationCancellationResult{},
				ErrRegistrationCancellationTransaction
		}
		if current.ParticipationStatus == registration.ParticipationStatusCancelled {
			if state.Adjustment == nil {
				return RegistrationCancellationResult{},
					ErrRegistrationCancellationTransaction
			}
		} else if state.Adjustment != nil {
			return RegistrationCancellationResult{},
				ErrRegistrationCancellationTransaction
		}
		currentCoupon = &ledger
	}
	if current.ParticipationStatus == registration.ParticipationStatusCancelled {
		result, replayErr := replayRegistrationCancellation(
			ctx,
			tx,
			command,
			current,
			currentOrder,
			currentHold,
			hasPayment,
			currentCoupon,
		)
		if replayErr != nil {
			return RegistrationCancellationResult{}, replayErr
		}
		if err := tx.Commit(); err != nil {
			return RegistrationCancellationResult{}, classifyCancellationCommitError(err)
		}
		committed = true
		return result, nil
	}

	processedAt := canceller.now().UTC().Truncate(time.Microsecond)
	if processedAt.IsZero() || processedAt.Before(current.CreatedAt) {
		return RegistrationCancellationResult{}, ErrInvalidRegistrationCancellationCommand
	}
	policyVersion, err := canceller.evaluateCancellationPolicy(
		ctx,
		command,
		current,
		currentOrder,
		hasPayment,
		session,
		processedAt,
	)
	if err != nil {
		return RegistrationCancellationResult{}, err
	}
	cancellationReason := cancellationReasonForCommand(command, policyVersion)
	cancelled, changed, err := registration.CancelRegistration(
		current,
		cancellationReason,
		processedAt,
	)
	if err != nil || !changed {
		return RegistrationCancellationResult{}, fmt.Errorf(
			"%w: cancel participation: %v",
			ErrRegistrationCancellationTransaction,
			err,
		)
	}

	result := RegistrationCancellationResult{
		Registration:  cancelled,
		PolicyVersion: policyVersion,
	}
	if hasPayment {
		result.Order = &currentOrder
		result.Hold = &currentHold
	}
	result, err = resolveCancellationState(
		ctx,
		tx,
		command,
		result,
		current,
		session,
		processedAt,
		currentCoupon,
		canceller.couponPolicy,
	)
	if err != nil {
		return RegistrationCancellationResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return RegistrationCancellationResult{}, classifyCancellationCommitError(err)
	}
	committed = true
	return result, nil
}

func (canceller *RegistrationCanceller) evaluateCancellationPolicy(
	ctx context.Context,
	command CancelRegistrationCommand,
	current registration.Registration,
	currentOrder payment.Order,
	hasPayment bool,
	session lockedCancellationSession,
	evaluatedAt time.Time,
) (string, error) {
	if command.Source != CancellationSourceSelf {
		return "", nil
	}
	cutoffDecision, err := canceller.evaluateSelfCancellationPolicy(
		ctx,
		current,
		session,
		evaluatedAt,
	)
	if err != nil {
		return "", err
	}
	if !cutoffDecision.Allowed {
		return "", ErrRegistrationCancellationNotAllowed
	}
	if !hasPayment ||
		(currentOrder.PaymentStatus != payment.OrderStatusPaidConfirmed &&
			currentOrder.PaymentStatus != payment.OrderStatusUnknown &&
			currentOrder.PaymentStatus != payment.OrderStatusSettledZero) {
		return cutoffDecision.PolicyVersion, nil
	}
	if canceller.policy == nil {
		return "", ErrRegistrationCancellationPolicyMissing
	}
	decision, err := canceller.policy.EvaluatePaidSelfCancellation(
		ctx,
		PaidSelfCancellationPolicyInput{
			TenantID:       current.TenantID,
			RegistrationID: current.ID,
			PrincipalID:    current.PrincipalID,
			SessionID:      current.SessionID,
			SessionStartAt: session.sessionStartAt,
			OrderStatus:    currentOrder.PaymentStatus,
			EvaluatedAt:    evaluatedAt,
		},
	)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrRegistrationCancellationPolicyMissing, err)
	}
	if !decision.Allowed {
		return "", ErrRegistrationCancellationNotAllowed
	}
	if !decision.FullRefund || decision.PolicyVersion == "" ||
		registration.ValidateSelfCancellationPolicyDecision(
			registration.SelfCancellationPolicyDecision{
				Allowed:       decision.Allowed,
				PolicyVersion: decision.PolicyVersion,
			},
		) != nil {
		return "", ErrRegistrationCancellationPolicyMissing
	}
	if cutoffDecision.PolicyVersion != "" &&
		decision.PolicyVersion != cutoffDecision.PolicyVersion {
		return "", ErrRegistrationCancellationPolicyMissing
	}
	return decision.PolicyVersion, nil
}

func (canceller *RegistrationCanceller) evaluateSelfCancellationPolicy(
	ctx context.Context,
	current registration.Registration,
	session lockedCancellationSession,
	evaluatedAt time.Time,
) (registration.SelfCancellationPolicyDecision, error) {
	input := registration.SelfCancellationPolicyInput{
		TenantID:       current.TenantID,
		RegistrationID: current.ID,
		PrincipalID:    current.PrincipalID,
		SessionID:      current.SessionID,
		SessionStartAt: session.sessionStartAt,
		EvaluatedAt:    evaluatedAt,
	}
	var (
		decision registration.SelfCancellationPolicyDecision
		err      error
	)
	if canceller.selfPolicy == nil {
		decision, err = registration.EvaluateSelfCancellationCutoff(
			input,
			"",
			0,
		)
	} else {
		decision, err = canceller.selfPolicy.EvaluateSelfCancellation(ctx, input)
	}
	if err != nil ||
		registration.ValidateSelfCancellationPolicyDecision(decision) != nil {
		return registration.SelfCancellationPolicyDecision{},
			ErrRegistrationCancellationPolicyMissing
	}
	return decision, nil
}

func resolveCancellationState(
	ctx context.Context,
	tx cancellationTransaction,
	command CancelRegistrationCommand,
	result RegistrationCancellationResult,
	current registration.Registration,
	session lockedCancellationSession,
	processedAt time.Time,
	couponLedger *couponpostgres.Ledger,
	couponPolicy coupon.RefundPolicyEvaluator,
) (RegistrationCancellationResult, error) {
	if result.Order == nil {
		if current.ParticipationStatus != registration.ParticipationStatusConfirmed {
			return RegistrationCancellationResult{}, ErrRegistrationCancellationTransaction
		}
		if err := tx.decrementConfirmedCapacity(
			ctx,
			current.TenantID,
			current.InstanceID,
			current.SessionID,
			session.version,
			processedAt,
		); err != nil {
			return RegistrationCancellationResult{}, err
		}
		updated, err := tx.updateRegistration(ctx, result.Registration, current.Version)
		if err != nil {
			return RegistrationCancellationResult{}, classifyCancellationWriteError(err)
		}
		result.Registration = updated
		return result, nil
	}

	currentOrder := *result.Order
	currentHold := *result.Hold
	if current.ParticipationStatus == registration.ParticipationStatusConfirmed {
		if currentHold.HoldStatus != payment.CapacityHoldStatusConverted {
			return RegistrationCancellationResult{}, ErrRegistrationCancellationTransaction
		}
		switch currentOrder.PaymentStatus {
		case payment.OrderStatusPaidConfirmed:
			if currentOrder.ActualPaidCents == nil ||
				*currentOrder.ActualPaidCents <= 0 {
				return RegistrationCancellationResult{},
					ErrRegistrationCancellationTransaction
			}
		case payment.OrderStatusSettledZero:
			if currentOrder.ActualPaidCents != nil ||
				currentOrder.PayableCents != 0 ||
				currentOrder.DiscountCents <= 0 ||
				currentOrder.DiscountCents != currentOrder.OriginalPriceCents ||
				couponLedger == nil {
				return RegistrationCancellationResult{},
					ErrRegistrationCancellationTransaction
			}
		default:
			return RegistrationCancellationResult{},
				ErrRegistrationCancellationTransaction
		}
		if err := tx.decrementConfirmedCapacity(
			ctx,
			current.TenantID,
			current.InstanceID,
			current.SessionID,
			session.version,
			processedAt,
		); err != nil {
			return RegistrationCancellationResult{}, err
		}
	} else if current.ParticipationStatus == registration.ParticipationStatusPendingPayment {
		if currentHold.HoldStatus != payment.CapacityHoldStatusActive ||
			(currentOrder.PaymentStatus != payment.OrderStatusPending &&
				currentOrder.PaymentStatus != payment.OrderStatusUnknown &&
				currentOrder.PaymentStatus != payment.OrderStatusClosedUnpaid) {
			return RegistrationCancellationResult{}, ErrRegistrationCancellationTransaction
		}

		finishedHold, _, err := finishCancellationHold(currentHold, processedAt)
		if err != nil {
			return RegistrationCancellationResult{}, err
		}
		finishedHold, err = tx.updateHold(ctx, finishedHold, currentHold.Version)
		if err != nil {
			return RegistrationCancellationResult{}, classifyCancellationWriteError(err)
		}
		result.Hold = &finishedHold

		if currentOrder.PaymentStatus == payment.OrderStatusPending {
			closedOrder, changed, closeErr := payment.CloseOrderUnpaid(currentOrder, processedAt)
			if closeErr != nil || !changed {
				return RegistrationCancellationResult{}, fmt.Errorf(
					"%w: close pending Order: %v",
					ErrRegistrationCancellationTransaction,
					closeErr,
				)
			}
			closedOrder, err = tx.updateOrder(ctx, closedOrder, currentOrder.Version)
			if err != nil {
				return RegistrationCancellationResult{}, classifyCancellationWriteError(err)
			}
			result.Order = &closedOrder
		}
		if currentOrder.DiscountCents > 0 {
			if err := tx.releaseCouponForOrder(
				ctx,
				currentOrder.TenantID,
				currentOrder.ID,
				"registration_cancelled",
				processedAt,
			); err != nil {
				return RegistrationCancellationResult{}, err
			}
		}
		if err := tx.decrementHoldCapacity(
			ctx,
			current.TenantID,
			current.InstanceID,
			current.SessionID,
			session.version,
			processedAt,
		); err != nil {
			return RegistrationCancellationResult{}, err
		}
	} else {
		return RegistrationCancellationResult{}, ErrRegistrationCancellationTransaction
	}

	updated, err := tx.updateRegistration(ctx, result.Registration, current.Version)
	if err != nil {
		return RegistrationCancellationResult{}, classifyCancellationWriteError(err)
	}
	result.Registration = updated
	if currentOrder.PaymentStatus == payment.OrderStatusPaidConfirmed {
		refundCase, refundErr := ensureCancellationRefund(
			ctx,
			tx,
			command,
			result,
			processedAt,
		)
		if refundErr != nil {
			return RegistrationCancellationResult{}, refundErr
		}
		result.Refund = &refundCase
	} else if currentOrder.PaymentStatus == payment.OrderStatusSettledZero {
		adjustment, adjustmentErr := applyRegistrationCouponPolicy(
			ctx,
			tx,
			command,
			currentOrder,
			*couponLedger,
			processedAt,
			couponPolicy,
		)
		if adjustmentErr != nil {
			return RegistrationCancellationResult{}, adjustmentErr
		}
		result.CouponAdjustment = &adjustment
	}
	return result, nil
}

func finishCancellationHold(
	current payment.CapacityHold,
	processedAt time.Time,
) (payment.CapacityHold, bool, error) {
	if processedAt.Before(current.ExpiresAt) {
		return payment.ReleaseCapacityHold(current, "registration_cancelled", processedAt)
	}
	return payment.ExpireCapacityHold(current, "registration_cancelled", processedAt)
}

func ensureCancellationRefund(
	ctx context.Context,
	tx cancellationTransaction,
	command CancelRegistrationCommand,
	result RegistrationCancellationResult,
	processedAt time.Time,
) (refund.Case, error) {
	if result.Order == nil || result.Order.ActualPaidCents == nil {
		return refund.Case{}, ErrRegistrationCancellationTransaction
	}
	existing, err := tx.lockRefundByOrder(ctx, result.Order.TenantID, result.Order.ID)
	if err == nil {
		if !cancellationRefundMatches(existing, result) {
			return refund.Case{}, ErrRegistrationCancellationConflict
		}
		return existing, nil
	}
	if !errors.Is(err, errCancellationRefundNotFound) {
		return refund.Case{}, err
	}

	reasonCode := refund.ReasonUserCancelled
	operatorNote := ""
	if command.Source == CancellationSourceOperator {
		reasonCode = refund.ReasonOperatorAdjustment
		operatorNote = "Registration cancellation approved by operator " + command.ActorID.String()
	} else if result.PolicyVersion != "" {
		operatorNote = "cancel_policy_version=" + result.PolicyVersion
	}
	created, err := refund.NewCase(refund.NewCaseCommand{
		TenantID:             result.Order.TenantID,
		OrderID:              result.Order.ID,
		RegistrationID:       result.Order.RegistrationID,
		SeriesID:             result.Order.SeriesID,
		InstanceID:           result.Order.InstanceID,
		SessionID:            result.Order.SessionID,
		PrincipalID:          result.Order.PrincipalID,
		ReasonCode:           reasonCode,
		IdempotencyKey:       "refund:cancellation:" + result.Registration.ID.String(),
		ActualPaidCents:      *result.Order.ActualPaidCents,
		RequestedRefundCents: *result.Order.ActualPaidCents,
		OperatorNote:         operatorNote,
		Now:                  processedAt,
	})
	if err != nil {
		return refund.Case{}, fmt.Errorf(
			"%w: construct cancellation Refund case: %v",
			ErrRegistrationCancellationTransaction,
			err,
		)
	}
	created, err = tx.createRefund(ctx, created)
	if err != nil {
		return refund.Case{}, classifyCancellationWriteError(err)
	}
	return created, nil
}

func applyRegistrationCouponPolicy(
	ctx context.Context,
	tx cancellationTransaction,
	command CancelRegistrationCommand,
	order payment.Order,
	ledger couponpostgres.Ledger,
	processedAt time.Time,
	policy coupon.RefundPolicyEvaluator,
) (coupon.Entry, error) {
	if policy == nil {
		return coupon.Entry{}, ErrRegistrationCouponPolicyMissing
	}
	state, err := coupon.InspectRefundPolicyOrder(
		ledger.Instrument,
		ledger.Entries,
		order.ID,
		order.RegistrationID,
	)
	if err != nil || state.Adjustment != nil {
		return coupon.Entry{}, ErrRegistrationCancellationTransaction
	}
	input := coupon.RefundPolicyInput{
		TenantID:       order.TenantID,
		CouponID:       ledger.Instrument.ID,
		OrderID:        order.ID,
		RegistrationID: order.RegistrationID,
		Trigger:        coupon.RefundTriggerSettledZeroCancellation,
		Reason:         "Registration cancellation settled-zero Coupon",
		EvaluatedAt:    processedAt,
	}
	decision, err := policy.EvaluateCouponRefund(ctx, input)
	if err != nil {
		return coupon.Entry{}, fmt.Errorf(
			"%w: %v",
			ErrRegistrationCouponPolicyMissing,
			err,
		)
	}
	if err := coupon.ValidateRefundPolicyDecision(decision); err != nil {
		return coupon.Entry{}, fmt.Errorf(
			"%w: %v",
			ErrRegistrationCouponPolicyMissing,
			err,
		)
	}
	adjustment, err := coupon.ApplyRefundPolicy(coupon.ApplyRefundPolicyCommand{
		Instrument: ledger.Instrument,
		History:    ledger.Entries,
		Input:      input,
		Decision:   decision,
		ActorID:    command.ActorID,
		RecordedAt: processedAt,
	})
	if err != nil {
		return coupon.Entry{}, fmt.Errorf(
			"%w: construct Coupon adjustment: %v",
			ErrRegistrationCancellationTransaction,
			err,
		)
	}
	adjustment, err = tx.appendCouponEntry(ctx, adjustment)
	if err != nil {
		return coupon.Entry{}, classifyCancellationWriteError(err)
	}
	return adjustment, nil
}

func replayRegistrationCancellation(
	ctx context.Context,
	tx cancellationTransaction,
	command CancelRegistrationCommand,
	current registration.Registration,
	currentOrder payment.Order,
	currentHold payment.CapacityHold,
	hasPayment bool,
	couponLedger *couponpostgres.Ledger,
) (RegistrationCancellationResult, error) {
	if current.CancellationReason == nil ||
		!cancellationReasonMatchesCommand(*current.CancellationReason, command) {
		return RegistrationCancellationResult{}, ErrRegistrationCancellationConflict
	}
	result := RegistrationCancellationResult{
		Registration:  current,
		PolicyVersion: cancellationPolicyVersion(*current.CancellationReason),
	}
	if !hasPayment {
		return result, nil
	}
	result.Order = &currentOrder
	result.Hold = &currentHold
	if currentOrder.PaymentStatus == payment.OrderStatusPaidConfirmed {
		refundCase, err := tx.lockRefundByOrder(ctx, currentOrder.TenantID, currentOrder.ID)
		if errors.Is(err, errCancellationRefundNotFound) {
			return RegistrationCancellationResult{}, ErrRegistrationCancellationTransaction
		}
		if err != nil {
			return RegistrationCancellationResult{}, err
		}
		if !cancellationRefundMatches(refundCase, result) {
			return RegistrationCancellationResult{}, ErrRegistrationCancellationConflict
		}
		result.Refund = &refundCase
	} else if currentOrder.PaymentStatus == payment.OrderStatusSettledZero {
		if couponLedger == nil {
			return RegistrationCancellationResult{},
				ErrRegistrationCancellationTransaction
		}
		state, err := coupon.InspectRefundPolicyOrder(
			couponLedger.Instrument,
			couponLedger.Entries,
			currentOrder.ID,
			current.ID,
		)
		if err != nil || state.Adjustment == nil {
			return RegistrationCancellationResult{},
				ErrRegistrationCancellationTransaction
		}
		result.CouponAdjustment = state.Adjustment
	}
	return result, nil
}

func loadCancellationPaymentContext(
	ctx context.Context,
	tx cancellationTransaction,
	current registration.Registration,
) (payment.Order, payment.CapacityHold, bool, error) {
	currentOrder, err := tx.lockOrderByRegistration(ctx, current.TenantID, current.ID)
	if errors.Is(err, errCancellationOrderNotFound) {
		return payment.Order{}, payment.CapacityHold{}, false, nil
	}
	if err != nil {
		return payment.Order{}, payment.CapacityHold{}, false, err
	}
	currentHold, err := tx.lockHoldByOrder(ctx, current.TenantID, currentOrder.ID)
	if err != nil {
		return payment.Order{}, payment.CapacityHold{}, false, err
	}
	if currentOrder.TenantID != current.TenantID ||
		currentOrder.RegistrationID != current.ID ||
		currentOrder.SeriesID != current.SeriesID ||
		currentOrder.InstanceID != current.InstanceID ||
		currentOrder.SessionID != current.SessionID ||
		currentOrder.PrincipalID != current.PrincipalID ||
		currentHold.TenantID != current.TenantID ||
		currentHold.OrderID != currentOrder.ID ||
		currentHold.RegistrationID != current.ID ||
		currentHold.SessionID != current.SessionID {
		return payment.Order{}, payment.CapacityHold{}, false, ErrRegistrationCancellationTransaction
	}
	return currentOrder, currentHold, true, nil
}

func validateCancellationCommand(command CancelRegistrationCommand) error {
	if command.TenantID == uuid.Nil ||
		command.RegistrationID == uuid.Nil ||
		command.ActorID == uuid.Nil {
		return ErrInvalidRegistrationCancellationCommand
	}
	switch command.Source {
	case CancellationSourceSelf:
		if command.Reason != "" {
			return ErrInvalidRegistrationCancellationCommand
		}
	case CancellationSourceOperator:
		reason := strings.TrimSpace(command.Reason)
		if reason == "" || reason != command.Reason || len([]rune(reason)) > 400 {
			return ErrInvalidRegistrationCancellationCommand
		}
	default:
		return ErrInvalidRegistrationCancellationCommand
	}
	return nil
}

func authorizeCancellation(
	command CancelRegistrationCommand,
	current registration.Registration,
) error {
	if command.Source == CancellationSourceSelf && command.ActorID != current.PrincipalID {
		return ErrRegistrationCancellationForbidden
	}
	return nil
}

func cancellationReasonForCommand(
	command CancelRegistrationCommand,
	policyVersion string,
) string {
	if command.Source == CancellationSourceSelf {
		if policyVersion == "" {
			return "user_cancelled"
		}
		return "user_cancelled@" + policyVersion
	}
	return "operator_cancelled:" + command.ActorID.String() + ":" + command.Reason
}

func cancellationReasonMatchesCommand(recorded string, command CancelRegistrationCommand) bool {
	if command.Source == CancellationSourceSelf {
		return recorded == "user_cancelled" || strings.HasPrefix(recorded, "user_cancelled@")
	}
	return recorded == cancellationReasonForCommand(command, "")
}

func cancellationPolicyVersion(recordedReason string) string {
	const prefix = "user_cancelled@"
	if !strings.HasPrefix(recordedReason, prefix) {
		return ""
	}
	return strings.TrimPrefix(recordedReason, prefix)
}

func sameCancellationRegistrationIdentity(
	located registration.Registration,
	locked registration.Registration,
) bool {
	return located.ID == locked.ID &&
		located.TenantID == locked.TenantID &&
		located.SeriesID == locked.SeriesID &&
		located.InstanceID == locked.InstanceID &&
		located.SessionID == locked.SessionID &&
		located.PrincipalID == locked.PrincipalID
}

func cancellationRefundMatches(
	value refund.Case,
	result RegistrationCancellationResult,
) bool {
	return result.Order != nil &&
		result.Order.ActualPaidCents != nil &&
		value.TenantID == result.Order.TenantID &&
		value.OrderID == result.Order.ID &&
		value.RegistrationID == result.Registration.ID &&
		value.SeriesID == result.Registration.SeriesID &&
		value.InstanceID == result.Registration.InstanceID &&
		value.SessionID == result.Registration.SessionID &&
		value.PrincipalID == result.Registration.PrincipalID &&
		value.RequestedRefundCents == *result.Order.ActualPaidCents
}

type lockedCancellationSession struct {
	sessionStartAt time.Time
	version        int64
}

var (
	errCancellationOrderNotFound  = errors.New("cancellation Order not found")
	errCancellationRefundNotFound = errors.New("cancellation Refund case not found")
)

type cancellationRegistrationResolver interface {
	Get(context.Context, uuid.UUID, uuid.UUID) (registration.Registration, error)
}

type cancellationTransaction interface {
	lockSeries(context.Context, uuid.UUID, uuid.UUID) error
	lockInstance(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
	lockSession(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (lockedCancellationSession, error)
	lockRegistration(context.Context, uuid.UUID, uuid.UUID) (registration.Registration, error)
	lockOrderByRegistration(context.Context, uuid.UUID, uuid.UUID) (payment.Order, error)
	lockHoldByOrder(context.Context, uuid.UUID, uuid.UUID) (payment.CapacityHold, error)
	lockRefundByOrder(context.Context, uuid.UUID, uuid.UUID) (refund.Case, error)
	lockCouponByOrder(context.Context, uuid.UUID, uuid.UUID) (couponpostgres.Ledger, error)
	updateRegistration(context.Context, registration.Registration, int64) (registration.Registration, error)
	updateOrder(context.Context, payment.Order, int64) (payment.Order, error)
	updateHold(context.Context, payment.CapacityHold, int64) (payment.CapacityHold, error)
	releaseCouponForOrder(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error
	appendCouponEntry(context.Context, coupon.Entry) (coupon.Entry, error)
	createRefund(context.Context, refund.Case) (refund.Case, error)
	decrementConfirmedCapacity(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64, time.Time) error
	decrementHoldCapacity(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64, time.Time) error
	Commit() error
	Rollback() error
}

type cancellationTransactionStarter interface {
	beginCancellationTx(context.Context, *sql.TxOptions) (cancellationTransaction, error)
}

type cancellationSQLTransactionStarter struct {
	db *sql.DB
}

func (starter cancellationSQLTransactionStarter) beginCancellationTx(
	ctx context.Context,
	options *sql.TxOptions,
) (cancellationTransaction, error) {
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return newCancellationSQLTransaction(tx), nil
}

type cancellationSQLTransaction struct {
	tx            *sql.Tx
	registrations *Repository
	payments      *paymentpostgres.Repository
	refunds       *refundpostgres.Repository
	coupons       *couponpostgres.Repository
}

func newCancellationSQLTransaction(tx *sql.Tx) *cancellationSQLTransaction {
	return &cancellationSQLTransaction{
		tx:            tx,
		registrations: NewRepository(tx),
		payments:      paymentpostgres.NewRepository(tx),
		refunds:       refundpostgres.NewRepository(tx),
		coupons:       couponpostgres.NewRepository(tx),
	}
}

func (tx *cancellationSQLTransaction) lockSeries(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) error {
	var version int64
	err := tx.tx.QueryRowContext(ctx, `
SELECT version
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, seriesID).Scan(&version)
	return cancellationLockError("Series", err)
}

func (tx *cancellationSQLTransaction) lockInstance(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) error {
	var version int64
	err := tx.tx.QueryRowContext(ctx, `
SELECT version
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND series_id = $2 AND id = $3
FOR UPDATE
`, tenantID, seriesID, instanceID).Scan(&version)
	return cancellationLockError("Instance", err)
}

func (tx *cancellationSQLTransaction) lockSession(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (lockedCancellationSession, error) {
	var value lockedCancellationSession
	var sessionStartAt sql.NullTime
	err := tx.tx.QueryRowContext(ctx, `
SELECT session_start_at, version
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2 AND id = $3
FOR UPDATE
`, tenantID, instanceID, sessionID).Scan(&sessionStartAt, &value.version)
	if err := cancellationLockError("Session", err); err != nil {
		return lockedCancellationSession{}, err
	}
	value.sessionStartAt = sessionStartAt.Time
	return value, nil
}

func (tx *cancellationSQLTransaction) lockRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (registration.Registration, error) {
	value, err := tx.registrations.GetForUpdate(ctx, tenantID, registrationID)
	if errors.Is(err, ErrRegistrationNotFound) {
		return registration.Registration{}, ErrRegistrationCancellationNotFound
	}
	return value, err
}

func (tx *cancellationSQLTransaction) lockOrderByRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (payment.Order, error) {
	value, err := tx.payments.GetOrderByRegistrationForUpdate(ctx, tenantID, registrationID)
	if errors.Is(err, paymentpostgres.ErrOrderNotFound) {
		return payment.Order{}, errCancellationOrderNotFound
	}
	return value, err
}

func (tx *cancellationSQLTransaction) lockHoldByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (payment.CapacityHold, error) {
	value, err := tx.payments.GetCapacityHoldByOrderForUpdate(ctx, tenantID, orderID)
	if errors.Is(err, paymentpostgres.ErrCapacityHoldNotFound) {
		return payment.CapacityHold{}, ErrRegistrationCancellationTransaction
	}
	return value, err
}

func (tx *cancellationSQLTransaction) lockRefundByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (refund.Case, error) {
	value, err := tx.refunds.GetByOrderForUpdate(ctx, tenantID, orderID)
	if errors.Is(err, refundpostgres.ErrRefundCaseNotFound) {
		return refund.Case{}, errCancellationRefundNotFound
	}
	return value, err
}

func (tx *cancellationSQLTransaction) lockCouponByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (couponpostgres.Ledger, error) {
	value, err := tx.coupons.GetLedgerByOrderForUpdate(ctx, tenantID, orderID)
	if errors.Is(err, couponpostgres.ErrCouponNotFound) ||
		errors.Is(err, couponpostgres.ErrGrantFactsConflict) ||
		errors.Is(err, coupon.ErrInvalidLedger) {
		return couponpostgres.Ledger{}, ErrRegistrationCancellationTransaction
	}
	return value, err
}

func (tx *cancellationSQLTransaction) updateRegistration(
	ctx context.Context,
	value registration.Registration,
	expectedVersion int64,
) (registration.Registration, error) {
	return tx.registrations.UpdateParticipation(ctx, value, expectedVersion)
}

func (tx *cancellationSQLTransaction) updateOrder(
	ctx context.Context,
	value payment.Order,
	expectedVersion int64,
) (payment.Order, error) {
	return tx.payments.UpdateOrderPayment(ctx, value, expectedVersion)
}

func (tx *cancellationSQLTransaction) updateHold(
	ctx context.Context,
	value payment.CapacityHold,
	expectedVersion int64,
) (payment.CapacityHold, error) {
	return tx.payments.UpdateCapacityHold(ctx, value, expectedVersion)
}

func (tx *cancellationSQLTransaction) releaseCouponForOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
	reason string,
	at time.Time,
) error {
	_, err := tx.coupons.ReleaseOrderHold(ctx, tenantID, orderID, reason, at)
	if err != nil {
		return fmt.Errorf("%w: release Coupon hold: %v", ErrRegistrationCancellationTransaction, err)
	}
	return nil
}

func (tx *cancellationSQLTransaction) appendCouponEntry(
	ctx context.Context,
	value coupon.Entry,
) (coupon.Entry, error) {
	return tx.coupons.AppendLifecycleEntry(ctx, value)
}

func (tx *cancellationSQLTransaction) createRefund(
	ctx context.Context,
	value refund.Case,
) (refund.Case, error) {
	return tx.refunds.Create(ctx, value)
}

func (tx *cancellationSQLTransaction) decrementConfirmedCapacity(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
	expectedVersion int64,
	updatedAt time.Time,
) error {
	return tx.decrementCapacity(
		ctx,
		"confirmed_registration_count",
		tenantID,
		instanceID,
		sessionID,
		expectedVersion,
		updatedAt,
	)
}

func (tx *cancellationSQLTransaction) decrementHoldCapacity(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
	expectedVersion int64,
	updatedAt time.Time,
) error {
	return tx.decrementCapacity(
		ctx,
		"active_hold_count",
		tenantID,
		instanceID,
		sessionID,
		expectedVersion,
		updatedAt,
	)
}

func (tx *cancellationSQLTransaction) decrementCapacity(
	ctx context.Context,
	column string,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
	expectedVersion int64,
	updatedAt time.Time,
) error {
	const statement = `
UPDATE xiangwan_activity_sessions
SET %[1]s = %[1]s - 1,
    version = version + 1,
    updated_at = $4
WHERE tenant_id = $1
  AND instance_id = $2
  AND id = $3
  AND version = $5
  AND %[1]s > 0
RETURNING version
`
	var query string
	switch column {
	case "confirmed_registration_count":
		query = fmt.Sprintf(statement, "confirmed_registration_count")
	case "active_hold_count":
		query = fmt.Sprintf(statement, "active_hold_count")
	default:
		return ErrRegistrationCancellationTransaction
	}
	var newVersion int64
	err := tx.tx.QueryRowContext(
		ctx,
		query,
		tenantID,
		instanceID,
		sessionID,
		updatedAt,
		expectedVersion,
	).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRegistrationCancellationTransaction
	}
	if err != nil {
		return fmt.Errorf("decrement cancellation Session capacity: %w", err)
	}
	return nil
}

func (tx *cancellationSQLTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *cancellationSQLTransaction) Rollback() error {
	return tx.tx.Rollback()
}

func cancellationLockError(target string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRegistrationCancellationTransaction
	}
	if err != nil {
		return fmt.Errorf("lock cancellation %s: %w", target, err)
	}
	return nil
}

func classifyCancellationWriteError(err error) error {
	if errors.Is(err, ErrRegistrationVersionConflict) ||
		errors.Is(err, paymentpostgres.ErrOrderVersionConflict) ||
		errors.Is(err, paymentpostgres.ErrCapacityHoldVersionConflict) ||
		errors.Is(err, refundpostgres.ErrRefundCaseVersionConflict) ||
		errors.Is(err, coupon.ErrInvalidEntry) ||
		errors.Is(err, coupon.ErrInvalidLedger) {
		return ErrRegistrationCancellationTransaction
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return ErrRegistrationCancellationConflict
		case "23503", "23514", "40001":
			return fmt.Errorf("%w: %v", ErrRegistrationCancellationTransaction, err)
		}
	}
	return err
}

func classifyCancellationCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23503", "23514", "40001":
			return fmt.Errorf("%w: %v", ErrRegistrationCancellationTransaction, err)
		}
	}
	return fmt.Errorf("commit xiangwan Registration cancellation transaction: %w", err)
}
