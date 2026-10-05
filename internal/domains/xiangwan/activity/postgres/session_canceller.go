package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidSessionCancellationCommand = errors.New("invalid xiangwan Session cancellation command")
	ErrSessionCancellationNotFound       = errors.New("xiangwan Session cancellation target not found")
	ErrSessionCancellationConflict       = errors.New("xiangwan Session cancellation conflicts with recorded fact")
	ErrSessionCancellationTransaction    = errors.New("xiangwan Session cancellation transaction conflict")
	ErrSessionCancellationCouponPolicy   = errors.New("xiangwan Session cancellation Coupon policy is unavailable")
)

var sessionCancellationOperationKeyPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`,
)

type SessionCancellationCommand struct {
	TenantID               uuid.UUID
	SessionID              uuid.UUID
	PreviewID              uuid.UUID
	ActorID                uuid.UUID
	IdentityLinkID         uuid.UUID
	RequestID              string
	ExpectedSessionVersion int64
	IdempotencyKey         string
	Reason                 string
	NotificationStrategy   activity.CancellationNotificationStrategy
}

type SessionCancellationResult struct {
	Session activity.Session
	Receipt activity.SessionCancellationReceipt
}

// SessionCancellationHooks keep trusted administrator authorization and audit
// inside the same transaction as the existing cancellation facts.
type SessionCancellationHooks struct {
	Authorize InstanceCancellationAuthorization
	Previewed func(context.Context, *sql.Tx, PreviewSessionCancellationCommand, activity.SessionCancellationPreview) error
	Cancelled func(context.Context, *sql.Tx, SessionCancellationCommand, SessionCancellationResult) error
}

type sessionCancellationSQLCarrier interface{ cancellationSQLTx() *sql.Tx }

// SessionCanceller is the operator boundary for cancelling one published
// Session. Authorization and preview confirmation belong to the admin
// transport; this service owns the all-or-nothing state convergence.
type SessionCanceller struct {
	resolver     sessionCancellationResolver
	transactions sessionCancellationTransactionStarter
	couponPolicy coupon.RefundPolicyEvaluator
	now          func() time.Time
	hooks        *SessionCancellationHooks
}

func NewSessionCanceller(db *sql.DB) *SessionCanceller {
	return NewSessionCancellerWithCouponRefundPolicy(db, nil)
}

func NewSessionCancellerWithCouponRefundPolicy(
	db *sql.DB,
	policy coupon.RefundPolicyEvaluator,
) *SessionCanceller {
	return NewSessionCancellerWithHooks(db, policy, nil)
}

func NewSessionCancellerWithHooks(db *sql.DB, policy coupon.RefundPolicyEvaluator, hooks *SessionCancellationHooks) *SessionCanceller {
	return &SessionCanceller{
		resolver:     sessionCancellationSQLResolver{db: db},
		transactions: sessionCancellationSQLTransactionStarter{db: db},
		couponPolicy: policy,
		now:          time.Now,
		hooks:        hooks,
	}
}

func (canceller *SessionCanceller) Cancel(
	ctx context.Context,
	command SessionCancellationCommand,
) (SessionCancellationResult, error) {
	if err := validateSessionCancellationCommand(command); err != nil {
		return SessionCancellationResult{}, err
	}
	target, err := canceller.resolver.resolveSessionCancellationTarget(
		ctx,
		command.TenantID,
		command.SessionID,
	)
	if errors.Is(err, errSessionCancellationTargetNotFound) {
		return SessionCancellationResult{}, ErrSessionCancellationNotFound
	}
	if err != nil {
		return SessionCancellationResult{}, err
	}

	tx, err := canceller.transactions.beginSessionCancellationTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return SessionCancellationResult{}, fmt.Errorf(
			"begin xiangwan Session cancellation transaction: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if canceller.hooks != nil {
		carrier, ok := tx.(sessionCancellationSQLCarrier)
		if !ok || canceller.hooks.Authorize == nil || command.IdentityLinkID == uuid.Nil || command.RequestID == "" {
			return SessionCancellationResult{}, ErrInvalidSessionCancellationCommand
		}
		if err := canceller.hooks.Authorize(ctx, carrier.cancellationSQLTx(), command.TenantID, command.ActorID, command.IdentityLinkID); err != nil {
			return SessionCancellationResult{}, err
		}
	}
	seriesStatus, err := tx.lockSeries(ctx, target.TenantID, target.SeriesID)
	if err != nil {
		return SessionCancellationResult{}, err
	}
	instanceStatus, err := tx.lockInstance(
		ctx,
		target.TenantID,
		target.SeriesID,
		target.InstanceID,
	)
	if err != nil {
		return SessionCancellationResult{}, err
	}
	current, err := tx.lockSession(
		ctx,
		target.TenantID,
		target.InstanceID,
		target.SessionID,
	)
	if err != nil {
		return SessionCancellationResult{}, err
	}
	if !sessionCancellationTargetMatches(target, current) {
		return SessionCancellationResult{}, ErrSessionCancellationTransaction
	}

	existingReceipt, receiptErr := tx.getReceiptBySession(
		ctx,
		current.TenantID,
		current.ID,
	)
	if current.Status == activity.SessionStatusCancelled {
		if receiptErr != nil {
			return SessionCancellationResult{}, ErrSessionCancellationTransaction
		}
		if !sessionCancellationReceiptMatchesCommand(existingReceipt, command) {
			return SessionCancellationResult{}, ErrSessionCancellationConflict
		}
		if err := tx.Commit(); err != nil {
			return SessionCancellationResult{}, classifySessionCancellationCommitError(err)
		}
		committed = true
		return SessionCancellationResult{
			Session: current,
			Receipt: existingReceipt,
		}, nil
	}
	if receiptErr == nil {
		return SessionCancellationResult{}, ErrSessionCancellationConflict
	}
	if !errors.Is(receiptErr, errSessionCancellationReceiptNotFound) {
		return SessionCancellationResult{}, receiptErr
	}
	if seriesStatus != activity.SeriesStatusActive ||
		instanceStatus != activity.InstanceStatusPublished ||
		current.Status != activity.SessionStatusPublished {
		return SessionCancellationResult{}, ErrSessionCancellationConflict
	}
	if current.Version != command.ExpectedSessionVersion {
		return SessionCancellationResult{}, ErrSessionCancellationPreviewConflict
	}

	processedAt := canceller.now().UTC().Truncate(time.Microsecond)
	if processedAt.IsZero() || processedAt.Before(current.UpdatedAt) {
		return SessionCancellationResult{}, ErrInvalidSessionCancellationCommand
	}
	preview, err := tx.lockPreview(ctx, command.TenantID, command.PreviewID)
	if errors.Is(err, errSessionCancellationPreviewNotFound) {
		return SessionCancellationResult{}, ErrSessionCancellationPreviewConflict
	}
	if err != nil {
		return SessionCancellationResult{}, err
	}
	if !sessionCancellationPreviewMatchesCancellationCommand(
		preview,
		command,
		target,
		processedAt,
	) {
		return SessionCancellationResult{}, ErrSessionCancellationPreviewConflict
	}
	registrations, err := tx.listOpenRegistrations(
		ctx,
		current.TenantID,
		current.ID,
	)
	if err != nil {
		return SessionCancellationResult{}, err
	}
	plan, err := buildSessionCancellationPlan(
		ctx,
		tx,
		current,
		target.SeriesID,
		registrations,
		sessionCancellationCause,
	)
	if err != nil {
		return SessionCancellationResult{}, err
	}
	matches, err := activity.SessionCancellationPreviewMatchesSnapshot(
		preview,
		plan.snapshot,
	)
	if err != nil || !matches {
		return SessionCancellationResult{}, ErrSessionCancellationPreviewConflict
	}
	impact, err := applySessionCancellationPlan(
		ctx,
		tx,
		command,
		plan,
		processedAt,
		sessionCancellationCause,
		canceller.couponPolicy,
	)
	if err != nil {
		return SessionCancellationResult{}, err
	}

	cancelled, receipt, err := activity.CancelSession(
		current,
		activity.CancelSessionCommand{
			SeriesID:             target.SeriesID,
			PreviewID:            command.PreviewID,
			CancelledBy:          command.ActorID,
			IdempotencyKey:       command.IdempotencyKey,
			Reason:               command.Reason,
			NotificationStrategy: command.NotificationStrategy,
			At:                   processedAt,
			RecordedAt:           processedAt,
		},
		impact,
	)
	if err != nil {
		return SessionCancellationResult{}, fmt.Errorf(
			"%w: %v",
			ErrSessionCancellationTransaction,
			err,
		)
	}
	cancelled, err = tx.updateSession(ctx, cancelled, current.Version)
	if err != nil {
		return SessionCancellationResult{}, classifySessionCancellationWriteError(err)
	}
	if _, err := tx.consumePreview(
		ctx,
		command.TenantID,
		command.PreviewID,
		cancelled.UpdatedAt,
	); err != nil {
		return SessionCancellationResult{},
			classifySessionCancellationWriteError(err)
	}
	receipt.ResultingSessionVersion = cancelled.Version
	receipt.CancelledAt = cancelled.UpdatedAt
	receipt, err = tx.createReceipt(ctx, receipt)
	if err != nil {
		return SessionCancellationResult{}, classifySessionCancellationWriteError(err)
	}

	result := SessionCancellationResult{Session: cancelled, Receipt: receipt}
	if canceller.hooks != nil && canceller.hooks.Cancelled != nil {
		if err := canceller.hooks.Cancelled(ctx, tx.(sessionCancellationSQLCarrier).cancellationSQLTx(), command, result); err != nil {
			return SessionCancellationResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return SessionCancellationResult{}, classifySessionCancellationCommitError(err)
	}
	committed = true
	return result, nil
}

func finishSessionCancellationHold(
	current payment.CapacityHold,
	processedAt time.Time,
) (payment.CapacityHold, bool, error) {
	if processedAt.Before(current.ExpiresAt) {
		return payment.ReleaseCapacityHold(current, "session_cancelled", processedAt)
	}
	return payment.ExpireCapacityHold(current, "session_cancelled", processedAt)
}

func validateSessionCancellationCommand(command SessionCancellationCommand) error {
	reason := strings.TrimSpace(command.Reason)
	if command.TenantID == uuid.Nil ||
		command.SessionID == uuid.Nil ||
		command.PreviewID == uuid.Nil ||
		command.ActorID == uuid.Nil ||
		command.ExpectedSessionVersion < 1 ||
		command.ExpectedSessionVersion == math.MaxInt64 ||
		!sessionCancellationOperationKeyPattern.MatchString(command.IdempotencyKey) ||
		reason == "" ||
		reason != command.Reason ||
		len([]rune(reason)) > 500 ||
		command.NotificationStrategy !=
			activity.CancellationNotificationManualRequired {
		return ErrInvalidSessionCancellationCommand
	}
	return nil
}

func sessionCancellationTargetMatches(
	target sessionCancellationTarget,
	current activity.Session,
) bool {
	return target.TenantID == current.TenantID &&
		target.InstanceID == current.InstanceID &&
		target.SessionID == current.ID
}

func sessionCancellationRegistrationMatches(
	session activity.Session,
	current registration.Registration,
) bool {
	return current.TenantID == session.TenantID &&
		current.InstanceID == session.InstanceID &&
		current.SessionID == session.ID
}

func sessionCancellationPaymentMatches(
	current registration.Registration,
	order payment.Order,
	hold payment.CapacityHold,
) bool {
	return order.TenantID == current.TenantID &&
		order.RegistrationID == current.ID &&
		order.SeriesID == current.SeriesID &&
		order.InstanceID == current.InstanceID &&
		order.SessionID == current.SessionID &&
		order.PrincipalID == current.PrincipalID &&
		hold.TenantID == current.TenantID &&
		hold.OrderID == order.ID &&
		hold.RegistrationID == current.ID &&
		hold.SessionID == current.SessionID
}

func sessionCancellationRefundMatches(
	value refund.Case,
	current registration.Registration,
	order payment.Order,
	reason refund.ReasonCode,
) bool {
	return order.ActualPaidCents != nil &&
		value.TenantID == current.TenantID &&
		value.OrderID == order.ID &&
		value.RegistrationID == current.ID &&
		value.SeriesID == current.SeriesID &&
		value.InstanceID == current.InstanceID &&
		value.SessionID == current.SessionID &&
		value.PrincipalID == current.PrincipalID &&
		value.ReasonCode == reason &&
		value.RequestedRefundCents == *order.ActualPaidCents
}

func sessionCancellationReceiptMatchesCommand(
	value activity.SessionCancellationReceipt,
	command SessionCancellationCommand,
) bool {
	return value.TenantID == command.TenantID &&
		value.SessionID == command.SessionID &&
		(value.PreviewID == uuid.Nil || value.PreviewID == command.PreviewID) &&
		value.IdempotencyKey == command.IdempotencyKey &&
		value.CancelledBy == command.ActorID &&
		value.CancellationReason == command.Reason &&
		(value.NotificationStrategy == "" ||
			value.NotificationStrategy == command.NotificationStrategy) &&
		(value.ResultingSessionVersion == 0 ||
			value.ResultingSessionVersion == command.ExpectedSessionVersion+1)
}

func sessionCancellationPreviewMatchesCancellationCommand(
	value activity.SessionCancellationPreview,
	command SessionCancellationCommand,
	target sessionCancellationTarget,
	processedAt time.Time,
) bool {
	return value.ID == command.PreviewID &&
		value.TenantID == command.TenantID &&
		value.SeriesID == target.SeriesID &&
		value.InstanceID == target.InstanceID &&
		value.SessionID == command.SessionID &&
		value.RequestedBy == command.ActorID &&
		value.ExpectedSessionVersion == command.ExpectedSessionVersion &&
		value.NotificationStrategy == command.NotificationStrategy &&
		value.ConsumedAt == nil &&
		!processedAt.Before(value.CreatedAt) &&
		!processedAt.After(value.ExpiresAt)
}

func classifySessionCancellationWriteError(err error) error {
	if errors.Is(err, coupon.ErrInvalidEntry) ||
		errors.Is(err, coupon.ErrInvalidLedger) {
		return ErrSessionCancellationTransaction
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return ErrSessionCancellationConflict
		case "23503", "23514", "40001":
			return fmt.Errorf("%w: %v", ErrSessionCancellationTransaction, err)
		}
	}
	return err
}

func classifySessionCancellationCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23503", "23514", "40001":
			return fmt.Errorf("%w: %v", ErrSessionCancellationTransaction, err)
		}
	}
	return fmt.Errorf("commit xiangwan Session cancellation: %w", err)
}

var (
	errSessionCancellationTargetNotFound  = errors.New("Session cancellation target not found")
	errSessionCancellationReceiptNotFound = errors.New("Session cancellation receipt not found")
	errSessionCancellationRefundNotFound  = errors.New("Session cancellation Refund not found")
	errSessionCancellationPreviewNotFound = errors.New("Session cancellation preview not found")
)

type sessionCancellationTarget struct {
	TenantID   uuid.UUID
	SeriesID   uuid.UUID
	InstanceID uuid.UUID
	SessionID  uuid.UUID
}

type sessionCancellationResolver interface {
	resolveSessionCancellationTarget(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (sessionCancellationTarget, error)
}

type sessionCancellationTransaction interface {
	lockSeries(context.Context, uuid.UUID, uuid.UUID) (activity.SeriesStatus, error)
	lockInstance(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (activity.InstanceStatus, error)
	lockSession(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (activity.Session, error)
	getReceiptBySession(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (activity.SessionCancellationReceipt, error)
	getPreviewByKey(
		context.Context,
		uuid.UUID,
		string,
	) (activity.SessionCancellationPreview, error)
	lockPreview(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (activity.SessionCancellationPreview, error)
	listOpenRegistrations(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) ([]registration.Registration, error)
	lockOrderByRegistration(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (payment.Order, bool, error)
	lockHoldByOrder(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (payment.CapacityHold, error)
	lockRefundByOrder(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (refund.Case, error)
	lockCouponByOrder(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (couponpostgres.Ledger, error)
	updateRegistration(
		context.Context,
		registration.Registration,
		int64,
	) (registration.Registration, error)
	updateOrder(context.Context, payment.Order, int64) (payment.Order, error)
	updateHold(
		context.Context,
		payment.CapacityHold,
		int64,
	) (payment.CapacityHold, error)
	releaseCouponForOrder(context.Context, uuid.UUID, uuid.UUID, string, time.Time) error
	appendCouponEntry(context.Context, coupon.Entry) (coupon.Entry, error)
	createRefund(context.Context, refund.Case) (refund.Case, error)
	updateSession(context.Context, activity.Session, int64) (activity.Session, error)
	createReceipt(
		context.Context,
		activity.SessionCancellationReceipt,
	) (activity.SessionCancellationReceipt, error)
	createPreview(
		context.Context,
		activity.SessionCancellationPreview,
	) (activity.SessionCancellationPreview, error)
	consumePreview(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) (activity.SessionCancellationPreview, error)
	Commit() error
	Rollback() error
}

type sessionCancellationTransactionStarter interface {
	beginSessionCancellationTx(
		context.Context,
		*sql.TxOptions,
	) (sessionCancellationTransaction, error)
}
