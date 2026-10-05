package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidInstanceCancellationPreviewCommand = errors.New(
		"invalid xiangwan Instance cancellation preview command",
	)
	ErrInvalidInstanceCancellationCommand = errors.New(
		"invalid xiangwan Instance cancellation command",
	)
	ErrInstanceCancellationNotFound = errors.New(
		"xiangwan Instance cancellation target not found",
	)
	ErrInstanceCancellationConflict = errors.New(
		"xiangwan Instance cancellation conflicts with recorded fact",
	)
	ErrInstanceCancellationPreviewConflict = errors.New(
		"xiangwan Instance cancellation preview conflicts with current facts",
	)
	ErrInstanceCancellationTransaction = errors.New(
		"xiangwan Instance cancellation transaction conflict",
	)
)

// InstanceCancellationAuthorization is evaluated on the cancellation
// transaction before any activity facts are locked or changed. The adapter
// supplies the tenant and authenticated administrator identity; the callback
// must lock the live Principal, identity link, and activity Grant rows for the
// duration of this Serializable transaction.
type InstanceCancellationAuthorization func(
	context.Context,
	*sql.Tx,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) error

type PreviewInstanceCancellationCommand struct {
	TenantID       uuid.UUID
	InstanceID     uuid.UUID
	RequestedBy    uuid.UUID
	IdentityLinkID uuid.UUID
	IdempotencyKey string
	Reason         string
}

type InstanceCancellationCommand struct {
	TenantID                uuid.UUID
	InstanceID              uuid.UUID
	PreviewID               uuid.UUID
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	ExpectedInstanceVersion int64
	IdempotencyKey          string
	Reason                  string
	NotificationStrategy    activity.CancellationNotificationStrategy
}

type InstanceCancellationResult struct {
	Instance        activity.Instance
	Receipt         activity.InstanceCancellationReceipt
	SessionReceipts []activity.SessionCancellationReceipt
}

type InstanceCancellationPreviewer struct {
	resolver     instanceCancellationResolver
	transactions instanceCancellationTransactionStarter
	now          func() time.Time
}

type InstanceCanceller struct {
	resolver     instanceCancellationResolver
	transactions instanceCancellationTransactionStarter
	couponPolicy coupon.RefundPolicyEvaluator
	now          func() time.Time
}

func NewInstanceCancellationPreviewer(db *sql.DB) *InstanceCancellationPreviewer {
	return NewInstanceCancellationPreviewerWithAuthorization(db, nil)
}

func NewInstanceCancellationPreviewerWithAuthorization(
	db *sql.DB,
	authorization InstanceCancellationAuthorization,
) *InstanceCancellationPreviewer {
	return &InstanceCancellationPreviewer{
		resolver: instanceCancellationSQLResolver{db: db},
		transactions: instanceCancellationSQLTransactionStarter{
			db:            db,
			authorization: authorization,
		},
		now: time.Now,
	}
}

func NewInstanceCanceller(db *sql.DB) *InstanceCanceller {
	return NewInstanceCancellerWithCouponRefundPolicy(db, nil)
}

func NewInstanceCancellerWithCouponRefundPolicy(
	db *sql.DB,
	policy coupon.RefundPolicyEvaluator,
) *InstanceCanceller {
	return NewInstanceCancellerWithCouponRefundPolicyAndAuthorization(db, policy, nil)
}

func NewInstanceCancellerWithCouponRefundPolicyAndAuthorization(
	db *sql.DB,
	policy coupon.RefundPolicyEvaluator,
	authorization InstanceCancellationAuthorization,
) *InstanceCanceller {
	return &InstanceCanceller{
		resolver: instanceCancellationSQLResolver{db: db},
		transactions: instanceCancellationSQLTransactionStarter{
			db:            db,
			authorization: authorization,
		},
		couponPolicy: policy,
		now:          time.Now,
	}
}

func (previewer *InstanceCancellationPreviewer) Preview(
	ctx context.Context,
	command PreviewInstanceCancellationCommand,
) (activity.InstanceCancellationPreview, error) {
	if err := validateInstanceCancellationPreviewCommand(command); err != nil {
		return activity.InstanceCancellationPreview{}, err
	}
	target, err := previewer.resolver.resolveInstanceCancellationTarget(
		ctx,
		command.TenantID,
		command.InstanceID,
	)
	if errors.Is(err, errInstanceCancellationTargetNotFound) {
		return activity.InstanceCancellationPreview{}, ErrInstanceCancellationNotFound
	}
	if err != nil {
		return activity.InstanceCancellationPreview{}, err
	}
	tx, err := previewer.transactions.beginInstanceCancellationTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return activity.InstanceCancellationPreview{}, fmt.Errorf(
			"begin xiangwan Instance cancellation preview transaction: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := tx.authorizeInstanceCancellation(
		ctx,
		command.TenantID,
		command.RequestedBy,
		command.IdentityLinkID,
	); err != nil {
		return activity.InstanceCancellationPreview{}, err
	}

	seriesStatus, err := tx.lockSeries(ctx, target.TenantID, target.SeriesID)
	if err != nil {
		return activity.InstanceCancellationPreview{}, err
	}
	current, err := tx.lockInstanceRecord(
		ctx,
		target.TenantID,
		target.SeriesID,
		target.InstanceID,
	)
	if err != nil {
		return activity.InstanceCancellationPreview{}, err
	}
	existing, previewErr := tx.getInstancePreviewByKey(
		ctx,
		command.TenantID,
		command.IdempotencyKey,
	)
	if previewErr == nil {
		if !instanceCancellationPreviewMatchesCommand(existing, command) {
			return activity.InstanceCancellationPreview{},
				ErrInstanceCancellationPreviewConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.InstanceCancellationPreview{},
				classifyInstanceCancellationCommitError(err)
		}
		committed = true
		return existing, nil
	}
	if !errors.Is(previewErr, errInstanceCancellationPreviewNotFound) {
		return activity.InstanceCancellationPreview{}, previewErr
	}
	if seriesStatus != activity.SeriesStatusActive ||
		current.Status != activity.InstanceStatusPublished {
		return activity.InstanceCancellationPreview{},
			ErrInstanceCancellationPreviewConflict
	}
	sessions, err := tx.listInstanceSessions(
		ctx,
		current.TenantID,
		current.ID,
	)
	if err != nil {
		return activity.InstanceCancellationPreview{}, err
	}
	plan, err := buildInstanceCancellationPlan(ctx, tx, current, sessions)
	if err != nil {
		return activity.InstanceCancellationPreview{}, err
	}
	createdAt := previewer.now().UTC().Truncate(time.Microsecond)
	if createdAt.IsZero() || createdAt.Before(current.UpdatedAt) {
		return activity.InstanceCancellationPreview{},
			ErrInvalidInstanceCancellationPreviewCommand
	}
	created, err := activity.NewInstanceCancellationPreview(
		activity.InstanceCancellationPreviewCommand{
			RequestedBy:    command.RequestedBy,
			IdempotencyKey: command.IdempotencyKey,
			Reason:         command.Reason,
			At:             createdAt,
		},
		plan.snapshot,
	)
	if err != nil {
		return activity.InstanceCancellationPreview{}, fmt.Errorf(
			"%w: %v",
			ErrInstanceCancellationTransaction,
			err,
		)
	}
	created, err = tx.createInstancePreview(ctx, created)
	if err != nil {
		return activity.InstanceCancellationPreview{},
			classifyInstanceCancellationWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return activity.InstanceCancellationPreview{},
			classifyInstanceCancellationCommitError(err)
	}
	committed = true
	return created, nil
}

func (canceller *InstanceCanceller) Cancel(
	ctx context.Context,
	command InstanceCancellationCommand,
) (InstanceCancellationResult, error) {
	if err := validateInstanceCancellationCommand(command); err != nil {
		return InstanceCancellationResult{}, err
	}
	target, err := canceller.resolver.resolveInstanceCancellationTarget(
		ctx,
		command.TenantID,
		command.InstanceID,
	)
	if errors.Is(err, errInstanceCancellationTargetNotFound) {
		return InstanceCancellationResult{}, ErrInstanceCancellationNotFound
	}
	if err != nil {
		return InstanceCancellationResult{}, err
	}
	tx, err := canceller.transactions.beginInstanceCancellationTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return InstanceCancellationResult{}, fmt.Errorf(
			"begin xiangwan Instance cancellation transaction: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := tx.authorizeInstanceCancellation(
		ctx,
		command.TenantID,
		command.ActorID,
		command.IdentityLinkID,
	); err != nil {
		return InstanceCancellationResult{}, err
	}

	seriesStatus, err := tx.lockSeries(ctx, target.TenantID, target.SeriesID)
	if err != nil {
		return InstanceCancellationResult{}, err
	}
	current, err := tx.lockInstanceRecord(
		ctx,
		target.TenantID,
		target.SeriesID,
		target.InstanceID,
	)
	if err != nil {
		return InstanceCancellationResult{}, err
	}
	existingReceipt, receiptErr := tx.getInstanceReceipt(
		ctx,
		current.TenantID,
		current.ID,
	)
	if current.Status == activity.InstanceStatusCancelled {
		if receiptErr != nil {
			return InstanceCancellationResult{},
				ErrInstanceCancellationTransaction
		}
		if !instanceCancellationReceiptMatchesCommand(existingReceipt, command) {
			return InstanceCancellationResult{},
				ErrInstanceCancellationConflict
		}
		if err := tx.Commit(); err != nil {
			return InstanceCancellationResult{},
				classifyInstanceCancellationCommitError(err)
		}
		committed = true
		return InstanceCancellationResult{
			Instance: current,
			Receipt:  existingReceipt,
		}, nil
	}
	if receiptErr == nil {
		return InstanceCancellationResult{}, ErrInstanceCancellationConflict
	}
	if !errors.Is(receiptErr, errInstanceCancellationReceiptNotFound) {
		return InstanceCancellationResult{}, receiptErr
	}
	if seriesStatus != activity.SeriesStatusActive ||
		current.Status != activity.InstanceStatusPublished {
		return InstanceCancellationResult{}, ErrInstanceCancellationConflict
	}
	if current.Version != command.ExpectedInstanceVersion {
		return InstanceCancellationResult{},
			ErrInstanceCancellationPreviewConflict
	}
	processedAt := canceller.now().UTC().Truncate(time.Microsecond)
	if processedAt.IsZero() || processedAt.Before(current.UpdatedAt) {
		return InstanceCancellationResult{},
			ErrInvalidInstanceCancellationCommand
	}
	preview, err := tx.lockInstancePreview(
		ctx,
		command.TenantID,
		command.PreviewID,
	)
	if errors.Is(err, errInstanceCancellationPreviewNotFound) {
		return InstanceCancellationResult{},
			ErrInstanceCancellationPreviewConflict
	}
	if err != nil {
		return InstanceCancellationResult{}, err
	}
	if !instanceCancellationPreviewMatchesCancellationCommand(
		preview,
		command,
		target,
		processedAt,
	) {
		return InstanceCancellationResult{},
			ErrInstanceCancellationPreviewConflict
	}
	sessions, err := tx.listInstanceSessions(
		ctx,
		current.TenantID,
		current.ID,
	)
	if err != nil {
		return InstanceCancellationResult{}, err
	}
	plan, err := buildInstanceCancellationPlan(ctx, tx, current, sessions)
	if err != nil {
		return InstanceCancellationResult{}, err
	}
	matches, err := activity.InstanceCancellationPreviewMatchesSnapshot(
		preview,
		plan.snapshot,
	)
	if err != nil || !matches {
		return InstanceCancellationResult{},
			ErrInstanceCancellationPreviewConflict
	}

	sessionReceipts, err := executeInstanceCancellationSessions(
		ctx,
		tx,
		command,
		preview,
		plan,
		processedAt,
		canceller.couponPolicy,
	)
	if err != nil {
		return InstanceCancellationResult{}, err
	}
	cancelled, receipt, err := activity.CancelInstance(
		current,
		activity.CancelInstanceCommand{
			PreviewID:            command.PreviewID,
			CancelledBy:          command.ActorID,
			IdempotencyKey:       command.IdempotencyKey,
			Reason:               command.Reason,
			NotificationStrategy: command.NotificationStrategy,
			At:                   processedAt,
			RecordedAt:           processedAt,
		},
		plan.assessment,
	)
	if err != nil {
		return InstanceCancellationResult{}, fmt.Errorf(
			"%w: %v",
			ErrInstanceCancellationTransaction,
			err,
		)
	}
	cancelled, err = tx.updateCancelledInstance(ctx, cancelled, current.Version)
	if err != nil {
		return InstanceCancellationResult{},
			classifyInstanceCancellationWriteError(err)
	}
	if _, err := tx.consumeInstancePreview(
		ctx,
		command.TenantID,
		command.PreviewID,
		cancelled.UpdatedAt,
	); err != nil {
		return InstanceCancellationResult{},
			classifyInstanceCancellationWriteError(err)
	}
	receipt.ResultingInstanceVersion = cancelled.Version
	receipt.CancelledAt = cancelled.UpdatedAt
	receipt, err = tx.createInstanceReceipt(ctx, receipt)
	if err != nil {
		return InstanceCancellationResult{},
			classifyInstanceCancellationWriteError(err)
	}
	if err := tx.Commit(); err != nil {
		return InstanceCancellationResult{},
			classifyInstanceCancellationCommitError(err)
	}
	committed = true
	return InstanceCancellationResult{
		Instance:        cancelled,
		Receipt:         receipt,
		SessionReceipts: sessionReceipts,
	}, nil
}

func executeInstanceCancellationSessions(
	ctx context.Context,
	tx instanceCancellationTransaction,
	command InstanceCancellationCommand,
	preview activity.InstanceCancellationPreview,
	plan instanceCancellationPlan,
	processedAt time.Time,
	couponPolicy coupon.RefundPolicyEvaluator,
) ([]activity.SessionCancellationReceipt, error) {
	receipts := make(
		[]activity.SessionCancellationReceipt,
		0,
		plan.assessment.TargetSessionCount,
	)
	for _, sessionPlan := range plan.sessions {
		if sessionPlan.cancellation == nil {
			continue
		}
		current := sessionPlan.current
		childPreview, err := activity.NewSessionCancellationPreview(
			activity.SessionCancellationPreviewCommand{
				RequestedBy:    command.ActorID,
				IdempotencyKey: instanceCancellationSessionPreviewKey(current.ID),
				At:             processedAt,
			},
			sessionPlan.cancellation.snapshot,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: create child Session preview: %v",
				ErrInstanceCancellationTransaction,
				err,
			)
		}
		parentID := preview.ID
		childPreview.ParentInstancePreviewID = &parentID
		childPreview, err = tx.createPreview(ctx, childPreview)
		if err != nil {
			return nil, classifyInstanceCancellationWriteError(err)
		}
		childCommand := SessionCancellationCommand{
			TenantID:               command.TenantID,
			SessionID:              current.ID,
			PreviewID:              childPreview.ID,
			ActorID:                command.ActorID,
			ExpectedSessionVersion: current.Version,
			IdempotencyKey:         instanceCancellationSessionOperationKey(current.ID),
			Reason:                 command.Reason,
			NotificationStrategy:   command.NotificationStrategy,
		}
		impact, err := applySessionCancellationPlan(
			ctx,
			tx,
			childCommand,
			*sessionPlan.cancellation,
			processedAt,
			instanceCancellationCause,
			couponPolicy,
		)
		if err != nil {
			return nil, err
		}
		cancelled, receipt, err := activity.CancelSession(
			current,
			activity.CancelSessionCommand{
				SeriesID:             plan.snapshot.SeriesID,
				PreviewID:            childPreview.ID,
				CancelledBy:          command.ActorID,
				IdempotencyKey:       childCommand.IdempotencyKey,
				Reason:               command.Reason,
				NotificationStrategy: command.NotificationStrategy,
				At:                   processedAt,
				RecordedAt:           processedAt,
			},
			impact,
		)
		if err != nil {
			return nil, fmt.Errorf(
				"%w: cancel child Session: %v",
				ErrInstanceCancellationTransaction,
				err,
			)
		}
		cancelled, err = tx.updateSession(ctx, cancelled, current.Version)
		if err != nil {
			return nil, classifyInstanceCancellationWriteError(err)
		}
		if _, err := tx.consumePreview(
			ctx,
			command.TenantID,
			childPreview.ID,
			cancelled.UpdatedAt,
		); err != nil {
			return nil, classifyInstanceCancellationWriteError(err)
		}
		receipt.ResultingSessionVersion = cancelled.Version
		receipt.CancelledAt = cancelled.UpdatedAt
		receipt, err = tx.createReceipt(ctx, receipt)
		if err != nil {
			return nil, classifyInstanceCancellationWriteError(err)
		}
		receipts = append(receipts, receipt)
	}
	return receipts, nil
}

func validateInstanceCancellationPreviewCommand(
	command PreviewInstanceCancellationCommand,
) error {
	if command.TenantID == uuid.Nil ||
		command.InstanceID == uuid.Nil ||
		command.RequestedBy == uuid.Nil ||
		strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey ||
		!sessionCancellationOperationKeyPattern.MatchString(command.IdempotencyKey) ||
		strings.TrimSpace(command.Reason) != command.Reason ||
		command.Reason == "" ||
		len([]rune(command.Reason)) > 500 {
		return ErrInvalidInstanceCancellationPreviewCommand
	}
	return nil
}

func validateInstanceCancellationCommand(
	command InstanceCancellationCommand,
) error {
	reason := strings.TrimSpace(command.Reason)
	if command.TenantID == uuid.Nil ||
		command.InstanceID == uuid.Nil ||
		command.PreviewID == uuid.Nil ||
		command.ActorID == uuid.Nil ||
		command.ExpectedInstanceVersion < 1 ||
		command.ExpectedInstanceVersion == math.MaxInt64 ||
		!sessionCancellationOperationKeyPattern.MatchString(command.IdempotencyKey) ||
		reason == "" ||
		reason != command.Reason ||
		len([]rune(reason)) > 500 ||
		command.NotificationStrategy !=
			activity.CancellationNotificationManualRequired {
		return ErrInvalidInstanceCancellationCommand
	}
	return nil
}

func instanceCancellationPreviewMatchesCommand(
	value activity.InstanceCancellationPreview,
	command PreviewInstanceCancellationCommand,
) bool {
	return value.TenantID == command.TenantID &&
		value.InstanceID == command.InstanceID &&
		value.RequestedBy == command.RequestedBy &&
		value.IdempotencyKey == command.IdempotencyKey &&
		value.CancellationReason == command.Reason
}

func instanceCancellationPreviewMatchesCancellationCommand(
	value activity.InstanceCancellationPreview,
	command InstanceCancellationCommand,
	target instanceCancellationTarget,
	processedAt time.Time,
) bool {
	return value.ID == command.PreviewID &&
		value.TenantID == command.TenantID &&
		value.SeriesID == target.SeriesID &&
		value.InstanceID == command.InstanceID &&
		value.RequestedBy == command.ActorID &&
		value.ExpectedInstanceVersion == command.ExpectedInstanceVersion &&
		value.CancellationReason == command.Reason &&
		value.NotificationStrategy == command.NotificationStrategy &&
		value.ConsumedAt == nil &&
		!processedAt.Before(value.CreatedAt) &&
		!processedAt.After(value.ExpiresAt)
}

func instanceCancellationReceiptMatchesCommand(
	value activity.InstanceCancellationReceipt,
	command InstanceCancellationCommand,
) bool {
	return value.TenantID == command.TenantID &&
		value.InstanceID == command.InstanceID &&
		value.PreviewID == command.PreviewID &&
		value.IdempotencyKey == command.IdempotencyKey &&
		value.CancelledBy == command.ActorID &&
		value.CancellationReason == command.Reason &&
		value.NotificationStrategy == command.NotificationStrategy &&
		value.ResultingInstanceVersion == command.ExpectedInstanceVersion+1
}

func classifyInstanceCancellationWriteError(err error) error {
	if errors.Is(err, coupon.ErrInvalidEntry) ||
		errors.Is(err, coupon.ErrInvalidLedger) {
		return ErrInstanceCancellationTransaction
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23505":
			return ErrInstanceCancellationConflict
		case "23503", "23514", "40001":
			return fmt.Errorf("%w: %v", ErrInstanceCancellationTransaction, err)
		}
	}
	return err
}

func classifyInstanceCancellationCommitError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "23503", "23514", "40001":
			return fmt.Errorf("%w: %v", ErrInstanceCancellationTransaction, err)
		}
	}
	return fmt.Errorf("commit xiangwan Instance cancellation: %w", err)
}

var (
	errInstanceCancellationTargetNotFound  = errors.New("Instance cancellation target not found")
	errInstanceCancellationPreviewNotFound = errors.New("Instance cancellation preview not found")
	errInstanceCancellationReceiptNotFound = errors.New("Instance cancellation receipt not found")
)

type instanceCancellationTarget struct {
	TenantID   uuid.UUID
	SeriesID   uuid.UUID
	InstanceID uuid.UUID
}

type instanceCancellationResolver interface {
	resolveInstanceCancellationTarget(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (instanceCancellationTarget, error)
}

type instanceCancellationTransaction interface {
	sessionCancellationTransaction
	authorizeInstanceCancellation(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) error
	lockInstanceRecord(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (activity.Instance, error)
	listInstanceSessions(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) ([]activity.Session, error)
	getInstancePreviewByKey(
		context.Context,
		uuid.UUID,
		string,
	) (activity.InstanceCancellationPreview, error)
	lockInstancePreview(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (activity.InstanceCancellationPreview, error)
	createInstancePreview(
		context.Context,
		activity.InstanceCancellationPreview,
	) (activity.InstanceCancellationPreview, error)
	consumeInstancePreview(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) (activity.InstanceCancellationPreview, error)
	getInstanceReceipt(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (activity.InstanceCancellationReceipt, error)
	updateCancelledInstance(
		context.Context,
		activity.Instance,
		int64,
	) (activity.Instance, error)
	createInstanceReceipt(
		context.Context,
		activity.InstanceCancellationReceipt,
	) (activity.InstanceCancellationReceipt, error)
}

type instanceCancellationTransactionStarter interface {
	beginInstanceCancellationTx(
		context.Context,
		*sql.TxOptions,
	) (instanceCancellationTransaction, error)
}
