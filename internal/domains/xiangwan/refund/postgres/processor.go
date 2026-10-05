package refundpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidRefundOperationCommand = errors.New("invalid xiangwan Refund operation command")
	ErrRefundOperationNotFound       = errors.New("xiangwan Refund operation target not found")
	ErrRefundOperationConflict       = errors.New("xiangwan Refund operation conflicts with recorded fact")
	ErrRefundOperationTransaction    = errors.New("xiangwan Refund operation transaction conflict")
	ErrCouponRefundPolicyUnavailable = errors.New("xiangwan Coupon refund policy is unavailable")
)

var refundOperationKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type StartProcessingCommand struct {
	TenantID                uuid.UUID
	RefundCaseID            uuid.UUID
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	ExpectedVersion         int64
	ReplayIgnoresOccurredAt bool
	IdempotencyKey          string
	OperatorNote            string
	OccurredAt              time.Time
}

type CompleteRefundCommand struct {
	TenantID                    uuid.UUID
	RefundCaseID                uuid.UUID
	ActorID                     uuid.UUID
	IdentityLinkID              uuid.UUID
	ExpectedVersion             int64
	RequireDistinctPriorHandler bool
	ReplayIgnoresOccurredAt     bool
	IdempotencyKey              string
	SuccessfulRefundCents       int64
	ExternalRefundID            string
	EvidenceReference           string
	OperatorNote                string
	OccurredAt                  time.Time
}

type ResolveRefundCommand struct {
	TenantID                uuid.UUID
	RefundCaseID            uuid.UUID
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	ExpectedVersion         int64
	RequireProcessing       bool
	ReplayIgnoresOccurredAt bool
	IdempotencyKey          string
	FailureReason           string
	OperatorNote            string
	OccurredAt              time.Time
}

type RefundOperationResult struct {
	Case             refund.Case
	Event            refund.Event
	CouponAdjustment *coupon.Entry
}

// Processor records manual actions without calling a refund provider. The
// administrator adapter injects live authorization into the same serializable
// transaction and applies stricter version and second-handler requirements.
type Processor struct {
	transactions refundOperationTransactionStarter
	couponPolicy coupon.RefundPolicyEvaluator
	now          func() time.Time
}

// RefundOperationAuthorizer runs inside the same serializable transaction as
// the financial transition. The admin adapter locks its live generation,
// identity link and action-specific Grant here.
type RefundOperationAuthorizer func(
	context.Context, *sql.Tx, uuid.UUID, uuid.UUID, string,
) error

func NewProcessor(db *sql.DB) *Processor {
	return NewProcessorWithCouponRefundPolicy(db, nil)
}

func NewProcessorWithCouponRefundPolicy(
	db *sql.DB,
	policy coupon.RefundPolicyEvaluator,
) *Processor {
	return &Processor{
		transactions: refundOperationSQLTransactionStarter{db: db},
		couponPolicy: policy,
		now:          time.Now,
	}
}

func NewAuthorizedProcessorWithCouponRefundPolicy(
	db *sql.DB,
	policy coupon.RefundPolicyEvaluator,
	authorize RefundOperationAuthorizer,
) *Processor {
	processor := NewProcessorWithCouponRefundPolicy(db, policy)
	processor.transactions = refundOperationSQLTransactionStarter{db: db, authorize: authorize}
	return processor
}

func (processor *Processor) StartProcessing(
	ctx context.Context,
	command StartProcessingCommand,
) (RefundOperationResult, error) {
	return processor.process(ctx, refundOperationCommand{
		action:                 refundOperationStart,
		tenantID:               command.TenantID,
		refundCaseID:           command.RefundCaseID,
		actorID:                command.ActorID,
		identityLinkID:         command.IdentityLinkID,
		expectedVersion:        command.ExpectedVersion,
		ignoreReplayOccurredAt: command.ReplayIgnoresOccurredAt,
		idempotencyKey:         command.IdempotencyKey,
		operatorNote:           command.OperatorNote,
		occurredAt:             command.OccurredAt,
	})
}

func (processor *Processor) Complete(
	ctx context.Context,
	command CompleteRefundCommand,
) (RefundOperationResult, error) {
	return processor.process(ctx, refundOperationCommand{
		action:                      refundOperationComplete,
		tenantID:                    command.TenantID,
		refundCaseID:                command.RefundCaseID,
		actorID:                     command.ActorID,
		identityLinkID:              command.IdentityLinkID,
		expectedVersion:             command.ExpectedVersion,
		requireDistinctPriorHandler: command.RequireDistinctPriorHandler,
		ignoreReplayOccurredAt:      command.ReplayIgnoresOccurredAt,
		idempotencyKey:              command.IdempotencyKey,
		successfulRefundCents:       command.SuccessfulRefundCents,
		externalRefundID:            command.ExternalRefundID,
		evidenceReference:           command.EvidenceReference,
		operatorNote:                command.OperatorNote,
		occurredAt:                  command.OccurredAt,
	})
}

func (processor *Processor) Fail(
	ctx context.Context,
	command ResolveRefundCommand,
) (RefundOperationResult, error) {
	return processor.resolve(ctx, refundOperationFail, command)
}

func (processor *Processor) Reject(
	ctx context.Context,
	command ResolveRefundCommand,
) (RefundOperationResult, error) {
	return processor.resolve(ctx, refundOperationReject, command)
}

func (processor *Processor) resolve(
	ctx context.Context,
	action refundOperationAction,
	command ResolveRefundCommand,
) (RefundOperationResult, error) {
	return processor.process(ctx, refundOperationCommand{
		action:                 action,
		tenantID:               command.TenantID,
		refundCaseID:           command.RefundCaseID,
		actorID:                command.ActorID,
		identityLinkID:         command.IdentityLinkID,
		expectedVersion:        command.ExpectedVersion,
		requireProcessing:      command.RequireProcessing,
		ignoreReplayOccurredAt: command.ReplayIgnoresOccurredAt,
		idempotencyKey:         command.IdempotencyKey,
		failureReason:          command.FailureReason,
		operatorNote:           command.OperatorNote,
		occurredAt:             command.OccurredAt,
	})
}

func (processor *Processor) process(
	ctx context.Context,
	command refundOperationCommand,
) (RefundOperationResult, error) {
	command.occurredAt = command.occurredAt.UTC().Truncate(time.Microsecond)
	if err := validateRefundOperationCommand(command); err != nil {
		return RefundOperationResult{}, err
	}
	tx, err := processor.transactions.beginRefundOperationTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
		command.actorID, command.identityLinkID, string(command.action),
	)
	if err != nil {
		return RefundOperationResult{}, fmt.Errorf("begin xiangwan Refund operation transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	located, err := tx.locateRefund(ctx, command.tenantID, command.refundCaseID)
	if errors.Is(err, ErrRefundCaseNotFound) {
		return RefundOperationResult{}, ErrRefundOperationNotFound
	}
	if err != nil {
		return RefundOperationResult{}, err
	}

	currentOrder, err := tx.lockOrder(ctx, located.TenantID, located.OrderID)
	if err != nil {
		return RefundOperationResult{}, err
	}
	current, err := tx.lockRefund(ctx, located.TenantID, located.ID)
	if err != nil {
		return RefundOperationResult{}, err
	}
	if !refundOperationContextMatches(located, current, currentOrder) {
		return RefundOperationResult{}, ErrRefundOperationTransaction
	}
	var couponLedger *couponpostgres.Ledger
	var existingCouponAdjustment *coupon.Entry
	needsCouponAdjustment := false
	if currentOrder.DiscountCents > 0 {
		ledger, ledgerErr := tx.lockCouponByOrder(
			ctx,
			currentOrder.TenantID,
			currentOrder.ID,
		)
		if ledgerErr != nil {
			return RefundOperationResult{}, ledgerErr
		}
		couponLedger = &ledger
		needsCouponAdjustment, existingCouponAdjustment, ledgerErr =
			couponRefundAdjustmentState(
				ledger,
				currentOrder.ID,
				current.RequestedRefundCents == *currentOrder.ActualPaidCents,
			)
		if ledgerErr != nil {
			return RefundOperationResult{}, ledgerErr
		}
	}

	existingEvent, err := tx.getEventByIdempotencyKey(
		ctx,
		current.TenantID,
		current.ID,
		command.idempotencyKey,
	)
	if err == nil {
		if !refundEventMatchesCommand(existingEvent, command) {
			return RefundOperationResult{}, ErrRefundOperationConflict
		}
		if command.expectedVersion > 0 &&
			existingEvent.ResultingRefundVersion != command.expectedVersion+1 {
			return RefundOperationResult{}, ErrRefundOperationConflict
		}
		if command.action == refundOperationComplete &&
			needsCouponAdjustment && existingCouponAdjustment == nil {
			return RefundOperationResult{}, ErrRefundOperationTransaction
		}
		if err := tx.Commit(); err != nil {
			return RefundOperationResult{}, classifyRefundOperationCommitError(err)
		}
		committed = true
		return RefundOperationResult{
			Case:             current,
			Event:            existingEvent,
			CouponAdjustment: cloneCouponEntry(existingCouponAdjustment),
		}, nil
	}
	if !errors.Is(err, errRefundOperationEventNotFound) {
		return RefundOperationResult{}, err
	}
	if command.expectedVersion > 0 && current.Version != command.expectedVersion {
		return RefundOperationResult{}, ErrRefundOperationConflict
	}
	if command.requireProcessing && current.RefundStatus != refund.StatusProcessing {
		return RefundOperationResult{}, ErrRefundOperationConflict
	}
	if command.requireDistinctPriorHandler &&
		(current.RefundStatus != refund.StatusProcessing || current.HandledBy == nil ||
			*current.HandledBy == command.actorID) {
		return RefundOperationResult{}, ErrRefundOperationConflict
	}

	updated, changed, err := applyRefundOperation(current, command)
	if err != nil {
		if errors.Is(err, refund.ErrRefundTerminal) ||
			errors.Is(err, refund.ErrRefundReplayConflict) {
			return RefundOperationResult{}, ErrRefundOperationConflict
		}
		return RefundOperationResult{}, fmt.Errorf("%w: %v", ErrInvalidRefundOperationCommand, err)
	}
	if !changed {
		return RefundOperationResult{}, ErrRefundOperationConflict
	}
	if command.action == refundOperationComplete &&
		command.successfulRefundCents != current.RequestedRefundCents {
		return RefundOperationResult{}, ErrRefundOperationConflict
	}
	updated, err = tx.updateRefund(ctx, updated, current.Version)
	if err != nil {
		return RefundOperationResult{}, classifyRefundOperationWriteError(err)
	}
	recordedAt := processor.now().UTC().Truncate(time.Microsecond)
	if recordedAt.IsZero() || recordedAt.Before(command.occurredAt) {
		return RefundOperationResult{}, ErrInvalidRefundOperationCommand
	}
	event, err := refund.NewEvent(current, updated, command.idempotencyKey, recordedAt)
	if err != nil {
		return RefundOperationResult{}, fmt.Errorf("%w: %v", ErrRefundOperationTransaction, err)
	}
	event, err = tx.createEvent(ctx, event)
	if err != nil {
		return RefundOperationResult{}, classifyRefundOperationWriteError(err)
	}
	var couponAdjustment *coupon.Entry
	if command.action == refundOperationComplete && needsCouponAdjustment {
		if existingCouponAdjustment != nil || couponLedger == nil ||
			processor.couponPolicy == nil {
			return RefundOperationResult{}, ErrCouponRefundPolicyUnavailable
		}
		input := coupon.RefundPolicyInput{
			TenantID:       currentOrder.TenantID,
			CouponID:       couponLedger.Instrument.ID,
			OrderID:        currentOrder.ID,
			RegistrationID: currentOrder.RegistrationID,
			RefundCaseID:   &current.ID,
			Trigger:        coupon.RefundTriggerFullCashRefund,
			Reason:         "full cash refund completed",
			EvaluatedAt:    command.occurredAt,
		}
		decision, policyErr := processor.couponPolicy.EvaluateCouponRefund(
			ctx,
			input,
		)
		if policyErr != nil {
			return RefundOperationResult{}, fmt.Errorf(
				"%w: %v",
				ErrCouponRefundPolicyUnavailable,
				policyErr,
			)
		}
		if decisionErr := coupon.ValidateRefundPolicyDecision(decision); decisionErr != nil {
			return RefundOperationResult{}, fmt.Errorf(
				"%w: %v",
				ErrCouponRefundPolicyUnavailable,
				decisionErr,
			)
		}
		adjustment, adjustmentErr := coupon.ApplyRefundPolicy(
			coupon.ApplyRefundPolicyCommand{
				Instrument: couponLedger.Instrument,
				History:    couponLedger.Entries,
				Input:      input,
				Decision:   decision,
				ActorID:    command.actorID,
				RecordedAt: recordedAt,
			},
		)
		if adjustmentErr != nil {
			return RefundOperationResult{}, fmt.Errorf(
				"%w: construct Coupon adjustment: %v",
				ErrRefundOperationTransaction,
				adjustmentErr,
			)
		}
		adjustment, adjustmentErr = tx.appendCouponEntry(ctx, adjustment)
		if adjustmentErr != nil {
			return RefundOperationResult{},
				classifyRefundOperationWriteError(adjustmentErr)
		}
		couponAdjustment = &adjustment
	}

	if err := tx.Commit(); err != nil {
		return RefundOperationResult{}, classifyRefundOperationCommitError(err)
	}
	committed = true
	return RefundOperationResult{
		Case:             updated,
		Event:            event,
		CouponAdjustment: couponAdjustment,
	}, nil
}

type refundOperationAction string

const (
	refundOperationStart    refundOperationAction = "start"
	refundOperationComplete refundOperationAction = "complete"
	refundOperationFail     refundOperationAction = "fail"
	refundOperationReject   refundOperationAction = "reject"
)

type refundOperationCommand struct {
	action                      refundOperationAction
	tenantID                    uuid.UUID
	refundCaseID                uuid.UUID
	actorID                     uuid.UUID
	identityLinkID              uuid.UUID
	expectedVersion             int64
	requireDistinctPriorHandler bool
	requireProcessing           bool
	ignoreReplayOccurredAt      bool
	idempotencyKey              string
	successfulRefundCents       int64
	externalRefundID            string
	evidenceReference           string
	failureReason               string
	operatorNote                string
	occurredAt                  time.Time
}

func validateRefundOperationCommand(command refundOperationCommand) error {
	if command.tenantID == uuid.Nil ||
		command.refundCaseID == uuid.Nil ||
		command.actorID == uuid.Nil ||
		!refundOperationKeyPattern.MatchString(command.idempotencyKey) ||
		command.occurredAt.IsZero() || command.expectedVersion < 0 ||
		(command.requireDistinctPriorHandler && command.action != refundOperationComplete) {
		return ErrInvalidRefundOperationCommand
	}
	if command.requireProcessing && command.action != refundOperationFail {
		return ErrInvalidRefundOperationCommand
	}
	switch command.action {
	case refundOperationStart:
		if command.successfulRefundCents != 0 ||
			command.externalRefundID != "" ||
			command.evidenceReference != "" ||
			command.failureReason != "" {
			return ErrInvalidRefundOperationCommand
		}
	case refundOperationComplete:
		if command.successfulRefundCents <= 0 || command.failureReason != "" {
			return ErrInvalidRefundOperationCommand
		}
	case refundOperationFail, refundOperationReject:
		if command.successfulRefundCents != 0 ||
			command.externalRefundID != "" ||
			command.evidenceReference != "" {
			return ErrInvalidRefundOperationCommand
		}
	default:
		return ErrInvalidRefundOperationCommand
	}
	return nil
}

func applyRefundOperation(
	current refund.Case,
	command refundOperationCommand,
) (refund.Case, bool, error) {
	switch command.action {
	case refundOperationStart:
		return refund.StartProcessing(current, refund.StartProcessingCommand{
			HandledBy:    command.actorID,
			OperatorNote: command.operatorNote,
			At:           command.occurredAt,
		})
	case refundOperationComplete:
		return refund.Complete(current, refund.CompleteCommand{
			HandledBy:         command.actorID,
			ExternalRefundID:  command.externalRefundID,
			EvidenceReference: command.evidenceReference,
			OperatorNote:      command.operatorNote,
			At:                command.occurredAt,
		})
	case refundOperationFail:
		return refund.Fail(current, refund.ResolveFailureCommand{
			HandledBy:     command.actorID,
			FailureReason: command.failureReason,
			OperatorNote:  command.operatorNote,
			At:            command.occurredAt,
		})
	case refundOperationReject:
		return refund.Reject(current, refund.ResolveFailureCommand{
			HandledBy:     command.actorID,
			FailureReason: command.failureReason,
			OperatorNote:  command.operatorNote,
			At:            command.occurredAt,
		})
	default:
		return refund.Case{}, false, ErrInvalidRefundOperationCommand
	}
}

func refundEventMatchesCommand(event refund.Event, command refundOperationCommand) bool {
	wantType := refund.EventTypeProcessingStarted
	switch command.action {
	case refundOperationComplete:
		wantType = refund.EventTypeRefundCompleted
	case refundOperationFail:
		wantType = refund.EventTypeRefundFailed
	case refundOperationReject:
		wantType = refund.EventTypeRefundRejected
	case refundOperationStart:
	default:
		return false
	}
	return event.TenantID == command.tenantID &&
		event.RefundCaseID == command.refundCaseID &&
		event.EventType == wantType &&
		event.IdempotencyKey == command.idempotencyKey &&
		event.ActorID == command.actorID &&
		event.SuccessfulRefundCents == command.successfulRefundCents &&
		optionalRefundEventStringEquals(event.ExternalRefundID, command.externalRefundID) &&
		optionalRefundEventStringEquals(event.EvidenceReference, command.evidenceReference) &&
		optionalRefundEventStringEquals(event.FailureReason, command.failureReason) &&
		optionalRefundEventStringEquals(event.OperatorNote, command.operatorNote) &&
		(command.ignoreReplayOccurredAt || event.OccurredAt.Equal(command.occurredAt))
}

func optionalRefundEventStringEquals(recorded *string, command string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return recorded == nil
	}
	return recorded != nil && *recorded == command
}

func refundOperationContextMatches(
	located refund.Case,
	locked refund.Case,
	order payment.Order,
) bool {
	return located.ID == locked.ID &&
		located.TenantID == locked.TenantID &&
		located.OrderID == locked.OrderID &&
		located.RegistrationID == locked.RegistrationID &&
		located.SeriesID == locked.SeriesID &&
		located.InstanceID == locked.InstanceID &&
		located.SessionID == locked.SessionID &&
		located.PrincipalID == locked.PrincipalID &&
		order.PaymentStatus == payment.OrderStatusPaidConfirmed &&
		order.ActualPaidCents != nil &&
		order.TenantID == locked.TenantID &&
		order.ID == locked.OrderID &&
		order.RegistrationID == locked.RegistrationID &&
		order.SeriesID == locked.SeriesID &&
		order.InstanceID == locked.InstanceID &&
		order.SessionID == locked.SessionID &&
		order.PrincipalID == locked.PrincipalID &&
		locked.RequestedRefundCents <= *order.ActualPaidCents
}

var errRefundOperationEventNotFound = errors.New("Refund operation event not found")

type refundOperationTransaction interface {
	locateRefund(context.Context, uuid.UUID, uuid.UUID) (refund.Case, error)
	lockOrder(context.Context, uuid.UUID, uuid.UUID) (payment.Order, error)
	lockRefund(context.Context, uuid.UUID, uuid.UUID) (refund.Case, error)
	lockCouponByOrder(context.Context, uuid.UUID, uuid.UUID) (couponpostgres.Ledger, error)
	getEventByIdempotencyKey(context.Context, uuid.UUID, uuid.UUID, string) (refund.Event, error)
	updateRefund(context.Context, refund.Case, int64) (refund.Case, error)
	createEvent(context.Context, refund.Event) (refund.Event, error)
	appendCouponEntry(context.Context, coupon.Entry) (coupon.Entry, error)
	Commit() error
	Rollback() error
}

type refundOperationTransactionStarter interface {
	beginRefundOperationTx(context.Context, *sql.TxOptions, uuid.UUID, uuid.UUID, string) (refundOperationTransaction, error)
}

type refundOperationSQLTransactionStarter struct {
	db        *sql.DB
	authorize RefundOperationAuthorizer
}

func (starter refundOperationSQLTransactionStarter) beginRefundOperationTx(
	ctx context.Context,
	options *sql.TxOptions,
	actorID uuid.UUID,
	identityLinkID uuid.UUID,
	action string,
) (refundOperationTransaction, error) {
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	if starter.authorize != nil {
		if err := starter.authorize(ctx, tx, actorID, identityLinkID, action); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
	}
	return &refundOperationSQLTransaction{
		tx:       tx,
		payments: paymentpostgres.NewRepository(tx),
		refunds:  NewRepository(tx),
		coupons:  couponpostgres.NewRepository(tx),
	}, nil
}

type refundOperationSQLTransaction struct {
	tx       *sql.Tx
	payments *paymentpostgres.Repository
	refunds  *Repository
	coupons  *couponpostgres.Repository
}

func (tx *refundOperationSQLTransaction) locateRefund(
	ctx context.Context,
	tenantID uuid.UUID,
	refundCaseID uuid.UUID,
) (refund.Case, error) {
	return tx.refunds.Get(ctx, tenantID, refundCaseID)
}

func (tx *refundOperationSQLTransaction) lockOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (payment.Order, error) {
	value, err := tx.payments.GetOrderForUpdate(ctx, tenantID, orderID)
	if errors.Is(err, paymentpostgres.ErrOrderNotFound) {
		return payment.Order{}, ErrRefundOperationTransaction
	}
	return value, err
}

func (tx *refundOperationSQLTransaction) lockRefund(
	ctx context.Context,
	tenantID uuid.UUID,
	refundCaseID uuid.UUID,
) (refund.Case, error) {
	value, err := tx.refunds.GetForUpdate(ctx, tenantID, refundCaseID)
	if errors.Is(err, ErrRefundCaseNotFound) {
		return refund.Case{}, ErrRefundOperationNotFound
	}
	return value, err
}

func (tx *refundOperationSQLTransaction) lockCouponByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (couponpostgres.Ledger, error) {
	value, err := tx.coupons.GetLedgerByOrderForUpdate(ctx, tenantID, orderID)
	if errors.Is(err, couponpostgres.ErrCouponNotFound) ||
		errors.Is(err, couponpostgres.ErrGrantFactsConflict) ||
		errors.Is(err, coupon.ErrInvalidLedger) {
		return couponpostgres.Ledger{}, ErrRefundOperationTransaction
	}
	return value, err
}

func (tx *refundOperationSQLTransaction) getEventByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	refundCaseID uuid.UUID,
	idempotencyKey string,
) (refund.Event, error) {
	value, err := tx.refunds.GetEventByIdempotencyKey(
		ctx,
		tenantID,
		refundCaseID,
		idempotencyKey,
	)
	if errors.Is(err, ErrRefundEventNotFound) {
		return refund.Event{}, errRefundOperationEventNotFound
	}
	return value, err
}

func (tx *refundOperationSQLTransaction) updateRefund(
	ctx context.Context,
	value refund.Case,
	expectedVersion int64,
) (refund.Case, error) {
	return tx.refunds.Update(ctx, value, expectedVersion)
}

func (tx *refundOperationSQLTransaction) createEvent(
	ctx context.Context,
	value refund.Event,
) (refund.Event, error) {
	return tx.refunds.CreateEvent(ctx, value)
}

func (tx *refundOperationSQLTransaction) appendCouponEntry(
	ctx context.Context,
	value coupon.Entry,
) (coupon.Entry, error) {
	return tx.coupons.AppendLifecycleEntry(ctx, value)
}

func (tx *refundOperationSQLTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *refundOperationSQLTransaction) Rollback() error {
	return tx.tx.Rollback()
}

func classifyRefundOperationWriteError(err error) error {
	if errors.Is(err, ErrRefundCaseVersionConflict) {
		return ErrRefundOperationTransaction
	}
	if errors.Is(err, coupon.ErrInvalidEntry) {
		return ErrRefundOperationTransaction
	}
	if errors.Is(err, couponpostgres.ErrGrantExists) {
		return ErrRefundOperationConflict
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return ErrRefundOperationConflict
	}
	return err
}

func classifyRefundOperationCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "40001" {
		return fmt.Errorf("%w: %v", ErrRefundOperationTransaction, err)
	}
	return fmt.Errorf("commit xiangwan Refund operation transaction: %w", err)
}
