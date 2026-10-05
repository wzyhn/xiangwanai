package checkinpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var errRevokeTestOutboxUnavailable = errors.New(`outbox unavailable`)

func TestRevokerCommitsRevocationAuditCorrectionAndCounterAtomically(
	t *testing.T,
) {
	t.Parallel()

	fixture := newRevokeFixture(t)
	tx := newFakeRevokeCheckinTransaction(fixture)
	tx.session.status = activity.SessionStatusCancelled
	starter := &fakeRevokeCheckinTransactionStarter{tx: tx}
	revoker := &Revoker{
		transactions: starter,
		generationID: uuid.New(),
		now:          func() time.Time { return fixture.now },
	}

	result, err := revoker.Revoke(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Revoke() error = %v`, err)
	}
	if starter.options == nil ||
		starter.options.Isolation != sql.LevelSerializable ||
		!tx.committed ||
		tx.rolledBack ||
		result.Duplicate ||
		result.Checkin.CheckinStatus != checkin.StatusRevoked ||
		result.Checkin.RevokedBy == nil ||
		*result.Checkin.RevokedBy != fixture.command.ActorID ||
		result.Checkin.RevocationReason == nil ||
		*result.Checkin.RevocationReason != `operator correction` ||
		result.Event.EventType != checkin.EventTypeRevoked ||
		result.Event.CheckinID != fixture.current.ID ||
		result.Event.ActorID != fixture.command.ActorID ||
		result.CorrectionOutboxID == uuid.Nil ||
		tx.decrementCalls != 1 {
		t.Fatalf(`Revoke() = %+v, tx = %+v`, result, tx)
	}
	if !reflect.DeepEqual(tx.operations, []string{
		`generation`,
		`authorize`,
		`idempotency`,
		`series`,
		`instance`,
		`session`,
		`registration`,
		`checkin`,
		`update`,
		`event`,
		`correction create`,
		`decrement`,
		`commit`,
	}) {
		t.Fatalf(`operations = %#v`, tx.operations)
	}
	if tx.authorization != (RevokeCheckinAuthorization{
		TenantID:       fixture.command.TenantID,
		SeriesID:       fixture.command.SeriesID,
		InstanceID:     fixture.command.InstanceID,
		SessionID:      fixture.command.SessionID,
		RegistrationID: fixture.command.RegistrationID,
		CheckinID:      fixture.command.CheckinID,
		ActorID:        fixture.command.ActorID,
	}) {
		t.Fatalf(`authorization = %+v`, tx.authorization)
	}
	if tx.createdCorrection.CheckinID != fixture.current.ID ||
		tx.createdCorrection.CheckinEventID != result.Event.ID ||
		tx.createdCorrection.PrincipalID != fixture.current.PrincipalID ||
		tx.createdCorrection.OutboxStatus != `pending` ||
		tx.createdCorrection.MaxAttempts != checkinCorrectionMaxAttempts {
		t.Fatalf(`correction = %+v`, tx.createdCorrection)
	}
}

func TestRevokerReplaysExactRevocationBeforeMutableFacts(t *testing.T) {
	t.Parallel()

	fixture := newRevokeFixture(t)
	revoked, event, correction := revokedRevokeFixture(t, fixture)
	tx := newFakeRevokeCheckinTransaction(fixture)
	tx.idempotencyEvent = event
	tx.idempotencyErr = nil
	tx.replayed = revoked
	tx.correction = correction
	tx.correctionGetErr = nil
	tx.seriesErr = errors.New(`must not lock mutable hierarchy`)
	revoker := &Revoker{
		transactions: &fakeRevokeCheckinTransactionStarter{tx: tx},
		generationID: uuid.New(),
		now:          func() time.Time { return fixture.now },
	}

	result, err := revoker.Revoke(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Revoke(replay) error = %v`, err)
	}
	if !result.Duplicate ||
		result.Checkin.ID != revoked.ID ||
		result.Event.ID != event.ID ||
		result.CorrectionOutboxID != correction.ID ||
		!tx.committed ||
		tx.rolledBack ||
		tx.decrementCalls != 0 ||
		!reflect.DeepEqual(tx.operations, []string{
			`generation`,
			`authorize`,
			`idempotency`,
			`replayed`,
			`correction get`,
			`commit`,
		}) {
		t.Fatalf(`Revoke(replay) = %+v, tx = %+v`, result, tx)
	}
}

func TestRevokerRejectsStaleGenerationBeforeBusinessLocks(t *testing.T) {
	t.Parallel()

	fixture := newRevokeFixture(t)
	tx := newFakeRevokeCheckinTransaction(fixture)
	tx.generationErr = ErrCheckinRevocationGenerationInactive
	revoker := &Revoker{
		transactions: &fakeRevokeCheckinTransactionStarter{tx: tx},
		generationID: uuid.New(),
		now:          func() time.Time { return fixture.now },
	}

	_, err := revoker.Revoke(context.Background(), fixture.command)
	if !errors.Is(err, ErrCheckinRevocationGenerationInactive) {
		t.Fatalf(`Revoke() error = %v`, err)
	}
	if tx.committed || !tx.rolledBack || !reflect.DeepEqual(tx.operations, []string{
		`generation`,
		`rollback`,
	}) {
		t.Fatalf(`transaction = %+v`, tx)
	}
}

func TestRevokerRejectsStaleGenerationBeforeReceiptReplay(t *testing.T) {
	t.Parallel()
	fixture := newRevokeFixture(t)
	revoked, event, correction := revokedRevokeFixture(t, fixture)
	tx := newFakeRevokeCheckinTransaction(fixture)
	tx.idempotencyEvent, tx.idempotencyErr, tx.replayed, tx.correction, tx.correctionGetErr = event, nil, revoked, correction, nil
	tx.generationErr = ErrCheckinRevocationGenerationInactive
	revoker := &Revoker{transactions: &fakeRevokeCheckinTransactionStarter{tx: tx}, generationID: uuid.New(), now: func() time.Time { return fixture.now }}
	if _, err := revoker.Revoke(context.Background(), fixture.command); !errors.Is(err, ErrCheckinRevocationGenerationInactive) {
		t.Fatalf("replay error %v", err)
	}
	if !reflect.DeepEqual(tx.operations, []string{"generation", "rollback"}) || tx.committed {
		t.Fatalf("unexpected replay operations %v", tx.operations)
	}
}

func TestRevokerFailsClosedWithoutPartialCommit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(*testing.T, *revokeFixture, *fakeRevokeCheckinTransaction)
		want    error
	}{
		{
			name: `not super admin`,
			prepare: func(
				_ *testing.T,
				_ *revokeFixture,
				tx *fakeRevokeCheckinTransaction,
			) {
				tx.authorizationErr = ErrCheckinRevocationForbidden
			},
			want: ErrCheckinRevocationForbidden,
		},
		{
			name: `cross-context Registration`,
			prepare: func(
				_ *testing.T,
				_ *revokeFixture,
				tx *fakeRevokeCheckinTransaction,
			) {
				tx.registration.SessionID = uuid.New()
			},
			want: ErrCheckinRevocationTargetNotFound,
		},
		{
			name: `cross-context Checkin`,
			prepare: func(
				_ *testing.T,
				_ *revokeFixture,
				tx *fakeRevokeCheckinTransaction,
			) {
				tx.current.InstanceID = uuid.New()
			},
			want: ErrCheckinRevocationTargetNotFound,
		},
		{
			name: `already revoked under another intent`,
			prepare: func(
				t *testing.T,
				fixture *revokeFixture,
				tx *fakeRevokeCheckinTransaction,
			) {
				revoked, _, _ := revokedRevokeFixture(t, *fixture)
				tx.current = revoked
			},
			want: ErrCheckinAlreadyRevoked,
		},
		{
			name: `stale expected version`,
			prepare: func(
				_ *testing.T,
				fixture *revokeFixture,
				_ *fakeRevokeCheckinTransaction,
			) {
				fixture.command.ExpectedVersion = 2
			},
			want: ErrCheckinRevocationVersionConflict,
		},
		{
			name: `attendance counter drift`,
			prepare: func(
				_ *testing.T,
				_ *revokeFixture,
				tx *fakeRevokeCheckinTransaction,
			) {
				tx.session.checkedInCount = 0
			},
			want: ErrCheckinRevocationTransactionConflict,
		},
		{
			name: `optimistic update conflict`,
			prepare: func(
				_ *testing.T,
				_ *revokeFixture,
				tx *fakeRevokeCheckinTransaction,
			) {
				tx.updateErr = ErrCheckinVersionConflict
			},
			want: ErrCheckinRevocationVersionConflict,
		},
		{
			name: `correction outbox failure`,
			prepare: func(
				_ *testing.T,
				_ *revokeFixture,
				tx *fakeRevokeCheckinTransaction,
			) {
				tx.correctionCreateErr = errRevokeTestOutboxUnavailable
			},
			want: errRevokeTestOutboxUnavailable,
		},
		{
			name: `counter reversal failure`,
			prepare: func(
				_ *testing.T,
				_ *revokeFixture,
				tx *fakeRevokeCheckinTransaction,
			) {
				tx.decrementErr = ErrCheckinRevocationTransactionConflict
			},
			want: ErrCheckinRevocationTransactionConflict,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newRevokeFixture(t)
			tx := newFakeRevokeCheckinTransaction(fixture)
			test.prepare(t, &fixture, tx)
			revoker := &Revoker{
				transactions: &fakeRevokeCheckinTransactionStarter{tx: tx},
				generationID: uuid.New(),
				now:          func() time.Time { return fixture.now },
			}
			_, err := revoker.Revoke(context.Background(), fixture.command)
			if !errors.Is(err, test.want) {
				t.Fatalf(`Revoke() error = %v, want %v`, err, test.want)
			}
			if tx.committed || !tx.rolledBack {
				t.Fatalf(`failed transaction = %+v`, tx)
			}
		})
	}
}

func TestRevokerRejectsConflictingIdempotencyPayload(t *testing.T) {
	t.Parallel()

	fixture := newRevokeFixture(t)
	revoked, event, correction := revokedRevokeFixture(t, fixture)
	differentReason := `different reason`
	event.Reason = &differentReason
	tx := newFakeRevokeCheckinTransaction(fixture)
	tx.idempotencyEvent = event
	tx.idempotencyErr = nil
	tx.replayed = revoked
	tx.correction = correction
	revoker := &Revoker{
		transactions: &fakeRevokeCheckinTransactionStarter{tx: tx},
		generationID: uuid.New(),
		now:          func() time.Time { return fixture.now },
	}

	_, err := revoker.Revoke(context.Background(), fixture.command)
	if !errors.Is(err, ErrCheckinRevocationIdempotencyConflict) {
		t.Fatalf(`Revoke() error = %v`, err)
	}
	if tx.committed ||
		!tx.rolledBack ||
		!reflect.DeepEqual(tx.operations, []string{
			`generation`,
			`authorize`,
			`idempotency`,
			`rollback`,
		}) {
		t.Fatalf(`transaction = %+v`, tx)
	}
}

func TestRevokerRejectsInvalidCommandBeforeTransaction(t *testing.T) {
	t.Parallel()

	fixture := newRevokeFixture(t)
	fixture.command.Reason = `   `
	starter := &fakeRevokeCheckinTransactionStarter{}
	revoker := &Revoker{
		transactions: starter,
		generationID: uuid.New(),
		now:          time.Now,
	}

	_, err := revoker.Revoke(context.Background(), fixture.command)
	if !errors.Is(err, ErrInvalidRevokeCheckinCommand) {
		t.Fatalf(`Revoke() error = %v`, err)
	}
	if starter.calls != 0 {
		t.Fatalf(`begin calls = %d`, starter.calls)
	}
}

func TestClassifyRevokeCheckinTransactionError(t *testing.T) {
	t.Parallel()

	for _, code := range []string{`23505`, `40001`, `40P01`} {
		code := code
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			err := classifyRevokeCheckinTransactionError(
				&pgconn.PgError{Code: code},
			)
			if !errors.Is(err, ErrCheckinRevocationTransactionConflict) {
				t.Fatalf(`classification = %v`, err)
			}
		})
	}
}

type revokeFixture struct {
	now          time.Time
	command      RevokeCheckinCommand
	registration registration.Registration
	current      checkin.Checkin
}

func newRevokeFixture(t *testing.T) revokeFixture {
	t.Helper()
	now := time.Date(2026, time.September, 22, 5, 0, 0, 0, time.UTC)
	checkedInAt := now.Add(-10 * time.Minute)
	registrationValue := registration.Registration{
		ID:                  uuid.New(),
		TenantID:            uuid.New(),
		SeriesID:            uuid.New(),
		InstanceID:          uuid.New(),
		SessionID:           uuid.New(),
		PrincipalID:         uuid.New(),
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      `registration:revoke-checkin:001`,
		Version:             2,
		CreatedAt:           checkedInAt.Add(-time.Minute),
		UpdatedAt:           checkedInAt.Add(-time.Minute),
	}
	registrationValue.ConfirmedAt = &registrationValue.UpdatedAt
	current, err := checkin.New(checkin.NewCommand{
		TenantID:       registrationValue.TenantID,
		RegistrationID: registrationValue.ID,
		SeriesID:       registrationValue.SeriesID,
		InstanceID:     registrationValue.InstanceID,
		SessionID:      registrationValue.SessionID,
		PrincipalID:    registrationValue.PrincipalID,
		CheckedInBy:    uuid.New(),
		At:             checkedInAt,
	})
	if err != nil {
		t.Fatalf(`New(Checkin) error = %v`, err)
	}
	command := RevokeCheckinCommand{
		TenantID:        current.TenantID,
		SeriesID:        current.SeriesID,
		InstanceID:      current.InstanceID,
		SessionID:       current.SessionID,
		RegistrationID:  current.RegistrationID,
		CheckinID:       current.ID,
		ActorID:         uuid.New(),
		ExpectedVersion: current.Version,
		Reason:          `  operator correction  `,
		IdempotencyKey:  `checkin:revoke:001`,
	}
	return revokeFixture{
		now:          now,
		command:      command,
		registration: registrationValue,
		current:      current,
	}
}

func revokedRevokeFixture(
	t *testing.T,
	fixture revokeFixture,
) (checkin.Checkin, checkin.Event, checkinCorrectionOutbox) {
	t.Helper()
	revoked, err := checkin.Revoke(fixture.current, checkin.RevokeCommand{
		RevokedBy: fixture.command.ActorID,
		Reason:    fixture.command.Reason,
		At:        fixture.now,
	})
	if err != nil {
		t.Fatalf(`Revoke(Checkin) error = %v`, err)
	}
	event, err := checkin.NewEvent(
		&fixture.current,
		revoked,
		fixture.command.IdempotencyKey,
	)
	if err != nil {
		t.Fatalf(`NewEvent() error = %v`, err)
	}
	return revoked, event, newCheckinCorrectionOutbox(revoked, event)
}

type fakeRevokeCheckinTransactionStarter struct {
	tx      *fakeRevokeCheckinTransaction
	options *sql.TxOptions
	err     error
	calls   int
}

func (starter *fakeRevokeCheckinTransactionStarter) beginRevokeCheckinTx(
	_ context.Context,
	options *sql.TxOptions,
) (revokeCheckinTransaction, error) {
	starter.calls++
	starter.options = options
	if starter.err != nil {
		return nil, starter.err
	}
	return starter.tx, nil
}

type fakeRevokeCheckinTransaction struct {
	authorization       RevokeCheckinAuthorization
	authorizationErr    error
	idempotencyEvent    checkin.Event
	idempotencyErr      error
	generationErr       error
	seriesErr           error
	instanceErr         error
	session             recordCheckinSession
	sessionErr          error
	registration        registration.Registration
	registrationErr     error
	current             checkin.Checkin
	currentErr          error
	replayed            checkin.Checkin
	replayedErr         error
	updateErr           error
	eventCreateErr      error
	correction          checkinCorrectionOutbox
	correctionGetErr    error
	createdCorrection   checkinCorrectionOutbox
	correctionCreateErr error
	decrementCalls      int
	decrementErr        error
	commitErr           error
	committed           bool
	rolledBack          bool
	operations          []string
}

func newFakeRevokeCheckinTransaction(
	fixture revokeFixture,
) *fakeRevokeCheckinTransaction {
	return &fakeRevokeCheckinTransaction{
		idempotencyErr: ErrCheckinEventNotFound,
		session: recordCheckinSession{
			status:         activity.SessionStatusPublished,
			confirmedCount: 1,
			checkedInCount: 1,
		},
		registration:     fixture.registration,
		current:          fixture.current,
		replayed:         fixture.current,
		correctionGetErr: ErrCheckinCorrectionOutboxNotFound,
	}
}

func (tx *fakeRevokeCheckinTransaction) authorizeRevokeCheckin(
	_ context.Context,
	request RevokeCheckinAuthorization,
) error {
	tx.operations = append(tx.operations, `authorize`)
	tx.authorization = request
	return tx.authorizationErr
}

func (tx *fakeRevokeCheckinTransaction) getRevocationEventByIdempotencyKey(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	string,
) (checkin.Event, error) {
	tx.operations = append(tx.operations, `idempotency`)
	return tx.idempotencyEvent, tx.idempotencyErr
}

func (tx *fakeRevokeCheckinTransaction) lockRevokeGeneration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) error {
	tx.operations = append(tx.operations, `generation`)
	return tx.generationErr
}

func (tx *fakeRevokeCheckinTransaction) lockRevokeSeries(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) error {
	tx.operations = append(tx.operations, `series`)
	return tx.seriesErr
}

func (tx *fakeRevokeCheckinTransaction) lockRevokeInstance(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) error {
	tx.operations = append(tx.operations, `instance`)
	return tx.instanceErr
}

func (tx *fakeRevokeCheckinTransaction) lockRevokeSession(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (recordCheckinSession, error) {
	tx.operations = append(tx.operations, `session`)
	return tx.session, tx.sessionErr
}

func (tx *fakeRevokeCheckinTransaction) lockRevokeRegistration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (registration.Registration, error) {
	tx.operations = append(tx.operations, `registration`)
	return tx.registration, tx.registrationErr
}

func (tx *fakeRevokeCheckinTransaction) lockRevokeCheckin(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.Checkin, error) {
	tx.operations = append(tx.operations, `checkin`)
	return tx.current, tx.currentErr
}

func (tx *fakeRevokeCheckinTransaction) getReplayedRevokedCheckin(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.Checkin, error) {
	tx.operations = append(tx.operations, `replayed`)
	return tx.replayed, tx.replayedErr
}

func (tx *fakeRevokeCheckinTransaction) updateRevokedCheckin(
	_ context.Context,
	value checkin.Checkin,
	_ int64,
) (checkin.Checkin, error) {
	tx.operations = append(tx.operations, `update`)
	return value, tx.updateErr
}

func (tx *fakeRevokeCheckinTransaction) createRevocationEvent(
	_ context.Context,
	value checkin.Event,
) (checkin.Event, error) {
	tx.operations = append(tx.operations, `event`)
	return value, tx.eventCreateErr
}

func (tx *fakeRevokeCheckinTransaction) createCheckinCorrectionOutbox(
	_ context.Context,
	value checkinCorrectionOutbox,
) (checkinCorrectionOutbox, error) {
	tx.operations = append(tx.operations, `correction create`)
	tx.createdCorrection = value
	return value, tx.correctionCreateErr
}

func (tx *fakeRevokeCheckinTransaction) getCheckinCorrectionOutbox(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkinCorrectionOutbox, error) {
	tx.operations = append(tx.operations, `correction get`)
	return tx.correction, tx.correctionGetErr
}

func (tx *fakeRevokeCheckinTransaction) decrementRevokedCheckinCount(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
	time.Time,
) error {
	tx.operations = append(tx.operations, `decrement`)
	tx.decrementCalls++
	return tx.decrementErr
}

func (tx *fakeRevokeCheckinTransaction) Commit() error {
	tx.operations = append(tx.operations, `commit`)
	if tx.commitErr == nil {
		tx.committed = true
	}
	return tx.commitErr
}

func (tx *fakeRevokeCheckinTransaction) Rollback() error {
	tx.operations = append(tx.operations, `rollback`)
	tx.rolledBack = true
	return nil
}
