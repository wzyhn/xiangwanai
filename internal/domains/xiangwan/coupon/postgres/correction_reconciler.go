package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

var (
	ErrInvalidCorrectionCommand = errors.New(
		`invalid xiangwan Coupon correction command`,
	)
	ErrCorrectionSourceNotFound = errors.New(
		`xiangwan Coupon correction Checkin source not found`,
	)
)

type CorrectionCommand struct {
	TenantID  uuid.UUID
	CheckinID uuid.UUID
}

type CorrectionResult struct {
	Corrections    []coupon.Entry
	Changed        bool
	ManualRequired bool
}

type CorrectionReconciler struct {
	transactions    correctionTransactionStarter
	pending         pendingCorrectionLister
	now             func() time.Time
	generationFence func(context.Context, CouponAuthorizationQuery, uuid.UUID) error
}

func NewCorrectionReconciler(db *sql.DB) *CorrectionReconciler {
	return &CorrectionReconciler{
		transactions: sqlCorrectionTransactionStarter{db: db},
		pending:      NewRepository(db),
		now:          time.Now,
	}
}

func NewCorrectionReconcilerWithGeneration(
	db *sql.DB, tenantID, generationID uuid.UUID,
) (*CorrectionReconciler, error) {
	if db == nil || tenantID == uuid.Nil || generationID == uuid.Nil {
		return nil, ErrInvalidCorrectionCommand
	}
	reconciler := NewCorrectionReconciler(db)
	reconciler.generationFence = func(ctx context.Context, query CouponAuthorizationQuery, target uuid.UUID) error {
		if target != tenantID {
			return ErrCouponGenerationInactive
		}
		return requireActiveCouponGeneration(ctx, query, target, generationID)
	}
	return reconciler, nil
}

func (reconciler *CorrectionReconciler) ReconcilePending(
	ctx context.Context,
	tenantID uuid.UUID,
	limit int,
) ([]CorrectionResult, error) {
	if reconciler == nil || reconciler.pending == nil ||
		tenantID == uuid.Nil || limit < 1 || limit > 500 {
		return nil, ErrInvalidCorrectionCommand
	}
	sources, err := reconciler.pending.ListPendingCorrectionSources(
		ctx,
		tenantID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	results := make([]CorrectionResult, 0, len(sources))
	for _, source := range sources {
		result, reconcileErr := reconciler.Reconcile(
			ctx,
			CorrectionCommand(source),
		)
		if reconcileErr != nil {
			return results, reconcileErr
		}
		results = append(results, result)
	}
	return results, nil
}

func (reconciler *CorrectionReconciler) Reconcile(
	ctx context.Context,
	command CorrectionCommand,
) (CorrectionResult, error) {
	if reconciler == nil || reconciler.transactions == nil ||
		reconciler.now == nil || command.TenantID == uuid.Nil ||
		command.CheckinID == uuid.Nil {
		return CorrectionResult{}, ErrInvalidCorrectionCommand
	}
	tx, err := reconciler.transactions.beginCorrectionTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return CorrectionResult{}, fmt.Errorf(
			`begin xiangwan Coupon correction: %w`,
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if reconciler.generationFence != nil {
		if err := reconciler.generationFence(ctx, tx.authorizationQuery(), command.TenantID); err != nil {
			return CorrectionResult{}, err
		}
	}

	source, err := tx.lockRevokedCheckin(
		ctx,
		command.TenantID,
		command.CheckinID,
	)
	if err != nil {
		return CorrectionResult{}, err
	}
	if err := validateRevokedCouponSource(source); err != nil {
		return CorrectionResult{}, err
	}
	couponIDs, err := tx.listSourceCouponIDsForUpdate(
		ctx,
		command.TenantID,
		source.Checkin.PrincipalID,
		command.CheckinID,
	)
	if err != nil {
		return CorrectionResult{}, err
	}
	result := CorrectionResult{
		Corrections: make([]coupon.Entry, 0, len(couponIDs)),
	}
	if len(couponIDs) == 0 {
		return commitCorrectionResult(tx, result, &committed)
	}
	if len(couponIDs) != coupon.GrantQuantity {
		return CorrectionResult{}, ErrGrantFactsConflict
	}
	now := reconciler.now().UTC().Truncate(time.Microsecond)
	if now.Before(source.RevokedEvent.OccurredAt) {
		return CorrectionResult{}, ErrGrantFactsConflict
	}
	for _, couponID := range couponIDs {
		ledger, ledgerErr := tx.getLedgerForUpdate(
			ctx,
			command.TenantID,
			source.Checkin.PrincipalID,
			couponID,
		)
		if ledgerErr != nil {
			return CorrectionResult{}, ledgerErr
		}
		if ledger.Instrument.SourceCheckin == nil ||
			*ledger.Instrument.SourceCheckin != command.CheckinID {
			return CorrectionResult{}, ErrGrantFactsConflict
		}
		correction, changed, manual, correctionErr :=
			reconcileCouponCorrection(ledger, source, now)
		if correctionErr != nil {
			return CorrectionResult{}, correctionErr
		}
		if changed {
			correction, correctionErr = tx.appendLifecycleEntry(
				ctx,
				correction,
			)
			if correctionErr != nil {
				return CorrectionResult{},
					classifyLifecycleError(correctionErr)
			}
			result.Changed = true
		}
		result.ManualRequired = result.ManualRequired || manual
		result.Corrections = append(result.Corrections, correction)
	}
	return commitCorrectionResult(tx, result, &committed)
}

func reconcileCouponCorrection(
	ledger Ledger,
	source revokedCouponSource,
	recordedAt time.Time,
) (coupon.Entry, bool, bool, error) {
	businessKey := `checkin-correction:` + source.RevokedEvent.ID.String()
	if invalidated := findLifecycleEntry(
		ledger.Entries,
		coupon.EntryTypeInvalidated,
		businessKey,
	); invalidated != nil {
		return *invalidated, false, false, nil
	}
	projection, err := coupon.Project(
		ledger.Instrument,
		ledger.Entries,
		recordedAt,
	)
	if err != nil {
		return coupon.Entry{}, false, false, ErrGrantFactsConflict
	}
	if exception := findLifecycleEntry(
		ledger.Entries,
		coupon.EntryTypeCorrectionRequired,
		businessKey,
	); exception != nil &&
		(projection.Status == coupon.StatusHeld ||
			projection.Status == coupon.StatusRedeemed) {
		return *exception, false, true, nil
	}
	reason := ``
	if source.RevokedEvent.Reason != nil {
		reason = *source.RevokedEvent.Reason
	}
	candidate, err := coupon.CorrectForRevokedCheckin(
		coupon.CheckinCorrectionCommand{
			Instrument:            ledger.Instrument,
			History:               ledger.Entries,
			RevokedCheckinEventID: source.RevokedEvent.ID,
			ActorID:               source.RevokedEvent.ActorID,
			Reason:                reason,
			At:                    source.RevokedEvent.OccurredAt,
			RecordedAt:            recordedAt,
		},
	)
	if err != nil {
		return coupon.Entry{}, false, false, err
	}
	return candidate,
		true,
		candidate.EntryType == coupon.EntryTypeCorrectionRequired,
		nil
}

func validateRevokedCouponSource(source revokedCouponSource) error {
	if checkin.Validate(source.Checkin) != nil ||
		checkin.ValidateEvent(source.RevokedEvent) != nil ||
		source.Checkin.CheckinStatus != checkin.StatusRevoked ||
		source.RevokedEvent.TenantID != source.Checkin.TenantID ||
		source.RevokedEvent.CheckinID != source.Checkin.ID ||
		source.RevokedEvent.RegistrationID !=
			source.Checkin.RegistrationID ||
		source.RevokedEvent.SessionID != source.Checkin.SessionID ||
		source.RevokedEvent.EventType != checkin.EventTypeRevoked ||
		source.RevokedEvent.ActorID == uuid.Nil ||
		source.RevokedEvent.Reason == nil ||
		source.Checkin.RevokedAt == nil ||
		!source.RevokedEvent.OccurredAt.Equal(*source.Checkin.RevokedAt) {
		return ErrGrantFactsConflict
	}
	return nil
}

func commitCorrectionResult(
	tx correctionTransaction,
	result CorrectionResult,
	committed *bool,
) (CorrectionResult, error) {
	if err := tx.Commit(); err != nil {
		return CorrectionResult{}, classifyLifecycleError(err)
	}
	*committed = true
	return result, nil
}

type revokedCouponSource struct {
	Checkin      checkin.Checkin
	RevokedEvent checkin.Event
}

type pendingCorrectionLister interface {
	ListPendingCorrectionSources(
		context.Context,
		uuid.UUID,
		int,
	) ([]PendingCorrectionSource, error)
}

type correctionTransactionStarter interface {
	beginCorrectionTx(
		context.Context,
		*sql.TxOptions,
	) (correctionTransaction, error)
}

type correctionTransaction interface {
	authorizationQuery() CouponAuthorizationQuery
	lockRevokedCheckin(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (revokedCouponSource, error)
	listSourceCouponIDsForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) ([]uuid.UUID, error)
	getLedgerForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (Ledger, error)
	appendLifecycleEntry(
		context.Context,
		coupon.Entry,
	) (coupon.Entry, error)
	Commit() error
	Rollback() error
}
