package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

func TestCorrectionReconcilerInvalidatesUnusedGrantBatch(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	source, ledgers := knownRevokedCouponBatch(t, now)
	tx := &fakeCorrectionTransaction{
		source:  source,
		ledgers: ledgers,
	}
	starter := &fakeCorrectionTransactionStarter{tx: tx}
	reconciler := &CorrectionReconciler{
		transactions: starter,
		pending:      &fakePendingCorrectionLister{},
		now:          func() time.Time { return now },
	}

	result, err := reconciler.Reconcile(
		context.Background(),
		CorrectionCommand{
			TenantID:  source.Checkin.TenantID,
			CheckinID: source.Checkin.ID,
		},
	)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !result.Changed || result.ManualRequired ||
		len(result.Corrections) != coupon.GrantQuantity ||
		len(tx.appended) != coupon.GrantQuantity ||
		!tx.committed || tx.rolledBack || starter.options == nil ||
		starter.options.Isolation != sql.LevelSerializable {
		t.Fatalf(
			"Reconcile() = %+v tx=%+v starter=%+v",
			result,
			tx,
			starter,
		)
	}
	for _, entry := range result.Corrections {
		if entry.EntryType != coupon.EntryTypeInvalidated ||
			entry.SourceCheckinEventID == nil ||
			*entry.SourceCheckinEventID != source.RevokedEvent.ID {
			t.Fatalf("correction = %+v", entry)
		}
	}
}

func TestCorrectionReconcilerEscalatesHeldCoupon(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	source, ledgers := knownRevokedCouponBatch(t, now)
	for couponID, ledger := range ledgers {
		order := knownOrderUseFacts(ledger, now.Add(-2*time.Minute))
		held := knownHoldEntry(
			t,
			ledger,
			order,
			now.Add(-3*time.Minute),
		)
		ledger.Entries = append(ledger.Entries, held)
		ledgers[couponID] = ledger
		break
	}
	tx := &fakeCorrectionTransaction{source: source, ledgers: ledgers}
	reconciler := &CorrectionReconciler{
		transactions: &fakeCorrectionTransactionStarter{tx: tx},
		pending:      &fakePendingCorrectionLister{},
		now:          func() time.Time { return now },
	}

	result, err := reconciler.Reconcile(
		context.Background(),
		CorrectionCommand{
			TenantID:  source.Checkin.TenantID,
			CheckinID: source.Checkin.ID,
		},
	)
	if err != nil || !result.Changed || !result.ManualRequired ||
		len(result.Corrections) != coupon.GrantQuantity {
		t.Fatalf("Reconcile(held) = %+v, %v", result, err)
	}
	var exceptionCount int
	for _, entry := range result.Corrections {
		if entry.EntryType == coupon.EntryTypeCorrectionRequired {
			exceptionCount++
		}
	}
	if exceptionCount != 1 {
		t.Fatalf("correction_required count = %d", exceptionCount)
	}
}

func TestCorrectionReconcilerReplaysTerminalCorrections(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	source, ledgers := knownRevokedCouponBatch(t, now)
	for couponID, ledger := range ledgers {
		entry, err := coupon.CorrectForRevokedCheckin(
			coupon.CheckinCorrectionCommand{
				Instrument:            ledger.Instrument,
				History:               ledger.Entries,
				RevokedCheckinEventID: source.RevokedEvent.ID,
				ActorID:               source.RevokedEvent.ActorID,
				Reason:                *source.RevokedEvent.Reason,
				At:                    source.RevokedEvent.OccurredAt,
				RecordedAt:            now,
			},
		)
		if err != nil {
			t.Fatalf("CorrectForRevokedCheckin() error = %v", err)
		}
		ledger.Entries = append(ledger.Entries, entry)
		ledgers[couponID] = ledger
	}
	tx := &fakeCorrectionTransaction{source: source, ledgers: ledgers}
	reconciler := &CorrectionReconciler{
		transactions: &fakeCorrectionTransactionStarter{tx: tx},
		pending:      &fakePendingCorrectionLister{},
		now:          func() time.Time { return now.Add(time.Minute) },
	}
	result, err := reconciler.Reconcile(
		context.Background(),
		CorrectionCommand{
			TenantID:  source.Checkin.TenantID,
			CheckinID: source.Checkin.ID,
		},
	)
	if err != nil || result.Changed || result.ManualRequired ||
		len(result.Corrections) != coupon.GrantQuantity ||
		len(tx.appended) != 0 || !tx.committed {
		t.Fatalf("Reconcile(replay) = %+v, %v tx=%+v", result, err, tx)
	}
}

func TestCorrectionReconcilerRejectsPartialGrantBatch(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	source, ledgers := knownRevokedCouponBatch(t, now)
	for couponID := range ledgers {
		delete(ledgers, couponID)
		break
	}
	tx := &fakeCorrectionTransaction{source: source, ledgers: ledgers}
	reconciler := &CorrectionReconciler{
		transactions: &fakeCorrectionTransactionStarter{tx: tx},
		pending:      &fakePendingCorrectionLister{},
		now:          func() time.Time { return now },
	}
	_, err := reconciler.Reconcile(
		context.Background(),
		CorrectionCommand{
			TenantID:  source.Checkin.TenantID,
			CheckinID: source.Checkin.ID,
		},
	)
	if !errors.Is(err, ErrGrantFactsConflict) ||
		tx.committed || !tx.rolledBack || len(tx.appended) != 0 {
		t.Fatalf("Reconcile(partial) error=%v tx=%+v", err, tx)
	}
}

func TestCorrectionReconcilePendingUsesPostgresSources(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	source, ledgers := knownRevokedCouponBatch(t, now)
	tx := &fakeCorrectionTransaction{source: source, ledgers: ledgers}
	pending := &fakePendingCorrectionLister{
		sources: []PendingCorrectionSource{{
			TenantID:  source.Checkin.TenantID,
			CheckinID: source.Checkin.ID,
		}},
	}
	reconciler := &CorrectionReconciler{
		transactions: &fakeCorrectionTransactionStarter{tx: tx},
		pending:      pending,
		now:          func() time.Time { return now },
	}
	results, err := reconciler.ReconcilePending(
		context.Background(),
		source.Checkin.TenantID,
		20,
	)
	if err != nil || len(results) != 1 || !results[0].Changed ||
		pending.calls != 1 || pending.limit != 20 {
		t.Fatalf(
			"ReconcilePending() = %+v, %v pending=%+v",
			results,
			err,
			pending,
		)
	}
}

func knownRevokedCouponBatch(
	t *testing.T,
	now time.Time,
) (revokedCouponSource, map[uuid.UUID]Ledger) {
	t.Helper()
	initial := knownInitialGuestSource(t, now.Add(-time.Hour), true)
	grant := knownInitialGrant(t, initial, now.Add(-time.Hour))
	before := initial.Checkin
	revoked, err := checkin.Revoke(before, checkin.RevokeCommand{
		RevokedBy: uuid.New(),
		Reason:    "incorrect invited-guest Checkin",
		At:        now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf("checkin.Revoke() error = %v", err)
	}
	event, err := checkin.NewEvent(
		&before,
		revoked,
		"coupon:checkin:revoked",
	)
	if err != nil {
		t.Fatalf("checkin.NewEvent(revoked) error = %v", err)
	}
	ledgers := make(map[uuid.UUID]Ledger, coupon.GrantQuantity)
	for index := range grant.Coupons {
		ledgers[grant.Coupons[index].ID] = Ledger{
			Instrument: grant.Coupons[index],
			Entries:    []coupon.Entry{grant.Entries[index]},
		}
	}
	return revokedCouponSource{
		Checkin:      revoked,
		RevokedEvent: event,
	}, ledgers
}

type fakeCorrectionTransactionStarter struct {
	tx      correctionTransaction
	options *sql.TxOptions
}

func (starter *fakeCorrectionTransactionStarter) beginCorrectionTx(
	_ context.Context,
	options *sql.TxOptions,
) (correctionTransaction, error) {
	starter.options = options
	return starter.tx, nil
}

type fakeCorrectionTransaction struct {
	source     revokedCouponSource
	sourceErr  error
	ledgers    map[uuid.UUID]Ledger
	ledgerErr  error
	appended   []coupon.Entry
	appendErr  error
	committed  bool
	commitErr  error
	rolledBack bool
}

func (*fakeCorrectionTransaction) authorizationQuery() CouponAuthorizationQuery {
	return nil
}

func (tx *fakeCorrectionTransaction) lockRevokedCheckin(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (revokedCouponSource, error) {
	return tx.source, tx.sourceErr
}

func (tx *fakeCorrectionTransaction) listSourceCouponIDsForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) ([]uuid.UUID, error) {
	result := make([]uuid.UUID, 0, len(tx.ledgers))
	for couponID := range tx.ledgers {
		result = append(result, couponID)
	}
	return result, nil
}

func (tx *fakeCorrectionTransaction) getLedgerForUpdate(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	couponID uuid.UUID,
) (Ledger, error) {
	return tx.ledgers[couponID], tx.ledgerErr
}

func (tx *fakeCorrectionTransaction) appendLifecycleEntry(
	_ context.Context,
	entry coupon.Entry,
) (coupon.Entry, error) {
	tx.appended = append(tx.appended, entry)
	return entry, tx.appendErr
}

func (tx *fakeCorrectionTransaction) Commit() error {
	tx.committed = true
	return tx.commitErr
}

func (tx *fakeCorrectionTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}

type fakePendingCorrectionLister struct {
	sources []PendingCorrectionSource
	err     error
	calls   int
	limit   int
}

func (lister *fakePendingCorrectionLister) ListPendingCorrectionSources(
	_ context.Context,
	_ uuid.UUID,
	limit int,
) ([]PendingCorrectionSource, error) {
	lister.calls++
	lister.limit = limit
	return append([]PendingCorrectionSource(nil), lister.sources...),
		lister.err
}
