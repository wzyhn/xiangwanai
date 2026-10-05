package contributionpostgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestReconcilerCreatesOneEligibleHostContribution(t *testing.T) {
	t.Parallel()

	source := contributionSource(t, false)
	tx := newFakeReconcileTransaction(source)
	starter := &fakeReconcileTransactionStarter{tx: tx}
	reconciler := &Reconciler{
		transactions: starter,
		now: func() time.Time {
			return source.CheckedInEvent.OccurredAt.Add(time.Minute)
		},
	}
	result, err := reconciler.Reconcile(context.Background(), ReconcileCommand{
		TenantID:  source.Checkin.TenantID,
		CheckinID: source.Checkin.ID,
	})
	if err != nil {
		t.Fatalf(`Reconcile() error = %v`, err)
	}
	if !result.SourceEligible || !result.Changed || result.Earned == nil ||
		result.Reversal != nil ||
		result.Earned.CheckinEventID != source.CheckedInEvent.ID ||
		result.Earned.PeopleBindingID != source.PeopleBindingID ||
		result.Earned.RoleBindingID != source.RoleBindingID ||
		len(tx.created) != 1 || !tx.committed || tx.rolledBack ||
		starter.options == nil ||
		starter.options.Isolation != sql.LevelSerializable {
		t.Fatalf(`Reconcile() = %+v tx=%+v starter=%+v`, result, tx, starter)
	}
	history, err := contribution.BuildHistory(tx.created)
	if err != nil || history.ActiveCount != 1 {
		t.Fatalf(`BuildHistory(created) = %+v, %v`, history, err)
	}
}

func TestReconcilerCreatesEarnedAndReversalForAlreadyRevokedCheckin(
	t *testing.T,
) {
	t.Parallel()

	source := contributionSource(t, true)
	tx := newFakeReconcileTransaction(source)
	reconciler := &Reconciler{
		transactions: &fakeReconcileTransactionStarter{tx: tx},
		now: func() time.Time {
			return source.RevokedEvent.OccurredAt.Add(time.Minute)
		},
	}
	result, err := reconciler.Reconcile(context.Background(), ReconcileCommand{
		TenantID:  source.Checkin.TenantID,
		CheckinID: source.Checkin.ID,
	})
	if err != nil {
		t.Fatalf(`Reconcile() error = %v`, err)
	}
	if !result.Changed || result.Earned == nil || result.Reversal == nil ||
		result.Reversal.CheckinEventID != source.RevokedEvent.ID ||
		result.Reversal.ReversalOfEntryID == nil ||
		*result.Reversal.ReversalOfEntryID != result.Earned.ID ||
		len(tx.created) != 2 || !tx.committed {
		t.Fatalf(`Reconcile() = %+v tx=%+v`, result, tx)
	}
	history, err := contribution.BuildHistory(tx.created)
	if err != nil || history.ActiveCount != 0 ||
		len(history.Items) != 1 ||
		history.Items[0].State != contribution.StateReversed {
		t.Fatalf(`BuildHistory(created) = %+v, %v`, history, err)
	}
}

func TestReconcilerReplaysExistingLedgerWithoutDuplicateWrites(t *testing.T) {
	t.Parallel()

	source := contributionSource(t, true)
	earned := earnedForSource(t, source)
	reversal, err := contribution.Reverse(contribution.ReverseCommand{
		Earned:         earned,
		CheckinEventID: source.RevokedEvent.ID,
		OccurredAt:     source.RevokedEvent.OccurredAt,
		RecordedAt:     source.RevokedEvent.OccurredAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`Reverse() error = %v`, err)
	}
	tx := newFakeReconcileTransaction(source)
	tx.earned = earned
	tx.earnedErr = nil
	tx.reversal = reversal
	tx.reversalErr = nil
	reconciler := &Reconciler{
		transactions: &fakeReconcileTransactionStarter{tx: tx},
		now:          time.Now,
	}
	result, err := reconciler.Reconcile(context.Background(), ReconcileCommand{
		TenantID:  source.Checkin.TenantID,
		CheckinID: source.Checkin.ID,
	})
	if err != nil || result.Changed || result.Earned == nil ||
		result.Reversal == nil || len(tx.created) != 0 || !tx.committed {
		t.Fatalf(`Reconcile(replay) = %+v, %v tx=%+v`, result, err, tx)
	}
}

func TestReconcilerDoesNotInventContributionWithoutTrustedHostFacts(
	t *testing.T,
) {
	t.Parallel()

	source := contributionSource(t, false)
	source.Eligible = false
	source.PeopleProfileID = uuid.Nil
	source.PeopleBindingID = uuid.Nil
	source.RoleBindingID = uuid.Nil
	tx := newFakeReconcileTransaction(source)
	reconciler := &Reconciler{
		transactions: &fakeReconcileTransactionStarter{tx: tx},
		now:          time.Now,
	}
	result, err := reconciler.Reconcile(context.Background(), ReconcileCommand{
		TenantID:  source.Checkin.TenantID,
		CheckinID: source.Checkin.ID,
	})
	if err != nil || result.SourceEligible || result.Earned != nil ||
		result.Changed || len(tx.created) != 0 || !tx.committed {
		t.Fatalf(`Reconcile(ineligible) = %+v, %v tx=%+v`, result, err, tx)
	}
}

func TestReconcilerDoesNotReverseContributionFromAnotherCheckin(
	t *testing.T,
) {
	t.Parallel()

	source := contributionSource(t, true)
	earned := earnedForSource(t, source)
	earned.CheckinID = uuid.New()
	earned.CheckinEventID = uuid.New()
	earned.RegistrationID = uuid.New()
	earned.SessionID = uuid.New()
	tx := newFakeReconcileTransaction(source)
	tx.earned = earned
	tx.earnedErr = nil
	reconciler := &Reconciler{
		transactions: &fakeReconcileTransactionStarter{tx: tx},
		now:          time.Now,
	}
	result, err := reconciler.Reconcile(context.Background(), ReconcileCommand{
		TenantID:  source.Checkin.TenantID,
		CheckinID: source.Checkin.ID,
	})
	if err != nil || result.Earned == nil || result.Reversal != nil ||
		result.Changed || tx.reversalCalls != 0 || !tx.committed {
		t.Fatalf(`Reconcile(other source) = %+v, %v tx=%+v`, result, err, tx)
	}
}

func TestReconcilerFailsClosedAndRollsBackInvalidFacts(t *testing.T) {
	t.Parallel()

	source := contributionSource(t, false)
	source.CheckedInEvent.RegistrationID = uuid.New()
	tx := newFakeReconcileTransaction(source)
	reconciler := &Reconciler{
		transactions: &fakeReconcileTransactionStarter{tx: tx},
		now:          time.Now,
	}
	_, err := reconciler.Reconcile(context.Background(), ReconcileCommand{
		TenantID:  source.Checkin.TenantID,
		CheckinID: source.Checkin.ID,
	})
	if !errors.Is(err, ErrContributionFactsConflict) ||
		tx.committed || !tx.rolledBack || tx.earnedCalls != 0 {
		t.Fatalf(`Reconcile(invalid facts) error = %v tx=%+v`, err, tx)
	}
}

func TestReconcilerRejectsInvalidCommandBeforeTransaction(t *testing.T) {
	t.Parallel()

	starter := &fakeReconcileTransactionStarter{}
	reconciler := &Reconciler{transactions: starter, now: time.Now}
	_, err := reconciler.Reconcile(context.Background(), ReconcileCommand{})
	if !errors.Is(err, ErrInvalidReconcileCommand) || starter.calls != 0 {
		t.Fatalf(`Reconcile(invalid) error = %v calls=%d`, err, starter.calls)
	}
}

func TestReconcilerDrainsPostgresDerivedPendingSources(t *testing.T) {
	t.Parallel()

	source := contributionSource(t, false)
	tx := newFakeReconcileTransaction(source)
	pending := &fakePendingSourceLister{sources: []PendingSource{{
		TenantID:  source.Checkin.TenantID,
		CheckinID: source.Checkin.ID,
	}}}
	reconciler := &Reconciler{
		transactions: &fakeReconcileTransactionStarter{tx: tx},
		pending:      pending,
		now: func() time.Time {
			return source.CheckedInEvent.OccurredAt.Add(time.Minute)
		},
	}
	results, err := reconciler.ReconcilePending(
		context.Background(),
		source.Checkin.TenantID,
		25,
	)
	if err != nil || len(results) != 1 || !results[0].Changed ||
		pending.calls != 1 || pending.limit != 25 || !tx.committed {
		t.Fatalf(
			`ReconcilePending() = %+v, %v pending=%+v tx=%+v`,
			results,
			err,
			pending,
			tx,
		)
	}
}

func TestContributionPostgresErrorsAreClassified(t *testing.T) {
	t.Parallel()

	for _, code := range []string{`23503`, `23505`, `23514`, `40001`, `40P01`} {
		if err := classifyContributionWriteError(
			&pgconn.PgError{Code: code},
		); !errors.Is(err, ErrContributionTransactionConflict) {
			t.Fatalf(`classifyContributionWriteError(%s) = %v`, code, err)
		}
	}
	if err := classifyContributionCommitError(
		&pgconn.PgError{Code: `40001`},
	); !errors.Is(err, ErrContributionTransactionConflict) {
		t.Fatalf(`classifyContributionCommitError() = %v`, err)
	}
}

func contributionSource(t *testing.T, revoked bool) reconcileSource {
	t.Helper()
	at := time.Now().UTC().Add(-2 * time.Hour)
	value, err := checkin.New(checkin.NewCommand{
		TenantID:       uuid.New(),
		RegistrationID: uuid.New(),
		SeriesID:       uuid.New(),
		InstanceID:     uuid.New(),
		SessionID:      uuid.New(),
		PrincipalID:    uuid.New(),
		CheckedInBy:    uuid.New(),
		At:             at,
	})
	if err != nil {
		t.Fatalf(`checkin.New() error = %v`, err)
	}
	checkedInEvent, err := checkin.NewEvent(nil, value, `scan:create:contribution`)
	if err != nil {
		t.Fatalf(`checkin.NewEvent() error = %v`, err)
	}
	result := reconcileSource{
		Checkin:         value,
		CheckedInEvent:  checkedInEvent,
		Eligible:        true,
		PeopleProfileID: uuid.New(),
		PeopleBindingID: uuid.New(),
		RoleBindingID:   uuid.New(),
	}
	if !revoked {
		return result
	}
	revokedValue, err := checkin.Revoke(value, checkin.RevokeCommand{
		RevokedBy: uuid.New(),
		Reason:    `operator correction`,
		At:        at.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf(`checkin.Revoke() error = %v`, err)
	}
	revokedEvent, err := checkin.NewEvent(
		&value,
		revokedValue,
		`scan:revoke:contribution`,
	)
	if err != nil {
		t.Fatalf(`checkin.NewEvent(revoked) error = %v`, err)
	}
	result.Checkin = revokedValue
	result.RevokedEvent = &revokedEvent
	return result
}

func earnedForSource(t *testing.T, source reconcileSource) contribution.Entry {
	t.Helper()
	value, err := contribution.Earn(contribution.EarnCommand{
		TenantID:         source.Checkin.TenantID,
		PeopleProfileID:  source.PeopleProfileID,
		PeopleBindingID:  source.PeopleBindingID,
		PrincipalID:      source.Checkin.PrincipalID,
		SeriesID:         source.Checkin.SeriesID,
		InstanceID:       source.Checkin.InstanceID,
		RegistrationID:   source.Checkin.RegistrationID,
		SessionID:        source.Checkin.SessionID,
		CheckinID:        source.Checkin.ID,
		CheckinEventID:   source.CheckedInEvent.ID,
		RoleBindingID:    source.RoleBindingID,
		ContributionType: contribution.TypeHostCheckin,
		OccurredAt:       source.CheckedInEvent.OccurredAt,
		RecordedAt:       source.CheckedInEvent.OccurredAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`contribution.Earn() error = %v`, err)
	}
	return value
}

type fakeReconcileTransactionStarter struct {
	tx      reconcileTransaction
	err     error
	calls   int
	options *sql.TxOptions
}

func (starter *fakeReconcileTransactionStarter) beginReconcileTx(
	_ context.Context,
	options *sql.TxOptions,
) (reconcileTransaction, error) {
	starter.calls++
	starter.options = options
	return starter.tx, starter.err
}

type fakeReconcileTransaction struct {
	source        reconcileSource
	sourceErr     error
	earned        contribution.Entry
	earnedErr     error
	reversal      contribution.Entry
	reversalErr   error
	createErr     error
	commitErr     error
	created       []contribution.Entry
	earnedCalls   int
	reversalCalls int
	committed     bool
	rolledBack    bool
}

type fakePendingSourceLister struct {
	sources []PendingSource
	err     error
	calls   int
	limit   int
}

func (lister *fakePendingSourceLister) ListPendingSources(
	_ context.Context,
	_ uuid.UUID,
	limit int,
) ([]PendingSource, error) {
	lister.calls++
	lister.limit = limit
	return append([]PendingSource(nil), lister.sources...), lister.err
}

func newFakeReconcileTransaction(source reconcileSource) *fakeReconcileTransaction {
	return &fakeReconcileTransaction{
		source:      source,
		earnedErr:   ErrEntryNotFound,
		reversalErr: ErrEntryNotFound,
	}
}

func (tx *fakeReconcileTransaction) lockSource(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (reconcileSource, error) {
	return tx.source, tx.sourceErr
}

func (tx *fakeReconcileTransaction) getEarnedForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
	contribution.Type,
) (contribution.Entry, error) {
	tx.earnedCalls++
	return tx.earned, tx.earnedErr
}

func (tx *fakeReconcileTransaction) getReversalForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (contribution.Entry, error) {
	tx.reversalCalls++
	return tx.reversal, tx.reversalErr
}

func (tx *fakeReconcileTransaction) createEntry(
	_ context.Context,
	value contribution.Entry,
) (contribution.Entry, error) {
	if tx.createErr != nil {
		return contribution.Entry{}, tx.createErr
	}
	tx.created = append(tx.created, value)
	if value.EntryKind == contribution.EntryKindEarned {
		tx.earned = value
		tx.earnedErr = nil
	} else {
		tx.reversal = value
		tx.reversalErr = nil
	}
	return value, nil
}

func (tx *fakeReconcileTransaction) Commit() error {
	tx.committed = true
	return tx.commitErr
}

func (tx *fakeReconcileTransaction) Rollback() error {
	tx.rolledBack = true
	return nil
}
