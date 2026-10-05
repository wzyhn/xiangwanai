package paymentpostgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidPaidRegistrationCommand        = errors.New("invalid xiangwan paid Registration command")
	ErrPaidRegistrationUnavailable           = errors.New("xiangwan Session is unavailable for paid Registration")
	ErrPaidRegistrationFreeSession           = errors.New("xiangwan Session does not require payment")
	ErrPaidRegistrationAlreadyOpen           = errors.New("xiangwan principal already has an open Registration")
	ErrPaidRegistrationIdempotencyConflict   = errors.New("xiangwan paid Registration idempotency conflict")
	ErrPaidRegistrationCapacityConflict      = errors.New("xiangwan Session capacity changed")
	ErrPaidRegistrationTransactionConflict   = errors.New("xiangwan paid Registration transaction conflict")
	ErrPaidRegistrationQuestionnaireConflict = errors.New(
		"xiangwan paid Registration questionnaire changed",
	)
	ErrPaidRegistrationContactPolicyConflict = errors.New(
		"xiangwan paid Registration contact policy changed",
	)
	ErrPaidRegistrationPrivacyPolicyConflict = errors.New(
		"xiangwan paid Registration privacy policy changed",
	)
	ErrPaidRegistrationGenerationInactive = errors.New(
		"xiangwan paid Registration generation is inactive",
	)
	ErrPaidRegistrationAnswersInvalid = errors.New(
		"xiangwan paid Registration questionnaire answers are invalid",
	)
)

var errPaidRegistrationNotFound = errors.New("xiangwan paid Registration not found")

type StartPaidRegistrationCommand struct {
	TenantID                   uuid.UUID
	SeriesID                   uuid.UUID
	InstanceID                 uuid.UUID
	SessionID                  uuid.UUID
	PrincipalID                uuid.UUID
	IdempotencyKey             string
	MerchantOrderNo            string
	PaymentAppID               string
	PaymentMerchantID          string
	MerchantConfigGenerationID uuid.UUID
	CouponID                   *uuid.UUID
	Submission                 *registration.RegistrationSubmission
	PrivacyPolicyVersion       string
	ManualContactEnabled       bool
	ContactPolicyVersion       string
}

// ReplayPaidRegistrationCommand contains the immutable request identity needed
// to recover a committed paid-registration context. Current payment or Session
// configuration is intentionally not part of replay eligibility.
type ReplayPaidRegistrationCommand struct {
	TenantID       uuid.UUID
	SessionID      uuid.UUID
	PrincipalID    uuid.UUID
	IdempotencyKey string
	CouponID       *uuid.UUID
	Submission     registration.RegistrationSubmission
}

type PaidRegistrationContext struct {
	Registration registration.Registration
	Payment      payment.PaymentContext
	Coupon       *AppliedCoupon
}

// PaidRegistrar starts one paid Registration by reserving Session capacity and
// persisting Registration, Order, and hold facts in one serializable PostgreSQL
// transaction. The Session row is the concurrency lock; Redis is unused.
type PaidRegistrar struct {
	transactions paidRegistrationTransactionStarter
	now          func() time.Time
	generationID uuid.UUID
}

func NewPaidRegistrar(db *sql.DB, generationID uuid.UUID) *PaidRegistrar {
	return &PaidRegistrar{
		transactions: sqlPaidRegistrationTransactionStarter{db: db},
		now:          time.Now,
		generationID: generationID,
	}
}

// Replay recovers an exact committed Registration, Order, and capacity hold
// before callers consult mutable Session facts.
func (registrar *PaidRegistrar) Replay(
	ctx context.Context,
	command ReplayPaidRegistrationCommand,
) (PaidRegistrationContext, bool, error) {
	if registrar == nil || registrar.transactions == nil || ctx == nil ||
		command.TenantID == uuid.Nil || command.SessionID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		!registration.ValidIdempotencyKey(command.IdempotencyKey) {
		return PaidRegistrationContext{}, false,
			ErrInvalidPaidRegistrationCommand
	}
	if command.CouponID != nil && *command.CouponID == uuid.Nil {
		return PaidRegistrationContext{}, false, ErrInvalidPaidRegistrationCommand
	}
	prepared, err := registration.PrepareRegistrationSubmission(
		command.SessionID,
		command.Submission,
	)
	if err != nil {
		return PaidRegistrationContext{}, false, fmt.Errorf(
			"%w: %v",
			ErrInvalidPaidRegistrationCommand,
			err,
		)
	}
	tx, err := registrar.transactions.beginTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true},
	)
	if err != nil {
		return PaidRegistrationContext{}, false, fmt.Errorf(
			"begin xiangwan paid Registration replay transaction: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	existing, err := getPaidRegistrationByIdempotencyKey(
		ctx,
		tx,
		command.TenantID,
		command.IdempotencyKey,
	)
	if errors.Is(err, errPaidRegistrationNotFound) {
		_, orderErr := (&Repository{db: tx}).GetOrderByIdempotencyKey(
			ctx,
			command.TenantID,
			command.IdempotencyKey,
		)
		switch {
		case orderErr == nil:
			return PaidRegistrationContext{}, false,
				ErrPaidRegistrationIdempotencyConflict
		case !errors.Is(orderErr, ErrOrderNotFound):
			return PaidRegistrationContext{}, false, orderErr
		}
		if err := tx.Commit(); err != nil {
			return PaidRegistrationContext{}, false,
				classifyPaidRegistrationCommitError(err)
		}
		committed = true
		return PaidRegistrationContext{}, false, nil
	}
	if err != nil {
		return PaidRegistrationContext{}, false, err
	}
	if existing.SessionID != command.SessionID ||
		existing.PrincipalID != command.PrincipalID ||
		existing.IdempotencyKey != command.IdempotencyKey ||
		existing.SeriesID == uuid.Nil || existing.InstanceID == uuid.Nil {
		return PaidRegistrationContext{}, false,
			ErrPaidRegistrationIdempotencyConflict
	}
	fingerprint, fingerprintErr := getPaidRegistrationSnapshotFingerprint(
		ctx,
		tx,
		command.TenantID,
		existing.ID,
	)
	if fingerprintErr != nil || fingerprint != prepared.RequestFingerprint {
		return PaidRegistrationContext{}, false,
			ErrPaidRegistrationIdempotencyConflict
	}
	replayed, err := loadPaidRegistrationContext(
		ctx,
		&Repository{db: tx},
		tx.couponOrderRepository(),
		existing,
	)
	if err != nil {
		return PaidRegistrationContext{}, false, err
	}
	if !paidRegistrationCouponMatches(replayed, command.CouponID) ||
		replayed.Payment.Order.IdempotencyKey != command.IdempotencyKey {
		return PaidRegistrationContext{}, false,
			ErrPaidRegistrationIdempotencyConflict
	}
	if err := tx.Commit(); err != nil {
		return PaidRegistrationContext{}, false,
			classifyPaidRegistrationCommitError(err)
	}
	committed = true
	return replayed, true, nil
}

func (registrar *PaidRegistrar) Start(
	ctx context.Context,
	command StartPaidRegistrationCommand,
) (PaidRegistrationContext, error) {
	if registrar == nil || registrar.transactions == nil || registrar.now == nil ||
		registrar.generationID == uuid.Nil {
		return PaidRegistrationContext{}, ErrInvalidPaidRegistrationCommand
	}
	var preparedSubmission *registration.PreparedRegistrationSubmission
	if command.Submission != nil {
		prepared, prepareErr := registration.PrepareRegistrationSubmission(
			command.SessionID,
			*command.Submission,
		)
		if prepareErr != nil {
			return PaidRegistrationContext{}, fmt.Errorf(
				"%w: %v",
				ErrInvalidPaidRegistrationCommand,
				prepareErr,
			)
		}
		preparedSubmission = &prepared
	}
	validationNow := registrar.now().UTC()
	_, err := registration.NewRegistration(registration.NewRegistrationCommand{
		TenantID:        command.TenantID,
		SeriesID:        command.SeriesID,
		InstanceID:      command.InstanceID,
		SessionID:       command.SessionID,
		PrincipalID:     command.PrincipalID,
		IdempotencyKey:  command.IdempotencyKey,
		RequiresPayment: true,
		Now:             validationNow,
	})
	if err != nil || (command.CouponID != nil && *command.CouponID == uuid.Nil) {
		return PaidRegistrationContext{}, fmt.Errorf(
			"%w: Registration identity or discount is invalid",
			ErrInvalidPaidRegistrationCommand,
		)
	}

	tx, err := registrar.transactions.beginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return PaidRegistrationContext{}, fmt.Errorf("begin xiangwan paid Registration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	paymentRepository := &Repository{db: tx}
	existingRegistration, err := getPaidRegistrationByIdempotencyKey(
		ctx,
		tx,
		command.TenantID,
		command.IdempotencyKey,
	)
	switch {
	case err == nil:
		if !paidRegistrationMatchesCommand(existingRegistration, command) {
			return PaidRegistrationContext{}, ErrPaidRegistrationIdempotencyConflict
		}
		existingContext, replayErr := loadPaidRegistrationContext(
			ctx,
			paymentRepository,
			tx.couponOrderRepository(),
			existingRegistration,
		)
		if replayErr != nil {
			return PaidRegistrationContext{}, replayErr
		}
		if !paidRegistrationContextMatchesCommand(existingContext, command) {
			return PaidRegistrationContext{}, ErrPaidRegistrationIdempotencyConflict
		}
		if preparedSubmission != nil {
			fingerprint, fingerprintErr :=
				getPaidRegistrationSnapshotFingerprint(
					ctx,
					tx,
					command.TenantID,
					existingRegistration.ID,
				)
			if fingerprintErr != nil ||
				fingerprint != preparedSubmission.RequestFingerprint {
				return PaidRegistrationContext{},
					ErrPaidRegistrationIdempotencyConflict
			}
		}
		if err := tx.Commit(); err != nil {
			return PaidRegistrationContext{}, classifyPaidRegistrationCommitError(err)
		}
		committed = true
		return existingContext, nil
	case !errors.Is(err, errPaidRegistrationNotFound):
		return PaidRegistrationContext{}, err
	}

	_, err = paymentRepository.GetOrderByIdempotencyKey(
		ctx,
		command.TenantID,
		command.IdempotencyKey,
	)
	switch {
	case err == nil:
		return PaidRegistrationContext{}, ErrPaidRegistrationIdempotencyConflict
	case !errors.Is(err, ErrOrderNotFound):
		return PaidRegistrationContext{}, err
	}
	if err := tx.lockActiveGeneration(
		ctx,
		command.TenantID,
		registrar.generationID,
	); err != nil {
		return PaidRegistrationContext{}, err
	}
	if preparedSubmission != nil {
		if preparedSubmission.PrivacyPolicyVersion != "" &&
			preparedSubmission.PrivacyPolicyVersion !=
				command.PrivacyPolicyVersion {
			return PaidRegistrationContext{}, ErrPaidRegistrationPrivacyPolicyConflict
		}
		if !command.ManualContactEnabled ||
			preparedSubmission.Contact.PolicyVersion != command.ContactPolicyVersion {
			return PaidRegistrationContext{}, ErrPaidRegistrationContactPolicyConflict
		}
		if err := requireActivePaidRegistrationBrand(
			ctx,
			tx,
			command.TenantID,
		); err != nil {
			return PaidRegistrationContext{}, err
		}
	}

	series, err := lockPaidRegistrationSeries(
		ctx,
		tx,
		command.TenantID,
		command.SeriesID,
	)
	if err != nil {
		return PaidRegistrationContext{}, err
	}
	if series.status != activity.SeriesStatusActive {
		return PaidRegistrationContext{}, ErrPaidRegistrationUnavailable
	}

	instance, err := lockPaidRegistrationInstance(
		ctx,
		tx,
		command.TenantID,
		command.SeriesID,
		command.InstanceID,
	)
	if err != nil {
		return PaidRegistrationContext{}, err
	}
	if instance.status != activity.InstanceStatusPublished {
		return PaidRegistrationContext{}, ErrPaidRegistrationUnavailable
	}
	instancePublicationVersion := int64(0)
	if preparedSubmission != nil {
		instancePublicationVersion, err =
			getCurrentPaidRegistrationInstancePublicationVersion(
				ctx,
				tx,
				command.TenantID,
				command.SeriesID,
				command.InstanceID,
			)
		if err != nil {
			return PaidRegistrationContext{}, err
		}
		if instancePublicationVersion !=
			preparedSubmission.InstancePublicationVersion {
			return PaidRegistrationContext{},
				ErrPaidRegistrationTransactionConflict
		}
	}

	session, err := lockPaidRegistrationSession(
		ctx,
		tx,
		command.TenantID,
		command.InstanceID,
		command.SessionID,
	)
	if err != nil {
		return PaidRegistrationContext{}, err
	}
	if session.status != activity.SessionStatusPublished {
		return PaidRegistrationContext{}, ErrPaidRegistrationUnavailable
	}

	hasOpenRegistration, err := paidRegistrationIsOpen(
		ctx,
		tx,
		command.TenantID,
		command.PrincipalID,
		command.SessionID,
	)
	if err != nil {
		return PaidRegistrationContext{}, err
	}
	if hasOpenRegistration {
		return PaidRegistrationContext{}, ErrPaidRegistrationAlreadyOpen
	}

	if session.priceCents == nil {
		return PaidRegistrationContext{}, fmt.Errorf(
			"%w: price is missing",
			ErrPaidRegistrationUnavailable,
		)
	}
	if preparedSubmission != nil &&
		*session.priceCents != preparedSubmission.PriceCents {
		return PaidRegistrationContext{},
			ErrPaidRegistrationTransactionConflict
	}
	now := registrar.now().UTC()
	display, err := activity.DecideSessionDisplay(activity.SessionDisplayFacts{
		Now:                        now,
		RegistrationStartAt:        session.registrationStartAt,
		RegistrationEndAt:          session.registrationEndAt,
		SessionStartAt:             session.sessionStartAt,
		SessionEndAt:               session.sessionEndAt,
		Capacity:                   session.capacity,
		ConfirmedRegistrationCount: session.confirmedCount,
		ActiveHoldCount:            session.activeHoldCount,
		GroupMinimum:               session.groupMinimum,
		LowStockThreshold:          session.lowStockThreshold,
	})
	if err != nil {
		return PaidRegistrationContext{}, fmt.Errorf("%w: %v", ErrPaidRegistrationUnavailable, err)
	}
	if !display.State.RegistrationAllowed() {
		return PaidRegistrationContext{}, fmt.Errorf(
			"%w: display state %q",
			ErrPaidRegistrationUnavailable,
			display.State,
		)
	}
	if *session.priceCents == 0 {
		return PaidRegistrationContext{}, ErrPaidRegistrationFreeSession
	}
	var questionnaire *activity.SessionQuestionnaire
	var normalizedAnswers []activity.QuestionnaireAnswer
	if preparedSubmission != nil {
		questionnaire, normalizedAnswers, err =
			validateCurrentPaidRegistrationQuestionnaire(
				ctx,
				tx,
				command.TenantID,
				command.InstanceID,
				command.SessionID,
				*preparedSubmission,
			)
		if err != nil {
			return PaidRegistrationContext{}, err
		}
	}

	couponRepository := tx.couponOrderRepository()
	var selectedLedger *couponpostgres.Ledger
	discountCents := int64(0)
	if command.CouponID != nil {
		selectedLedger, err = lockSelectedCoupon(
			ctx,
			couponRepository,
			command.TenantID,
			*command.CouponID,
			command.PrincipalID,
		)
		if err != nil {
			return PaidRegistrationContext{}, err
		}
		discountCents = selectedLedger.Instrument.FaceValueCents
		if discountCents > *session.priceCents {
			return PaidRegistrationContext{}, ErrCouponPaymentUnavailable
		}
	}

	registrationCandidate, err := registration.NewRegistration(registration.NewRegistrationCommand{
		TenantID:        command.TenantID,
		SeriesID:        command.SeriesID,
		InstanceID:      command.InstanceID,
		SessionID:       command.SessionID,
		PrincipalID:     command.PrincipalID,
		IdempotencyKey:  command.IdempotencyKey,
		RequiresPayment: true,
		Now:             now,
	})
	if err != nil {
		return PaidRegistrationContext{}, fmt.Errorf("%w: %v", ErrInvalidPaidRegistrationCommand, err)
	}
	paymentCandidate, err := payment.NewPaymentContext(payment.NewPaymentContextCommand{
		TenantID:                   command.TenantID,
		RegistrationID:             registrationCandidate.ID,
		SeriesID:                   command.SeriesID,
		InstanceID:                 command.InstanceID,
		SessionID:                  command.SessionID,
		PrincipalID:                command.PrincipalID,
		IdempotencyKey:             command.IdempotencyKey,
		MerchantOrderNo:            command.MerchantOrderNo,
		PaymentAppID:               command.PaymentAppID,
		PaymentMerchantID:          command.PaymentMerchantID,
		MerchantConfigGenerationID: command.MerchantConfigGenerationID,
		OriginalPriceCents:         *session.priceCents,
		DiscountCents:              discountCents,
		Now:                        now,
	})
	if err != nil {
		return PaidRegistrationContext{}, fmt.Errorf("%w: %v", ErrInvalidPaidRegistrationCommand, err)
	}

	createdRegistration, err := createPendingPaidRegistration(ctx, tx, registrationCandidate)
	if err != nil {
		return PaidRegistrationContext{}, classifyPaidRegistrationWriteError(err)
	}
	createdOrder, err := paymentRepository.CreateOrder(ctx, paymentCandidate.Order)
	if err != nil {
		return PaidRegistrationContext{}, classifyPaidRegistrationWriteError(err)
	}
	createdHold, err := paymentRepository.CreateCapacityHold(ctx, paymentCandidate.Hold)
	if err != nil {
		return PaidRegistrationContext{}, classifyPaidRegistrationWriteError(err)
	}
	if preparedSubmission != nil {
		if err := createPaidRegistrationSubmissionSnapshot(
			ctx,
			tx,
			createdRegistration,
			instancePublicationVersion,
			session,
			*preparedSubmission,
			questionnaire,
			normalizedAnswers,
			now,
		); err != nil {
			return PaidRegistrationContext{}, err
		}
	}

	var couponApplication *AppliedCoupon
	if selectedLedger != nil {
		heldEntry, holdErr := appendOrderCouponHold(
			ctx,
			couponRepository,
			selectedLedger,
			createdOrder.ID,
			createdRegistration.ID,
			createdOrder.SeriesID,
			instance.activityType,
			createdOrder.OriginalPriceCents,
			createdHold.ExpiresAt,
			now,
		)
		if holdErr != nil {
			return PaidRegistrationContext{}, holdErr
		}
		couponApplication = appliedCoupon(selectedLedger, &heldEntry)
	}

	if createdOrder.PayableCents > 0 {
		if err := incrementPaidRegistrationHold(
			ctx,
			tx,
			command.TenantID,
			command.InstanceID,
			command.SessionID,
			session.version,
			now,
		); err != nil {
			return PaidRegistrationContext{}, err
		}
	} else {
		if selectedLedger == nil {
			return PaidRegistrationContext{}, ErrCouponPaymentConflict
		}
		createdRegistration, createdOrder, createdHold, couponApplication, err =
			settleZeroPaidRegistration(
				ctx,
				tx,
				paymentRepository,
				couponRepository,
				selectedLedger,
				createdRegistration,
				createdOrder,
				createdHold,
				series,
				session,
				now,
			)
		if err != nil {
			return PaidRegistrationContext{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return PaidRegistrationContext{}, classifyPaidRegistrationCommitError(err)
	}
	committed = true
	return PaidRegistrationContext{
		Registration: createdRegistration,
		Payment: payment.PaymentContext{
			Order: createdOrder,
			Hold:  createdHold,
		},
		Coupon: couponApplication,
	}, nil
}

func settleZeroPaidRegistration(
	ctx context.Context,
	tx paidRegistrationTransaction,
	paymentRepository *Repository,
	couponRepository couponOrderRepository,
	ledger *couponpostgres.Ledger,
	currentRegistration registration.Registration,
	currentOrder payment.Order,
	currentHold payment.CapacityHold,
	series lockedPaidRegistrationSeries,
	session lockedPaidRegistrationSession,
	now time.Time,
) (
	registration.Registration,
	payment.Order,
	payment.CapacityHold,
	*AppliedCoupon,
	error,
) {
	settledOrder, changed, err := payment.SettleOrderZero(currentOrder, now)
	if err != nil || !changed {
		return registration.Registration{}, payment.Order{}, payment.CapacityHold{}, nil,
			fmt.Errorf("%w: settle zero Order: %v", ErrPaidRegistrationTransactionConflict, err)
	}
	settledOrder, err = paymentRepository.UpdateOrderPayment(
		ctx,
		settledOrder,
		currentOrder.Version,
	)
	if err != nil {
		return registration.Registration{}, payment.Order{}, payment.CapacityHold{}, nil,
			classifyPaidRegistrationWriteError(err)
	}
	convertedHold, changed, err := payment.ConvertCapacityHold(currentHold, now)
	if err != nil || !changed {
		return registration.Registration{}, payment.Order{}, payment.CapacityHold{}, nil,
			fmt.Errorf("%w: convert zero settlement hold: %v", ErrPaidRegistrationTransactionConflict, err)
	}
	convertedHold, err = paymentRepository.UpdateCapacityHold(
		ctx,
		convertedHold,
		currentHold.Version,
	)
	if err != nil {
		return registration.Registration{}, payment.Order{}, payment.CapacityHold{}, nil,
			classifyPaidRegistrationWriteError(err)
	}
	confirmedRegistration, changed, err := registration.ConfirmRegistration(
		currentRegistration,
		now,
	)
	if err != nil || !changed {
		return registration.Registration{}, payment.Order{}, payment.CapacityHold{}, nil,
			fmt.Errorf("%w: confirm zero settlement Registration: %v", ErrPaidRegistrationTransactionConflict, err)
	}
	confirmedRegistration, err = updatePaymentConfirmationRegistration(
		ctx,
		tx,
		confirmedRegistration,
		currentRegistration.Version,
	)
	if err != nil {
		return registration.Registration{}, payment.Order{}, payment.CapacityHold{}, nil, err
	}
	if err := confirmZeroSettlementCapacity(
		ctx,
		tx,
		currentOrder.TenantID,
		currentOrder.InstanceID,
		currentOrder.SessionID,
		session.version,
		now,
	); err != nil {
		return registration.Registration{}, payment.Order{}, payment.CapacityHold{}, nil, err
	}
	if err := incrementPaymentConfirmationHistory(
		ctx,
		tx,
		currentOrder.TenantID,
		currentOrder.SeriesID,
		series.version,
		now,
	); err != nil {
		return registration.Registration{}, payment.Order{}, payment.CapacityHold{}, nil, err
	}
	redeemedEntry, err := redeemOrderCoupon(
		ctx,
		couponRepository,
		ledger,
		currentOrder.ID,
		now,
	)
	if err != nil {
		return registration.Registration{}, payment.Order{}, payment.CapacityHold{}, nil, err
	}
	return confirmedRegistration, settledOrder, convertedHold,
		appliedCoupon(ledger, redeemedEntry), nil
}

func confirmZeroSettlementCapacity(
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
SET confirmed_registration_count = confirmed_registration_count + 1,
    version = version + 1,
    updated_at = $4
WHERE tenant_id = $1
  AND instance_id = $2
  AND id = $3
  AND status = 'published'
  AND version = $5
  AND confirmed_registration_count + active_hold_count < capacity
RETURNING version
`, tenantID, instanceID, sessionID, now, expectedVersion).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPaidRegistrationCapacityConflict
	}
	if err != nil {
		return fmt.Errorf("confirm zero settlement capacity: %w", err)
	}
	return nil
}

func loadPaidRegistrationContext(
	ctx context.Context,
	repository *Repository,
	couponRepository couponOrderRepository,
	existingRegistration registration.Registration,
) (PaidRegistrationContext, error) {
	order, err := repository.GetOrderByRegistration(
		ctx,
		existingRegistration.TenantID,
		existingRegistration.ID,
	)
	if errors.Is(err, ErrOrderNotFound) {
		return PaidRegistrationContext{}, fmt.Errorf(
			"%w: Registration has no Order",
			ErrPaidRegistrationTransactionConflict,
		)
	}
	if err != nil {
		return PaidRegistrationContext{}, err
	}
	hold, err := repository.GetCapacityHoldByOrder(
		ctx,
		existingRegistration.TenantID,
		order.ID,
	)
	if errors.Is(err, ErrCapacityHoldNotFound) {
		return PaidRegistrationContext{}, fmt.Errorf(
			"%w: Order has no capacity hold",
			ErrPaidRegistrationTransactionConflict,
		)
	}
	if err != nil {
		return PaidRegistrationContext{}, err
	}
	if !paymentContextMatchesRegistration(order, hold, existingRegistration) {
		return PaidRegistrationContext{}, fmt.Errorf(
			"%w: payment context identity mismatch",
			ErrPaidRegistrationTransactionConflict,
		)
	}
	var couponApplication *AppliedCoupon
	var ledger *couponpostgres.Ledger
	if order.DiscountCents > 0 {
		ledger, err = lockOrderCoupon(
			ctx,
			couponRepository,
			order.TenantID,
			order.ID,
		)
		if err != nil {
			return PaidRegistrationContext{}, err
		}
	}
	if ledger != nil {
		entry := latestOrderCouponEntry(ledger.Entries, order.ID)
		if entry == nil {
			return PaidRegistrationContext{}, ErrPaidRegistrationTransactionConflict
		}
		couponApplication = appliedCoupon(ledger, entry)
	}
	return PaidRegistrationContext{
		Registration: existingRegistration,
		Payment: payment.PaymentContext{
			Order: order,
			Hold:  hold,
		},
		Coupon: couponApplication,
	}, nil
}

func createPendingPaidRegistration(
	ctx context.Context,
	tx paidRegistrationTransaction,
	value registration.Registration,
) (registration.Registration, error) {
	return scanPaidRegistration(tx.queryRowContext(ctx, `
INSERT INTO xiangwan_registrations (
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8,
    $9, $10, $11,
    $12, $13, $14
)
RETURNING
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
`,
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.ParticipationStatus,
		value.IdempotencyKey,
		value.ConfirmedAt,
		value.CancelledAt,
		value.CancellationReason,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
}

func getPaidRegistrationByIdempotencyKey(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	idempotencyKey string,
) (registration.Registration, error) {
	value, err := scanPaidRegistration(tx.queryRowContext(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
FROM xiangwan_registrations
WHERE tenant_id = $1 AND idempotency_key = $2
`, tenantID, idempotencyKey))
	if errors.Is(err, sql.ErrNoRows) {
		return registration.Registration{}, errPaidRegistrationNotFound
	}
	if err != nil {
		return registration.Registration{}, fmt.Errorf("get paid Registration by idempotency key: %w", err)
	}
	return value, nil
}

func paidRegistrationIsOpen(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	sessionID uuid.UUID,
) (bool, error) {
	var existingID uuid.UUID
	err := tx.queryRowContext(ctx, `
SELECT id
FROM xiangwan_registrations
WHERE tenant_id = $1
  AND principal_id = $2
  AND session_id = $3
  AND participation_status IN ('pending_payment', 'confirmed')
`, tenantID, principalID, sessionID).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("find open paid Registration: %w", err)
	}
	return true, nil
}

func requireActivePaidRegistrationBrand(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
) error {
	var status activity.BrandLifecycleStatus
	err := tx.queryRowContext(ctx, `
SELECT lifecycle_status
FROM xiangwan_brand_profiles
WHERE tenant_id = $1
FOR SHARE
`, tenantID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && status != activity.BrandLifecycleActive) {
		return ErrPaidRegistrationUnavailable
	}
	if err != nil {
		return fmt.Errorf("lock paid Registration BrandProfile: %w", err)
	}
	return nil
}

func getCurrentPaidRegistrationInstancePublicationVersion(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (int64, error) {
	var publicationVersion int64
	err := tx.queryRowContext(ctx, `
SELECT activity_instance.publication_version
FROM xiangwan_activity_series AS activity_series
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = activity_series.tenant_id
 AND activity_instance.series_id = activity_series.id
 AND activity_instance.id = activity_series.current_public_instance_id
WHERE activity_series.tenant_id = $1
  AND activity_series.id = $2
  AND activity_instance.id = $3
  AND activity_instance.status = 'published'
`, tenantID, seriesID, instanceID).Scan(&publicationVersion)
	if errors.Is(err, sql.ErrNoRows) ||
		(err == nil && publicationVersion < 1) {
		return 0, ErrPaidRegistrationUnavailable
	}
	if err != nil {
		return 0, fmt.Errorf(
			"read paid Registration Instance publication: %w",
			err,
		)
	}
	return publicationVersion, nil
}

func getPaidRegistrationSnapshotFingerprint(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (string, error) {
	var fingerprint string
	err := tx.queryRowContext(ctx, `
SELECT request_fingerprint
FROM xiangwan_registration_snapshots
WHERE tenant_id = $1 AND registration_id = $2
`, tenantID, registrationID).Scan(&fingerprint)
	if err != nil {
		return "", fmt.Errorf(
			"read paid Registration submission receipt: %w",
			err,
		)
	}
	return fingerprint, nil
}

type paidRegistrationQuestionnaireFieldJSON struct {
	FieldID       uuid.UUID                       `json:"field_id"`
	Code          string                          `json:"code"`
	Type          activity.QuestionnaireFieldType `json:"type"`
	Label         string                          `json:"label"`
	HelpText      string                          `json:"help_text"`
	Required      bool                            `json:"required"`
	SortOrder     int                             `json:"sort_order"`
	MinLength     *int                            `json:"min_length"`
	MaxLength     *int                            `json:"max_length"`
	MaxSelections *int                            `json:"max_selections"`
	Options       []activity.QuestionnaireOption  `json:"options"`
}

func loadCurrentPaidRegistrationQuestionnaire(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (activity.SessionQuestionnaire, error) {
	var questionnaire activity.SessionQuestionnaire
	var fieldsJSON []byte
	err := tx.queryRowContext(ctx, `
SELECT
    questionnaire.questionnaire_version_id,
    questionnaire.version,
    questionnaire.privacy_purpose,
    questionnaire.privacy_policy_version,
    questionnaire.published_at,
    JSONB_AGG(
        JSONB_BUILD_OBJECT(
            'field_id', field.field_id,
            'code', field.field_code,
            'type', field.field_type,
            'label', field.label,
            'help_text', field.help_text,
            'required', field.is_required,
            'sort_order', field.sort_order,
            'min_length', field.min_length,
            'max_length', field.max_length,
            'max_selections', field.max_selections,
            'options', field.options
        ) ORDER BY field.sort_order, field.field_id
    )
FROM LATERAL (
    SELECT assignment.questionnaire_version_id
    FROM xiangwan_instance_questionnaires AS assignment
    WHERE assignment.tenant_id = $1
      AND assignment.instance_id = $2
    ORDER BY assignment.assignment_version DESC
    LIMIT 1
) AS current_assignment
JOIN xiangwan_questionnaire_versions AS questionnaire
  ON questionnaire.tenant_id = $1
 AND questionnaire.instance_id = $2
 AND questionnaire.questionnaire_version_id =
     current_assignment.questionnaire_version_id
 AND questionnaire.status = 'published'
JOIN xiangwan_questionnaire_fields AS field
  ON field.tenant_id = questionnaire.tenant_id
 AND field.instance_id = questionnaire.instance_id
 AND field.questionnaire_version_id = questionnaire.questionnaire_version_id
GROUP BY
    questionnaire.questionnaire_version_id,
    questionnaire.version,
    questionnaire.privacy_purpose,
    questionnaire.privacy_policy_version,
    questionnaire.published_at
`, tenantID, instanceID).Scan(
		&questionnaire.QuestionnaireVersionID,
		&questionnaire.Version,
		&questionnaire.PrivacyPurpose,
		&questionnaire.PrivacyPolicyVersion,
		&questionnaire.PublishedAt,
		&fieldsJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.SessionQuestionnaire{},
			activity.ErrQuestionnaireUnavailable
	}
	if err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"read current paid Registration questionnaire: %w",
			err,
		)
	}
	var fields []paidRegistrationQuestionnaireFieldJSON
	if err := json.Unmarshal(fieldsJSON, &fields); err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"decode current paid Registration questionnaire: %w",
			err,
		)
	}
	questionnaire.InstanceID = instanceID
	questionnaire.SessionID = sessionID
	questionnaire.Fields = make([]activity.QuestionnaireField, 0, len(fields))
	for _, field := range fields {
		questionnaire.Fields = append(
			questionnaire.Fields,
			activity.QuestionnaireField{
				FieldID:       field.FieldID,
				Code:          field.Code,
				Type:          field.Type,
				Label:         field.Label,
				HelpText:      field.HelpText,
				Required:      field.Required,
				SortOrder:     field.SortOrder,
				MinLength:     field.MinLength,
				MaxLength:     field.MaxLength,
				MaxSelections: field.MaxSelections,
				Options:       field.Options,
			},
		)
	}
	if err := activity.ValidateSessionQuestionnaire(questionnaire); err != nil {
		return activity.SessionQuestionnaire{}, fmt.Errorf(
			"validate current paid Registration questionnaire: %w",
			err,
		)
	}
	return questionnaire, nil
}

func validateCurrentPaidRegistrationQuestionnaire(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
	submission registration.PreparedRegistrationSubmission,
) (
	*activity.SessionQuestionnaire,
	[]activity.QuestionnaireAnswer,
	error,
) {
	questionnaire, err := loadCurrentPaidRegistrationQuestionnaire(
		ctx,
		tx,
		tenantID,
		instanceID,
		sessionID,
	)
	if errors.Is(err, activity.ErrQuestionnaireUnavailable) {
		if submission.QuestionnaireVersionID != nil || len(submission.Answers) != 0 {
			return nil, nil, ErrPaidRegistrationQuestionnaireConflict
		}
		return nil, []activity.QuestionnaireAnswer{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if submission.QuestionnaireVersionID == nil ||
		*submission.QuestionnaireVersionID !=
			questionnaire.QuestionnaireVersionID {
		return nil, nil, ErrPaidRegistrationQuestionnaireConflict
	}
	answers, err := activity.NormalizeQuestionnaireAnswers(
		questionnaire,
		submission.Answers,
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"%w: %v",
			ErrPaidRegistrationAnswersInvalid,
			err,
		)
	}
	return &questionnaire, answers, nil
}

func createPaidRegistrationSubmissionSnapshot(
	ctx context.Context,
	tx paidRegistrationTransaction,
	created registration.Registration,
	instancePublicationVersion int64,
	session lockedPaidRegistrationSession,
	submission registration.PreparedRegistrationSubmission,
	questionnaire *activity.SessionQuestionnaire,
	answers []activity.QuestionnaireAnswer,
	now time.Time,
) error {
	var questionnaireVersionID any
	var questionnaireVersion any
	var privacyPurpose any
	var privacyPolicyVersion any
	var acknowledgedPrivacyPolicyVersion any
	if submission.PrivacyPolicyVersion != "" {
		acknowledgedPrivacyPolicyVersion = submission.PrivacyPolicyVersion
	}
	if questionnaire != nil {
		questionnaireVersionID = questionnaire.QuestionnaireVersionID
		questionnaireVersion = questionnaire.Version
		privacyPurpose = questionnaire.PrivacyPurpose
		privacyPolicyVersion = questionnaire.PrivacyPolicyVersion
	}
	var storedRegistrationID uuid.UUID
	err := tx.queryRowContext(ctx, `
INSERT INTO xiangwan_registration_snapshots (
    registration_id,
    tenant_id,
    series_id,
    instance_id,
    session_id,
    principal_id,
    instance_publication_version,
    session_version,
    price_cents,
    privacy_policy_version,
    contact_source,
    contact_name,
    contact_phone_e164,
    contact_policy_version,
    questionnaire_version_id,
    questionnaire_version,
    questionnaire_privacy_purpose,
    questionnaire_privacy_policy_version,
    request_fingerprint,
    created_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11, $12, $13, $14,
    $15, $16, $17, $18, $19, $20
)
RETURNING registration_id
`,
		created.ID,
		created.TenantID,
		created.SeriesID,
		created.InstanceID,
		created.SessionID,
		created.PrincipalID,
		instancePublicationVersion,
		session.version,
		*session.priceCents,
		acknowledgedPrivacyPolicyVersion,
		submission.Contact.Source,
		submission.Contact.Name,
		submission.Contact.PhoneE164,
		submission.Contact.PolicyVersion,
		questionnaireVersionID,
		questionnaireVersion,
		privacyPurpose,
		privacyPolicyVersion,
		submission.RequestFingerprint,
		now,
	).Scan(&storedRegistrationID)
	if err != nil {
		return fmt.Errorf("create paid Registration snapshot: %w", err)
	}
	if storedRegistrationID != created.ID {
		return ErrPaidRegistrationTransactionConflict
	}
	if questionnaire == nil {
		if len(answers) != 0 {
			return ErrPaidRegistrationTransactionConflict
		}
		return nil
	}
	if len(answers) != len(questionnaire.Fields) {
		return ErrPaidRegistrationTransactionConflict
	}
	for index, field := range questionnaire.Fields {
		if answers[index].FieldID != field.FieldID {
			return ErrPaidRegistrationTransactionConflict
		}
		optionsJSON, marshalErr := json.Marshal(field.Options)
		if marshalErr != nil {
			return fmt.Errorf("encode paid Registration options: %w", marshalErr)
		}
		answerJSON, marshalErr := json.Marshal(answers[index].Values)
		if marshalErr != nil {
			return fmt.Errorf("encode paid Registration answer: %w", marshalErr)
		}
		var storedFieldID uuid.UUID
		err := tx.queryRowContext(ctx, `
INSERT INTO xiangwan_registration_answers (
    tenant_id,
    instance_id,
    registration_id,
    questionnaire_version_id,
    field_id,
    field_code,
    field_type,
    field_label,
    field_help_text,
    is_required,
    sort_order,
    min_length,
    max_length,
    max_selections,
    options,
    answer_values,
    created_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9,
    $10, $11, $12, $13, $14, $15, $16, $17
)
RETURNING field_id
`,
			created.TenantID,
			created.InstanceID,
			created.ID,
			questionnaire.QuestionnaireVersionID,
			field.FieldID,
			field.Code,
			field.Type,
			field.Label,
			field.HelpText,
			field.Required,
			field.SortOrder,
			field.MinLength,
			field.MaxLength,
			field.MaxSelections,
			optionsJSON,
			answerJSON,
			now,
		).Scan(&storedFieldID)
		if err != nil {
			return fmt.Errorf("create paid Registration answer: %w", err)
		}
		if storedFieldID != field.FieldID {
			return ErrPaidRegistrationTransactionConflict
		}
	}
	return nil
}

type lockedPaidRegistrationSeries struct {
	status  activity.SeriesStatus
	version int64
}

func lockPaidRegistrationSeries(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (lockedPaidRegistrationSeries, error) {
	var value lockedPaidRegistrationSeries
	err := tx.queryRowContext(ctx, `
SELECT status, version
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, seriesID).Scan(&value.status, &value.version)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedPaidRegistrationSeries{}, ErrPaidRegistrationUnavailable
	}
	if err != nil {
		return lockedPaidRegistrationSeries{}, fmt.Errorf("lock paid Registration Series: %w", err)
	}
	return value, nil
}

type lockedPaidRegistrationInstance struct {
	status       activity.InstanceStatus
	activityType activity.ActivityType
}

func lockPaidRegistrationInstance(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (lockedPaidRegistrationInstance, error) {
	var value lockedPaidRegistrationInstance
	err := tx.queryRowContext(ctx, `
SELECT status, activity_type
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND series_id = $2 AND id = $3
FOR UPDATE
`, tenantID, seriesID, instanceID).Scan(&value.status, &value.activityType)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedPaidRegistrationInstance{}, ErrPaidRegistrationUnavailable
	}
	if err != nil {
		return lockedPaidRegistrationInstance{}, fmt.Errorf("lock paid Registration Instance: %w", err)
	}
	return value, nil
}

type lockedPaidRegistrationSession struct {
	status              activity.SessionStatus
	registrationStartAt time.Time
	registrationEndAt   time.Time
	sessionStartAt      time.Time
	sessionEndAt        time.Time
	capacity            int
	groupMinimum        int
	lowStockThreshold   *int
	priceCents          *int64
	confirmedCount      int
	activeHoldCount     int
	version             int64
}

func lockPaidRegistrationSession(
	ctx context.Context,
	tx paidRegistrationTransaction,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (lockedPaidRegistrationSession, error) {
	var session lockedPaidRegistrationSession
	var registrationStartAt sql.NullTime
	var registrationEndAt sql.NullTime
	var sessionStartAt sql.NullTime
	var sessionEndAt sql.NullTime
	var capacity sql.NullInt64
	var groupMinimum sql.NullInt64
	var lowStockThreshold sql.NullInt64
	var priceCents sql.NullInt64
	err := tx.queryRowContext(ctx, `
SELECT
    status,
    registration_start_at,
    registration_end_at,
    session_start_at,
    session_end_at,
    capacity,
    group_minimum,
    low_stock_threshold,
    price_cents,
    confirmed_registration_count,
    active_hold_count,
    version
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2 AND id = $3
FOR UPDATE
`, tenantID, instanceID, sessionID).Scan(
		&session.status,
		&registrationStartAt,
		&registrationEndAt,
		&sessionStartAt,
		&sessionEndAt,
		&capacity,
		&groupMinimum,
		&lowStockThreshold,
		&priceCents,
		&session.confirmedCount,
		&session.activeHoldCount,
		&session.version,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return lockedPaidRegistrationSession{}, ErrPaidRegistrationUnavailable
	}
	if err != nil {
		return lockedPaidRegistrationSession{}, fmt.Errorf("lock paid Registration Session: %w", err)
	}
	session.registrationStartAt = registrationStartAt.Time
	session.registrationEndAt = registrationEndAt.Time
	session.sessionStartAt = sessionStartAt.Time
	session.sessionEndAt = sessionEndAt.Time
	session.capacity = int(capacity.Int64)
	session.groupMinimum = int(groupMinimum.Int64)
	session.lowStockThreshold = paidNullableIntPointer(lowStockThreshold)
	session.priceCents = nullInt64Pointer(priceCents)
	return session, nil
}

func incrementPaidRegistrationHold(
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
SET active_hold_count = active_hold_count + 1,
    version = version + 1,
    updated_at = $4
WHERE tenant_id = $1
  AND instance_id = $2
  AND id = $3
  AND status = 'published'
  AND version = $5
  AND confirmed_registration_count + active_hold_count < capacity
RETURNING version
`, tenantID, instanceID, sessionID, now, expectedVersion).Scan(&newVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPaidRegistrationCapacityConflict
	}
	if err != nil {
		return fmt.Errorf("increment paid Registration capacity hold: %w", err)
	}
	return nil
}

func scanPaidRegistration(row rowScanner) (registration.Registration, error) {
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

func paidRegistrationMatchesCommand(
	existing registration.Registration,
	command StartPaidRegistrationCommand,
) bool {
	return existing.TenantID == command.TenantID &&
		existing.SeriesID == command.SeriesID &&
		existing.InstanceID == command.InstanceID &&
		existing.SessionID == command.SessionID &&
		existing.PrincipalID == command.PrincipalID
}

func paidRegistrationContextMatchesCommand(
	existing PaidRegistrationContext,
	command StartPaidRegistrationCommand,
) bool {
	order := existing.Payment.Order
	if !paidRegistrationMatchesCommand(existing.Registration, command) ||
		order.IdempotencyKey != command.IdempotencyKey ||
		order.MerchantOrderNo != command.MerchantOrderNo ||
		order.PaymentAppID != command.PaymentAppID ||
		order.PaymentMerchantID != command.PaymentMerchantID ||
		order.MerchantConfigGenerationID != command.MerchantConfigGenerationID {
		return false
	}
	if command.CouponID == nil {
		return existing.Coupon == nil && order.DiscountCents == 0
	}
	return existing.Coupon != nil &&
		existing.Coupon.Instrument.ID == *command.CouponID &&
		existing.Coupon.Instrument.FaceValueCents == order.DiscountCents
}

func paidRegistrationCouponMatches(
	context PaidRegistrationContext,
	couponID *uuid.UUID,
) bool {
	if couponID == nil {
		return context.Coupon == nil && context.Payment.Order.DiscountCents == 0
	}
	return context.Coupon != nil && context.Coupon.Instrument.ID == *couponID &&
		context.Coupon.Instrument.FaceValueCents == context.Payment.Order.DiscountCents
}

func paymentContextMatchesRegistration(
	order payment.Order,
	hold payment.CapacityHold,
	value registration.Registration,
) bool {
	return order.TenantID == value.TenantID &&
		order.RegistrationID == value.ID &&
		order.SeriesID == value.SeriesID &&
		order.InstanceID == value.InstanceID &&
		order.SessionID == value.SessionID &&
		order.PrincipalID == value.PrincipalID &&
		hold.TenantID == value.TenantID &&
		hold.OrderID == order.ID &&
		hold.RegistrationID == value.ID &&
		hold.SessionID == value.SessionID
}

func classifyPaidRegistrationWriteError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return err
	}
	if postgresError.Code == "23514" && strings.Contains(
		postgresError.Message,
		"xiangwan merchant config generation identity is not active for payment facts",
	) {
		return ErrPaymentMerchantConfigUnavailable
	}
	if postgresError.Code != "23505" {
		return err
	}
	switch postgresError.ConstraintName {
	case "xiangwan_registrations_tenant_idempotency_key_key",
		"xiangwan_orders_tenant_idempotency_key",
		"xiangwan_orders_merchant_order_key":
		return ErrPaidRegistrationIdempotencyConflict
	case "uq_xiangwan_registrations_open_principal_session":
		return ErrPaidRegistrationAlreadyOpen
	default:
		return ErrPaidRegistrationTransactionConflict
	}
}

func classifyPaidRegistrationCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "40001" {
		return fmt.Errorf("%w: %v", ErrPaidRegistrationTransactionConflict, err)
	}
	return fmt.Errorf("commit xiangwan paid Registration transaction: %w", err)
}

func paidNullableIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}

type paidRegistrationTransaction interface {
	queryExecutor
	lockActiveGeneration(context.Context, uuid.UUID, uuid.UUID) error
	couponOrderRepository() couponOrderRepository
	Commit() error
	Rollback() error
}

type paidRegistrationTransactionStarter interface {
	beginTx(context.Context, *sql.TxOptions) (paidRegistrationTransaction, error)
}

type sqlPaidRegistrationTransactionStarter struct {
	db *sql.DB
}

func (starter sqlPaidRegistrationTransactionStarter) beginTx(
	ctx context.Context,
	options *sql.TxOptions,
) (paidRegistrationTransaction, error) {
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlPaidRegistrationTransaction{tx: tx}, nil
}

type sqlPaidRegistrationTransaction struct {
	tx *sql.Tx
}

func (tx *sqlPaidRegistrationTransaction) lockActiveGeneration(
	ctx context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	var writeEpoch int64
	err := tx.tx.QueryRowContext(ctx, `
SELECT write_epoch
FROM xiangwan_runtime_generations
WHERE singleton_id = 1
  AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1
  AND active_generation_id = $2
  AND write_epoch > 0
  AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, tenantID, generationID).Scan(&writeEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPaidRegistrationGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan paid Registration generation: %w", err)
	}
	return nil
}

func (tx *sqlPaidRegistrationTransaction) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return tx.tx.QueryRowContext(ctx, query, args...)
}

func (tx *sqlPaidRegistrationTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlPaidRegistrationTransaction) Rollback() error {
	return tx.tx.Rollback()
}
