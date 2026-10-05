package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

var (
	ErrInvalidSessionCancellationPreviewCommand = errors.New(
		"invalid xiangwan Session cancellation preview command",
	)
	ErrSessionCancellationPreviewConflict = errors.New(
		"xiangwan Session cancellation preview conflicts with recorded fact",
	)
)

type PreviewSessionCancellationCommand struct {
	TenantID       uuid.UUID
	SessionID      uuid.UUID
	RequestedBy    uuid.UUID
	IdentityLinkID uuid.UUID
	RequestID      string
	IdempotencyKey string
}

// SessionCancellationPreviewer produces the short-lived PostgreSQL snapshot
// that an operator must confirm before cancelling one Session.
type SessionCancellationPreviewer struct {
	resolver     sessionCancellationResolver
	transactions sessionCancellationTransactionStarter
	now          func() time.Time
	hooks        *SessionCancellationHooks
}

func NewSessionCancellationPreviewer(db *sql.DB) *SessionCancellationPreviewer {
	return NewSessionCancellationPreviewerWithHooks(db, nil)
}

func NewSessionCancellationPreviewerWithHooks(db *sql.DB, hooks *SessionCancellationHooks) *SessionCancellationPreviewer {
	return &SessionCancellationPreviewer{
		resolver:     sessionCancellationSQLResolver{db: db},
		transactions: sessionCancellationSQLTransactionStarter{db: db},
		now:          time.Now,
		hooks:        hooks,
	}
}

func (previewer *SessionCancellationPreviewer) Preview(
	ctx context.Context,
	command PreviewSessionCancellationCommand,
) (activity.SessionCancellationPreview, error) {
	if err := validateSessionCancellationPreviewCommand(command); err != nil {
		return activity.SessionCancellationPreview{}, err
	}
	target, err := previewer.resolver.resolveSessionCancellationTarget(
		ctx,
		command.TenantID,
		command.SessionID,
	)
	if errors.Is(err, errSessionCancellationTargetNotFound) {
		return activity.SessionCancellationPreview{}, ErrSessionCancellationNotFound
	}
	if err != nil {
		return activity.SessionCancellationPreview{}, err
	}

	tx, err := previewer.transactions.beginSessionCancellationTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return activity.SessionCancellationPreview{}, fmt.Errorf(
			"begin xiangwan Session cancellation preview transaction: %w",
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if previewer.hooks != nil {
		carrier, ok := tx.(sessionCancellationSQLCarrier)
		if !ok || previewer.hooks.Authorize == nil || command.IdentityLinkID == uuid.Nil || command.RequestID == "" {
			return activity.SessionCancellationPreview{}, ErrInvalidSessionCancellationPreviewCommand
		}
		if err := previewer.hooks.Authorize(ctx, carrier.cancellationSQLTx(), command.TenantID, command.RequestedBy, command.IdentityLinkID); err != nil {
			return activity.SessionCancellationPreview{}, err
		}
	}
	seriesStatus, err := tx.lockSeries(ctx, target.TenantID, target.SeriesID)
	if err != nil {
		return activity.SessionCancellationPreview{}, err
	}
	instanceStatus, err := tx.lockInstance(
		ctx,
		target.TenantID,
		target.SeriesID,
		target.InstanceID,
	)
	if err != nil {
		return activity.SessionCancellationPreview{}, err
	}
	current, err := tx.lockSession(
		ctx,
		target.TenantID,
		target.InstanceID,
		target.SessionID,
	)
	if err != nil {
		return activity.SessionCancellationPreview{}, err
	}
	if !sessionCancellationTargetMatches(target, current) {
		return activity.SessionCancellationPreview{}, ErrSessionCancellationTransaction
	}

	existing, previewErr := tx.getPreviewByKey(
		ctx,
		command.TenantID,
		command.IdempotencyKey,
	)
	if previewErr == nil {
		if !sessionCancellationPreviewMatchesCommand(existing, command) {
			return activity.SessionCancellationPreview{},
				ErrSessionCancellationPreviewConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.SessionCancellationPreview{},
				classifySessionCancellationCommitError(err)
		}
		committed = true
		return existing, nil
	}
	if !errors.Is(previewErr, errSessionCancellationPreviewNotFound) {
		return activity.SessionCancellationPreview{}, previewErr
	}
	if seriesStatus != activity.SeriesStatusActive ||
		instanceStatus != activity.InstanceStatusPublished ||
		current.Status != activity.SessionStatusPublished {
		return activity.SessionCancellationPreview{},
			ErrSessionCancellationPreviewConflict
	}

	registrations, err := tx.listOpenRegistrations(
		ctx,
		current.TenantID,
		current.ID,
	)
	if err != nil {
		return activity.SessionCancellationPreview{}, err
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
		return activity.SessionCancellationPreview{}, err
	}
	createdAt := previewer.now().UTC().Truncate(time.Microsecond)
	if createdAt.IsZero() || createdAt.Before(current.UpdatedAt) {
		return activity.SessionCancellationPreview{},
			ErrInvalidSessionCancellationPreviewCommand
	}
	created, err := activity.NewSessionCancellationPreview(
		activity.SessionCancellationPreviewCommand{
			RequestedBy:    command.RequestedBy,
			IdempotencyKey: command.IdempotencyKey,
			At:             createdAt,
		},
		plan.snapshot,
	)
	if err != nil {
		return activity.SessionCancellationPreview{}, fmt.Errorf(
			"%w: %v",
			ErrSessionCancellationTransaction,
			err,
		)
	}
	created, err = tx.createPreview(ctx, created)
	if err != nil {
		return activity.SessionCancellationPreview{},
			classifySessionCancellationPreviewWriteError(err)
	}
	if previewer.hooks != nil && previewer.hooks.Previewed != nil {
		if err := previewer.hooks.Previewed(ctx, tx.(sessionCancellationSQLCarrier).cancellationSQLTx(), command, created); err != nil {
			return activity.SessionCancellationPreview{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return activity.SessionCancellationPreview{},
			classifySessionCancellationCommitError(err)
	}
	committed = true
	return created, nil
}

func validateSessionCancellationPreviewCommand(
	command PreviewSessionCancellationCommand,
) error {
	if command.TenantID == uuid.Nil ||
		command.SessionID == uuid.Nil ||
		command.RequestedBy == uuid.Nil ||
		!sessionCancellationOperationKeyPattern.MatchString(
			strings.TrimSpace(command.IdempotencyKey),
		) ||
		strings.TrimSpace(command.IdempotencyKey) != command.IdempotencyKey {
		return ErrInvalidSessionCancellationPreviewCommand
	}
	return nil
}

func sessionCancellationPreviewMatchesCommand(
	value activity.SessionCancellationPreview,
	command PreviewSessionCancellationCommand,
) bool {
	return value.TenantID == command.TenantID &&
		value.SessionID == command.SessionID &&
		value.RequestedBy == command.RequestedBy &&
		value.IdempotencyKey == command.IdempotencyKey
}

func classifySessionCancellationPreviewWriteError(err error) error {
	classified := classifySessionCancellationWriteError(err)
	if errors.Is(classified, ErrSessionCancellationConflict) {
		return ErrSessionCancellationPreviewConflict
	}
	return classified
}
