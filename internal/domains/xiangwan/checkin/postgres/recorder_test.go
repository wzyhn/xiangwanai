package checkinpostgres

import (
	"bytes"
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
)

func TestRecorderCommitsOneAtomicCheckin(t *testing.T) {
	t.Parallel()

	fixture := newRecordFixture(t)
	tx := newFakeRecordCheckinTransaction(fixture)
	starter := &fakeRecordCheckinTransactionStarter{tx: tx}
	recorder := &Recorder{
		transactions: starter,
		generationID: uuid.New(),
		now:          func() time.Time { return fixture.now },
	}

	result, err := recorder.Record(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Record() error = %v`, err)
	}
	if starter.options == nil ||
		starter.options.Isolation != sql.LevelSerializable ||
		!tx.committed ||
		tx.rolledBack ||
		result.Duplicate ||
		result.Checkin.ID == uuid.Nil ||
		result.Checkin.RegistrationID != fixture.registration.ID ||
		result.Checkin.CheckedInBy != fixture.command.ActorID ||
		!result.Checkin.CheckedInAt.Equal(fixture.now) ||
		result.Event == nil ||
		result.Event.EventType != checkin.EventTypeCheckedIn ||
		result.Event.IdempotencyKey != fixture.command.IdempotencyKey ||
		result.Event.CheckinID != result.Checkin.ID ||
		tx.incrementCalls != 1 {
		t.Fatalf(`Record() = %+v, tx = %+v`, result, tx)
	}
	wantOperations := []string{
		`authorize`,
		`idempotency`,
		`generation`,
		`Series`,
		`Instance`,
		`Session`,
		`Registration`,
		`verification`,
		`credential`,
		`existing`,
		`create Checkin`,
		`create event`,
		`increment`,
		`commit`,
	}
	if !reflect.DeepEqual(tx.operations, wantOperations) {
		t.Fatalf(`operations = %#v, want %#v`, tx.operations, wantOperations)
	}
	if tx.authorization != (RecordCheckinAuthorization{
		TenantID:       fixture.command.TenantID,
		SeriesID:       fixture.command.SeriesID,
		InstanceID:     fixture.command.InstanceID,
		SessionID:      fixture.command.SessionID,
		ActorID:        fixture.command.ActorID,
		IdentityLinkID: fixture.command.IdentityLinkID,
	}) {
		t.Fatalf(`authorization = %+v`, tx.authorization)
	}
}

func TestRecorderReturnsExistingWithoutDuplicateEffects(t *testing.T) {
	t.Parallel()

	fixture := newRecordFixture(t)
	existing, err := checkin.New(checkin.NewCommand{
		TenantID:       fixture.registration.TenantID,
		RegistrationID: fixture.registration.ID,
		SeriesID:       fixture.registration.SeriesID,
		InstanceID:     fixture.registration.InstanceID,
		SessionID:      fixture.registration.SessionID,
		PrincipalID:    fixture.registration.PrincipalID,
		CheckedInBy:    uuid.New(),
		At:             fixture.now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf(`New(Checkin) error = %v`, err)
	}
	tx := newFakeRecordCheckinTransaction(fixture)
	tx.existing = existing
	tx.existingErr = nil
	recorder := &Recorder{
		transactions: &fakeRecordCheckinTransactionStarter{tx: tx},
		generationID: uuid.New(),
		now:          func() time.Time { return fixture.now },
	}

	result, err := recorder.Record(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Record() error = %v`, err)
	}
	if !result.Duplicate ||
		result.Checkin.ID != existing.ID ||
		result.Event != nil ||
		tx.incrementCalls != 0 ||
		tx.createdCheckin.ID != uuid.Nil ||
		!tx.committed {
		t.Fatalf(`Record() = %+v, tx = %+v`, result, tx)
	}
}

func TestRecorderReplaysMatchingIdempotencyReceiptBeforeMutableFacts(t *testing.T) {
	t.Parallel()

	fixture := newRecordFixture(t)
	existing, err := checkin.New(checkin.NewCommand{
		TenantID:       fixture.registration.TenantID,
		RegistrationID: fixture.registration.ID,
		SeriesID:       fixture.registration.SeriesID,
		InstanceID:     fixture.registration.InstanceID,
		SessionID:      fixture.registration.SessionID,
		PrincipalID:    fixture.registration.PrincipalID,
		CheckedInBy:    fixture.command.ActorID,
		At:             fixture.now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf(`New(Checkin) error = %v`, err)
	}
	event, err := checkin.NewEvent(nil, existing, fixture.command.IdempotencyKey)
	if err != nil {
		t.Fatalf(`NewEvent() error = %v`, err)
	}
	tx := newFakeRecordCheckinTransaction(fixture)
	tx.idempotencyEvent = event
	tx.idempotencyErr = nil
	tx.recorded = existing
	tx.recordedErr = nil
	recorder := &Recorder{
		transactions: &fakeRecordCheckinTransactionStarter{tx: tx},
		generationID: uuid.New(),
		now:          func() time.Time { return fixture.now.Add(time.Hour) },
	}

	result, err := recorder.Record(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Record() error = %v`, err)
	}
	if !result.Duplicate ||
		result.Checkin.ID != existing.ID ||
		result.Event == nil || result.Event.ID != event.ID ||
		!reflect.DeepEqual(tx.operations, []string{
			`authorize`,
			`idempotency`,
			`recorded`,
			`verification`,
			`commit`,
		}) {
		t.Fatalf(`Record(replay) = %+v operations=%#v`, result, tx.operations)
	}
}

func TestRecorderRejectsStaleGenerationBeforeBusinessLocks(t *testing.T) {
	t.Parallel()

	fixture := newRecordFixture(t)
	tx := newFakeRecordCheckinTransaction(fixture)
	tx.generationErr = ErrCheckinRecordGenerationInactive
	recorder := &Recorder{
		transactions: &fakeRecordCheckinTransactionStarter{tx: tx},
		generationID: uuid.New(),
		now:          func() time.Time { return fixture.now },
	}

	_, err := recorder.Record(context.Background(), fixture.command)
	if !errors.Is(err, ErrCheckinRecordGenerationInactive) {
		t.Fatalf(`Record() error = %v`, err)
	}
	if tx.committed || !tx.rolledBack || !reflect.DeepEqual(tx.operations, []string{
		`authorize`,
		`idempotency`,
		`generation`,
		`rollback`,
	}) {
		t.Fatalf(`transaction = %+v`, tx)
	}
}

func TestRecorderFailsClosedAcrossAuthorizationAndCurrentFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(*recordFixture, *fakeRecordCheckinTransaction)
		want    error
	}{
		{
			name: `authorization`,
			prepare: func(_ *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.authorizationErr = ErrCheckinOperatorForbidden
			},
			want: ErrCheckinOperatorForbidden,
		},
		{
			name: `archived Series`,
			prepare: func(_ *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.seriesStatus = activity.SeriesStatusArchived
			},
			want: ErrCheckinRecordUnavailable,
		},
		{
			name: `cancelled Instance`,
			prepare: func(_ *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.instanceStatus = activity.InstanceStatusCancelled
			},
			want: ErrCheckinRecordUnavailable,
		},
		{
			name: `ended Session`,
			prepare: func(_ *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.session.status = activity.SessionStatusEnded
			},
			want: ErrCheckinRecordUnavailable,
		},
		{
			name: `cancelled Registration`,
			prepare: func(_ *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.registration.ParticipationStatus = registration.ParticipationStatusCancelled
			},
			want: ErrCheckinRecordUnavailable,
		},
		{
			name: `cross-context verification`,
			prepare: func(_ *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.attempt.RequestedSessionID = uuid.New()
			},
			want: ErrCheckinVerificationRejected,
		},
		{
			name: `credential JTI mismatch`,
			prepare: func(_ *recordFixture, tx *fakeRecordCheckinTransaction) {
				credentialJTI := uuid.New()
				tx.attempt.CredentialJTI = &credentialJTI
			},
			want: ErrCheckinVerificationRejected,
		},
		{
			name: `verification predates credential`,
			prepare: func(_ *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.attempt.OccurredAt = tx.credential.IssuedAt.Add(-time.Second)
				tx.attempt.CreatedAt = tx.attempt.OccurredAt
			},
			want: ErrCheckinVerificationRejected,
		},
		{
			name: `verification is in the future`,
			prepare: func(fixture *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.attempt.OccurredAt = fixture.now.Add(time.Second)
				tx.attempt.CreatedAt = tx.attempt.OccurredAt
			},
			want: ErrCheckinVerificationRejected,
		},
		{
			name: `revoked credential`,
			prepare: func(fixture *recordFixture, tx *fakeRecordCheckinTransaction) {
				revokedAt := fixture.now.Add(-time.Minute)
				reason := `rotation`
				tx.credential.CredentialStatus = checkin.CredentialStatusRevoked
				tx.credential.RevokedAt = &revokedAt
				tx.credential.RevocationReason = &reason
			},
			want: ErrCheckinVerificationRejected,
		},
		{
			name: `expired credential`,
			prepare: func(fixture *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.credential.ExpiresAt = fixture.now
			},
			want: ErrCheckinVerificationRejected,
		},
		{
			name: `counter drift`,
			prepare: func(_ *recordFixture, tx *fakeRecordCheckinTransaction) {
				tx.session.checkedInCount = tx.session.confirmedCount
			},
			want: ErrCheckinRecordTransactionConflict,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newRecordFixture(t)
			tx := newFakeRecordCheckinTransaction(fixture)
			test.prepare(&fixture, tx)
			recorder := &Recorder{
				transactions: &fakeRecordCheckinTransactionStarter{tx: tx},
				generationID: uuid.New(),
				now:          func() time.Time { return fixture.now },
			}
			_, err := recorder.Record(context.Background(), fixture.command)
			if !errors.Is(err, test.want) {
				t.Fatalf(`Record() error = %v, want %v`, err, test.want)
			}
			if tx.committed || !tx.rolledBack || tx.incrementCalls != 0 {
				t.Fatalf(`failed transaction = %+v`, tx)
			}
		})
	}
}

func TestRecorderRejectsConflictingIdempotencyReceipt(t *testing.T) {
	t.Parallel()

	fixture := newRecordFixture(t)
	existing, err := checkin.New(checkin.NewCommand{
		TenantID:       fixture.command.TenantID,
		RegistrationID: uuid.New(),
		SeriesID:       fixture.command.SeriesID,
		InstanceID:     fixture.command.InstanceID,
		SessionID:      fixture.command.SessionID,
		PrincipalID:    fixture.registration.PrincipalID,
		CheckedInBy:    fixture.command.ActorID,
		At:             fixture.now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf(`New(Checkin) error = %v`, err)
	}
	event, err := checkin.NewEvent(nil, existing, fixture.command.IdempotencyKey)
	if err != nil {
		t.Fatalf(`NewEvent() error = %v`, err)
	}
	tx := newFakeRecordCheckinTransaction(fixture)
	tx.idempotencyEvent = event
	tx.idempotencyErr = nil
	recorder := &Recorder{
		transactions: &fakeRecordCheckinTransactionStarter{tx: tx},
		generationID: uuid.New(),
		now:          func() time.Time { return fixture.now },
	}
	_, err = recorder.Record(context.Background(), fixture.command)
	if !errors.Is(err, ErrCheckinRecordIdempotencyConflict) {
		t.Fatalf(`Record() error = %v`, err)
	}
	if tx.committed || !tx.rolledBack {
		t.Fatalf(`transaction = %+v`, tx)
	}
}

func TestRecorderRejectsInvalidCommandBeforeOpeningTransaction(t *testing.T) {
	t.Parallel()

	fixture := newRecordFixture(t)
	fixture.command.IdempotencyKey = `contains spaces`
	starter := &fakeRecordCheckinTransactionStarter{}
	recorder := &Recorder{
		transactions: starter,
		generationID: uuid.New(),
		now:          time.Now,
	}
	_, err := recorder.Record(context.Background(), fixture.command)
	if !errors.Is(err, ErrInvalidRecordCheckinCommand) {
		t.Fatalf(`Record() error = %v`, err)
	}
	if starter.calls != 0 {
		t.Fatalf(`begin calls = %d`, starter.calls)
	}
}

type recordFixture struct {
	now          time.Time
	command      RecordCheckinCommand
	registration registration.Registration
	credential   checkin.Credential
	attempt      checkin.VerificationAttempt
}

func newRecordFixture(t *testing.T) recordFixture {
	t.Helper()
	now := time.Date(2026, time.September, 21, 5, 0, 0, 0, time.UTC)
	confirmedAt := now.Add(-10 * time.Minute)
	registrationValue := registration.Registration{
		ID:                  uuid.New(),
		TenantID:            uuid.New(),
		SeriesID:            uuid.New(),
		InstanceID:          uuid.New(),
		SessionID:           uuid.New(),
		PrincipalID:         uuid.New(),
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      `registration:checkin:001`,
		ConfirmedAt:         &confirmedAt,
		Version:             2,
		CreatedAt:           confirmedAt.Add(-time.Minute),
		UpdatedAt:           confirmedAt,
	}
	protector, err := checkin.NewCredentialProtector(
		bytes.Repeat([]byte{0x7D}, 32),
		bytes.NewReader(bytes.Repeat([]byte{0x4C}, 96)),
	)
	if err != nil {
		t.Fatalf(`NewCredentialProtector() error = %v`, err)
	}
	issued, err := protector.Issue(checkin.IssueCredentialCommand{
		TenantID:       registrationValue.TenantID,
		RegistrationID: registrationValue.ID,
		SeriesID:       registrationValue.SeriesID,
		InstanceID:     registrationValue.InstanceID,
		SessionID:      registrationValue.SessionID,
		PrincipalID:    registrationValue.PrincipalID,
		Epoch:          1,
		TTL:            10 * time.Minute,
		At:             now.Add(-5 * time.Minute),
	})
	if err != nil {
		t.Fatalf(`Issue() error = %v`, err)
	}
	actorID := uuid.New()
	attempt, err := checkin.NewVerificationAttempt(
		checkin.NewVerificationAttemptCommand{
			TenantID:             registrationValue.TenantID,
			RequestedSeriesID:    registrationValue.SeriesID,
			RequestedInstanceID:  registrationValue.InstanceID,
			RequestedSessionID:   registrationValue.SessionID,
			ActorID:              actorID,
			PresentedKind:        checkin.PresentedCredentialKindQRToken,
			IdempotencyKey:       `verify:checkin:001`,
			RequestFingerprint:   checkin.CredentialDigest{0xC1},
			CredentialMatched:    true,
			RegistrationEligible: true,
			Credential:           &issued.Credential,
			At:                   now.Add(-time.Minute),
		},
	)
	if err != nil {
		t.Fatalf(`NewVerificationAttempt() error = %v`, err)
	}
	command := RecordCheckinCommand{
		TenantID:              registrationValue.TenantID,
		SeriesID:              registrationValue.SeriesID,
		InstanceID:            registrationValue.InstanceID,
		SessionID:             registrationValue.SessionID,
		RegistrationID:        registrationValue.ID,
		CredentialID:          issued.Credential.ID,
		VerificationAttemptID: attempt.ID,
		ActorID:               actorID,
		IdentityLinkID:        uuid.New(),
		IdempotencyKey:        `checkin:record:001`,
	}
	return recordFixture{
		now:          now,
		command:      command,
		registration: registrationValue,
		credential:   issued.Credential,
		attempt:      attempt,
	}
}

type fakeRecordCheckinTransactionStarter struct {
	tx      *fakeRecordCheckinTransaction
	options *sql.TxOptions
	err     error
	calls   int
}

func (starter *fakeRecordCheckinTransactionStarter) beginRecordCheckinTx(
	_ context.Context,
	options *sql.TxOptions,
) (recordCheckinTransaction, error) {
	starter.calls++
	starter.options = options
	if starter.err != nil {
		return nil, starter.err
	}
	return starter.tx, nil
}

type fakeRecordCheckinTransaction struct {
	authorization    RecordCheckinAuthorization
	authorizationErr error
	idempotencyEvent checkin.Event
	idempotencyErr   error
	generationErr    error
	seriesStatus     activity.SeriesStatus
	instanceStatus   activity.InstanceStatus
	session          recordCheckinSession
	registration     registration.Registration
	attempt          checkin.VerificationAttempt
	credential       checkin.Credential
	existing         checkin.Checkin
	existingErr      error
	recorded         checkin.Checkin
	recordedErr      error
	createdCheckin   checkin.Checkin
	createdEvent     checkin.Event
	incrementCalls   int
	incrementErr     error
	commitErr        error
	committed        bool
	rolledBack       bool
	operations       []string
}

func newFakeRecordCheckinTransaction(
	fixture recordFixture,
) *fakeRecordCheckinTransaction {
	return &fakeRecordCheckinTransaction{
		idempotencyErr: ErrCheckinEventNotFound,
		seriesStatus:   activity.SeriesStatusActive,
		instanceStatus: activity.InstanceStatusPublished,
		session: recordCheckinSession{
			status:         activity.SessionStatusPublished,
			confirmedCount: 2,
		},
		registration: fixture.registration,
		attempt:      fixture.attempt,
		credential:   fixture.credential,
		existingErr:  ErrCheckinNotFound,
		recordedErr:  ErrCheckinNotFound,
	}
}

func (tx *fakeRecordCheckinTransaction) authorizeRecordCheckin(
	_ context.Context,
	request RecordCheckinAuthorization,
) error {
	tx.operations = append(tx.operations, `authorize`)
	tx.authorization = request
	return tx.authorizationErr
}

func (tx *fakeRecordCheckinTransaction) getCheckinEventByIdempotencyKey(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	string,
) (checkin.Event, error) {
	tx.operations = append(tx.operations, `idempotency`)
	return tx.idempotencyEvent, tx.idempotencyErr
}

func (tx *fakeRecordCheckinTransaction) lockRecordGeneration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) error {
	tx.operations = append(tx.operations, `generation`)
	return tx.generationErr
}

func (tx *fakeRecordCheckinTransaction) lockRecordSeries(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (activity.SeriesStatus, error) {
	tx.operations = append(tx.operations, `Series`)
	return tx.seriesStatus, nil
}

func (tx *fakeRecordCheckinTransaction) lockRecordInstance(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (activity.InstanceStatus, error) {
	tx.operations = append(tx.operations, `Instance`)
	return tx.instanceStatus, nil
}

func (tx *fakeRecordCheckinTransaction) lockRecordSession(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (recordCheckinSession, error) {
	tx.operations = append(tx.operations, `Session`)
	return tx.session, nil
}

func (tx *fakeRecordCheckinTransaction) lockRecordRegistration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (registration.Registration, error) {
	tx.operations = append(tx.operations, `Registration`)
	return tx.registration, nil
}

func (tx *fakeRecordCheckinTransaction) getRecordVerificationAttempt(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.VerificationAttempt, error) {
	tx.operations = append(tx.operations, `verification`)
	return tx.attempt, nil
}

func (tx *fakeRecordCheckinTransaction) lockRecordCredential(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.Credential, error) {
	tx.operations = append(tx.operations, `credential`)
	return tx.credential, nil
}

func (tx *fakeRecordCheckinTransaction) lockCheckinByRegistration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.Checkin, error) {
	tx.operations = append(tx.operations, `existing`)
	return tx.existing, tx.existingErr
}

func (tx *fakeRecordCheckinTransaction) lockRecordedCheckin(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.Checkin, error) {
	tx.operations = append(tx.operations, `recorded`)
	return tx.recorded, tx.recordedErr
}

func (tx *fakeRecordCheckinTransaction) createRecordedCheckin(
	_ context.Context,
	value checkin.Checkin,
) (checkin.Checkin, error) {
	tx.operations = append(tx.operations, `create Checkin`)
	tx.createdCheckin = value
	return value, nil
}

func (tx *fakeRecordCheckinTransaction) createRecordedCheckinEvent(
	_ context.Context,
	value checkin.Event,
) (checkin.Event, error) {
	tx.operations = append(tx.operations, `create event`)
	tx.createdEvent = value
	return value, nil
}

func (tx *fakeRecordCheckinTransaction) incrementRecordedCheckinCount(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	time.Time,
) error {
	tx.operations = append(tx.operations, `increment`)
	tx.incrementCalls++
	return tx.incrementErr
}

func (tx *fakeRecordCheckinTransaction) Commit() error {
	tx.operations = append(tx.operations, `commit`)
	if tx.commitErr == nil {
		tx.committed = true
	}
	return tx.commitErr
}

func (tx *fakeRecordCheckinTransaction) Rollback() error {
	tx.operations = append(tx.operations, `rollback`)
	tx.rolledBack = true
	return nil
}
