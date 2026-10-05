package checkinpostgres

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var errVerifierPersistence = errors.New(`verification persistence failure`)

func TestVerifierRecordsValidQRDecisionWithoutPresentedSecret(t *testing.T) {
	t.Parallel()

	fixture := newVerifierFixture(t)
	tx := newFakeVerifyCredentialTransaction(fixture)
	starter := &fakeVerifyCredentialTransactionStarter{tx: tx}
	verifier := &Verifier{
		transactions:   starter,
		operationLocks: &fakeVerifyCredentialOperationLocker{},
		protector:      fixture.protector,
		generationID:   uuid.New(),
		now:            func() time.Time { return fixture.now },
	}

	result, err := verifier.Verify(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Verify() error = %v`, err)
	}
	if starter.options == nil ||
		starter.options.Isolation != sql.LevelSerializable ||
		!tx.committed ||
		tx.rolledBack ||
		result.Duplicate ||
		result.Attempt.Decision != checkin.VerificationDecisionValid ||
		result.Attempt.RequestFingerprint == (checkin.CredentialDigest{}) ||
		result.Attempt.CredentialID == nil ||
		*result.Attempt.CredentialID != fixture.issued.Credential.ID ||
		tx.created.ID != result.Attempt.ID {
		t.Fatalf(`Verify() = %+v, tx = %+v`, result, tx)
	}
	if strings.Contains(
		fmtVerificationAttempt(result.Attempt),
		fixture.command.PresentedValue,
	) {
		t.Fatal(`verification attempt retained presented QR token`)
	}
	if !reflect.DeepEqual(tx.operations, []string{
		`authorize`,
		`idempotency`,
		`generation`,
		`series`,
		`instance`,
		`session`,
		`credential JTI`,
		`registration`,
		`credential lock`,
		`checkin`,
		`create`,
		`commit`,
	}) {
		t.Fatalf(`operations = %#v`, tx.operations)
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

func TestVerifierAcceptsCanonicalBackupCode(t *testing.T) {
	t.Parallel()

	fixture := newVerifierFixture(t)
	fixture.command.PresentedKind = checkin.PresentedCredentialKindBackupCode
	fixture.command.PresentedValue = strings.ToLower(
		strings.ReplaceAll(fixture.issued.BackupCode, `-`, ` `),
	)
	tx := newFakeVerifyCredentialTransaction(fixture)
	verifier := verifierForFixture(fixture, tx)

	result, err := verifier.Verify(context.Background(), fixture.command)
	if err != nil || result.Attempt.Decision != checkin.VerificationDecisionValid {
		t.Fatalf(`Verify(backup) = %+v, %v`, result, err)
	}
	if !operationsContain(tx.operations, `credential backup`) ||
		operationsContain(tx.operations, `credential JTI`) {
		t.Fatalf(`operations = %#v`, tx.operations)
	}
}

func TestVerifierRejectsStaleGenerationBeforeBusinessLocks(t *testing.T) {
	t.Parallel()

	fixture := newVerifierFixture(t)
	tx := newFakeVerifyCredentialTransaction(fixture)
	tx.generationErr = ErrCheckinVerificationGenerationInactive
	verifier := verifierForFixture(fixture, tx)

	_, err := verifier.Verify(context.Background(), fixture.command)
	if !errors.Is(err, ErrCheckinVerificationGenerationInactive) {
		t.Fatalf(`Verify() error = %v`, err)
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

func TestVerifierRecordsUnknownCredentialWithoutIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		kind      checkin.PresentedCredentialKind
		presented string
	}{
		{
			name:      `malformed QR`,
			kind:      checkin.PresentedCredentialKindQRToken,
			presented: `not-a-token`,
		},
		{
			name:      `malformed backup`,
			kind:      checkin.PresentedCredentialKindBackupCode,
			presented: `0000-OOOO`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newVerifierFixture(t)
			fixture.command.PresentedKind = test.kind
			fixture.command.PresentedValue = test.presented
			tx := newFakeVerifyCredentialTransaction(fixture)
			verifier := verifierForFixture(fixture, tx)

			result, err := verifier.Verify(context.Background(), fixture.command)
			if err != nil ||
				result.Attempt.Decision !=
					checkin.VerificationDecisionInvalidCredential ||
				result.Attempt.CredentialID != nil ||
				result.Attempt.CredentialJTI != nil ||
				result.Attempt.RegistrationID != nil ||
				result.Attempt.PrincipalID != nil ||
				result.Attempt.CheckinID != nil {
				t.Fatalf(`Verify(unknown) = %+v, %v`, result, err)
			}
			if operationsContain(tx.operations, `registration`) ||
				operationsContain(tx.operations, `credential lock`) ||
				operationsContain(tx.operations, `checkin`) {
				t.Fatalf(`unknown credential operations = %#v`, tx.operations)
			}
		})
	}
}

func TestVerifierDerivesDecisionFromLockedCurrentFacts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(*testing.T, *verifierFixture, *fakeVerifyCredentialTransaction)
		want    checkin.VerificationDecision
	}{
		{
			name: `wrong Session`,
			prepare: func(
				_ *testing.T,
				fixture *verifierFixture,
				_ *fakeVerifyCredentialTransaction,
			) {
				fixture.command.SessionID = uuid.New()
			},
			want: checkin.VerificationDecisionWrongContext,
		},
		{
			name: `revoked`,
			prepare: func(
				t *testing.T,
				fixture *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				revoked, err := checkin.RevokeCredential(
					fixture.issued.Credential,
					`rotation`,
					fixture.now.Add(-time.Minute),
				)
				if err != nil {
					t.Fatalf(`RevokeCredential() error = %v`, err)
				}
				tx.lookupCredential = revoked
				tx.lockedCredential = revoked
			},
			want: checkin.VerificationDecisionRevoked,
		},
		{
			name: `expired`,
			prepare: func(
				_ *testing.T,
				fixture *verifierFixture,
				_ *fakeVerifyCredentialTransaction,
			) {
				fixture.now = fixture.issued.Credential.ExpiresAt
			},
			want: checkin.VerificationDecisionExpired,
		},
		{
			name: `cancelled Registration`,
			prepare: func(
				_ *testing.T,
				_ *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				tx.registration.ParticipationStatus =
					registration.ParticipationStatusCancelled
			},
			want: checkin.VerificationDecisionRegistrationIneligible,
		},
		{
			name: `cancelled target`,
			prepare: func(
				_ *testing.T,
				_ *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				tx.session.status = activity.SessionStatusCancelled
			},
			want: checkin.VerificationDecisionRegistrationIneligible,
		},
		{
			name: `already checked in`,
			prepare: func(
				t *testing.T,
				fixture *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				existing, err := checkin.New(checkin.NewCommand{
					TenantID:       fixture.command.TenantID,
					RegistrationID: fixture.registration.ID,
					SeriesID:       fixture.command.SeriesID,
					InstanceID:     fixture.command.InstanceID,
					SessionID:      fixture.command.SessionID,
					PrincipalID:    fixture.registration.PrincipalID,
					CheckedInBy:    uuid.New(),
					At:             fixture.now.Add(-time.Minute),
				})
				if err != nil {
					t.Fatalf(`New(Checkin) error = %v`, err)
				}
				tx.existingCheckin = existing
				tx.existingCheckinErr = nil
			},
			want: checkin.VerificationDecisionAlreadyCheckedIn,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newVerifierFixture(t)
			tx := newFakeVerifyCredentialTransaction(fixture)
			test.prepare(t, &fixture, tx)
			verifier := verifierForFixture(fixture, tx)

			result, err := verifier.Verify(context.Background(), fixture.command)
			if err != nil || result.Attempt.Decision != test.want {
				t.Fatalf(
					`Verify() decision = %q, error = %v, want %q`,
					result.Attempt.Decision,
					err,
					test.want,
				)
			}
			if test.want == checkin.VerificationDecisionAlreadyCheckedIn &&
				(result.Attempt.CheckinID == nil ||
					*result.Attempt.CheckinID != tx.existingCheckin.ID) {
				t.Fatalf(`already-checked-in attempt = %+v`, result.Attempt)
			}
		})
	}
}

func TestVerifierReplaysExactFingerprintBeforeCurrentFacts(t *testing.T) {
	t.Parallel()

	fixture := newVerifierFixture(t)
	firstTx := newFakeVerifyCredentialTransaction(fixture)
	verifier := verifierForFixture(fixture, firstTx)
	first, err := verifier.Verify(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Verify(first) error = %v`, err)
	}

	replayTx := newFakeVerifyCredentialTransaction(fixture)
	replayTx.idempotencyAttempt = first.Attempt
	replayTx.idempotencyErr = nil
	replayTx.seriesErr = errors.New(`must not inspect mutable facts`)
	replayVerifier := verifierForFixture(fixture, replayTx)
	replayed, err := replayVerifier.Verify(context.Background(), fixture.command)
	if err != nil ||
		!replayed.Duplicate ||
		replayed.Attempt.ID != first.Attempt.ID ||
		!replayTx.committed ||
		replayTx.rolledBack ||
		!reflect.DeepEqual(replayTx.operations, []string{
			`authorize`,
			`idempotency`,
			`commit`,
		}) {
		t.Fatalf(`Verify(replay) = %+v, tx=%+v, error=%v`, replayed, replayTx, err)
	}
}

func TestVerifierRejectsReusedKeyWithDifferentPresentedValue(t *testing.T) {
	t.Parallel()

	fixture := newVerifierFixture(t)
	firstTx := newFakeVerifyCredentialTransaction(fixture)
	first, err := verifierForFixture(fixture, firstTx).Verify(
		context.Background(),
		fixture.command,
	)
	if err != nil {
		t.Fatalf(`Verify(first) error = %v`, err)
	}

	fixture.command.PresentedValue += `changed`
	replayTx := newFakeVerifyCredentialTransaction(fixture)
	replayTx.idempotencyAttempt = first.Attempt
	replayTx.idempotencyErr = nil
	_, err = verifierForFixture(fixture, replayTx).Verify(
		context.Background(),
		fixture.command,
	)
	if !errors.Is(err, ErrCheckinVerificationIdempotencyConflict) {
		t.Fatalf(`Verify(conflict) error = %v`, err)
	}
	if replayTx.committed ||
		!replayTx.rolledBack ||
		!reflect.DeepEqual(replayTx.operations, []string{
			`authorize`,
			`idempotency`,
			`rollback`,
		}) {
		t.Fatalf(`conflict transaction = %+v`, replayTx)
	}
}

func TestVerifierFailsClosedWithoutPartialAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(*verifierFixture, *fakeVerifyCredentialTransaction)
		want    error
	}{
		{
			name: `authorization denied`,
			prepare: func(
				_ *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				tx.authorizationErr = ErrCheckinOperatorForbidden
			},
			want: ErrCheckinOperatorForbidden,
		},
		{
			name: `target missing`,
			prepare: func(
				_ *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				tx.seriesErr = ErrCheckinVerificationTargetNotFound
			},
			want: ErrCheckinVerificationTargetNotFound,
		},
		{
			name: `credential lookup unavailable`,
			prepare: func(
				_ *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				tx.lookupCredentialErr = errVerifierPersistence
			},
			want: errVerifierPersistence,
		},
		{
			name: `Registration identity drift`,
			prepare: func(
				_ *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				tx.registration.PrincipalID = uuid.New()
			},
			want: ErrCheckinVerificationTransactionConflict,
		},
		{
			name: `credential identity drift`,
			prepare: func(
				_ *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				tx.lockedCredential.CredentialEpoch++
			},
			want: ErrCheckinVerificationTransactionConflict,
		},
		{
			name: `credential issued in the future`,
			prepare: func(
				fixture *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				future := fixture.now.Add(time.Minute)
				tx.lookupCredential.IssuedAt = future
				tx.lockedCredential.IssuedAt = future
			},
			want: ErrCheckinVerificationTransactionConflict,
		},
		{
			name: `attempt insert failure`,
			prepare: func(
				_ *verifierFixture,
				tx *fakeVerifyCredentialTransaction,
			) {
				tx.createErr = errVerifierPersistence
			},
			want: errVerifierPersistence,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newVerifierFixture(t)
			tx := newFakeVerifyCredentialTransaction(fixture)
			test.prepare(&fixture, tx)
			_, err := verifierForFixture(fixture, tx).Verify(
				context.Background(),
				fixture.command,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf(`Verify() error = %v, want %v`, err, test.want)
			}
			if tx.committed || !tx.rolledBack {
				t.Fatalf(`failed transaction = %+v`, tx)
			}
		})
	}
}

func TestVerifierRejectsInvalidInputBeforeTransaction(t *testing.T) {
	t.Parallel()

	fixture := newVerifierFixture(t)
	fixture.command.PresentedValue = ``
	starter := &fakeVerifyCredentialTransactionStarter{}
	locker := &fakeVerifyCredentialOperationLocker{}
	verifier := &Verifier{
		transactions:   starter,
		operationLocks: locker,
		protector:      fixture.protector,
		generationID:   uuid.New(),
		now:            time.Now,
	}
	_, err := verifier.Verify(context.Background(), fixture.command)
	if !errors.Is(err, ErrInvalidVerifyCredentialCommand) {
		t.Fatalf(`Verify() error = %v`, err)
	}
	if starter.calls != 0 || locker.calls != 0 {
		t.Fatalf(`begin calls = %d, lock calls = %d`, starter.calls, locker.calls)
	}
}

func TestVerifierLocksOperationBeforeSerializableTransaction(t *testing.T) {
	t.Parallel()

	fixture := newVerifierFixture(t)
	tx := newFakeVerifyCredentialTransaction(fixture)
	order := make([]string, 0, 3)
	lockedConn := &sql.Conn{}
	starter := &fakeVerifyCredentialTransactionStarter{tx: tx, order: &order}
	locker := &fakeVerifyCredentialOperationLocker{conn: lockedConn, order: &order}
	verifier := &Verifier{
		transactions:   starter,
		operationLocks: locker,
		protector:      fixture.protector,
		generationID:   uuid.New(),
		now:            func() time.Time { return fixture.now },
	}

	if _, err := verifier.Verify(context.Background(), fixture.command); err != nil {
		t.Fatalf(`Verify() error = %v`, err)
	}
	if !reflect.DeepEqual(order, []string{`lock`, `begin`, `release`}) {
		t.Fatalf(`operation order = %#v`, order)
	}
	if locker.tenantID != fixture.command.TenantID ||
		locker.actorID != fixture.command.ActorID ||
		locker.idempotencyKey != fixture.command.IdempotencyKey ||
		locker.releases != 1 {
		t.Fatalf(`operation lock = %+v`, locker)
	}
	if starter.conn != lockedConn {
		t.Fatal("verification transaction did not use the locked connection")
	}
}

func TestClassifyVerifyCredentialTransactionError(t *testing.T) {
	t.Parallel()

	for _, code := range []string{`23505`, `40001`, `40P01`} {
		code := code
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			err := classifyVerifyCredentialTransactionError(
				&pgconn.PgError{Code: code},
			)
			if !errors.Is(err, ErrCheckinVerificationTransactionConflict) {
				t.Fatalf(`classification = %v`, err)
			}
		})
	}
}

func fmtVerificationAttempt(value checkin.VerificationAttempt) string {
	return strings.Join([]string{
		value.ID.String(),
		value.TenantID.String(),
		value.IdempotencyKey,
		string(value.PresentedKind),
		string(value.Decision),
	}, `:`)
}

func operationsContain(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type verifierFixture struct {
	now          time.Time
	protector    *checkin.CredentialProtector
	issued       checkin.IssuedCredential
	registration registration.Registration
	command      VerifyCredentialCommand
}

func newVerifierFixture(t *testing.T) verifierFixture {
	t.Helper()
	now := time.Date(2026, time.September, 23, 5, 0, 0, 0, time.UTC)
	protector, err := checkin.NewCredentialProtector(
		bytes.Repeat([]byte{0x8D}, 32),
		bytes.NewReader(bytes.Repeat([]byte{0x5C}, 96)),
	)
	if err != nil {
		t.Fatalf(`NewCredentialProtector() error = %v`, err)
	}
	confirmedAt := now.Add(-10 * time.Minute)
	registrationValue := registration.Registration{
		ID:                  uuid.New(),
		TenantID:            uuid.New(),
		SeriesID:            uuid.New(),
		InstanceID:          uuid.New(),
		SessionID:           uuid.New(),
		PrincipalID:         uuid.New(),
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      `registration:verification:001`,
		ConfirmedAt:         &confirmedAt,
		Version:             2,
		CreatedAt:           confirmedAt.Add(-time.Minute),
		UpdatedAt:           confirmedAt,
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
	command := VerifyCredentialCommand{
		TenantID:       registrationValue.TenantID,
		SeriesID:       registrationValue.SeriesID,
		InstanceID:     registrationValue.InstanceID,
		SessionID:      registrationValue.SessionID,
		ActorID:        uuid.New(),
		IdentityLinkID: uuid.New(),
		PresentedKind:  checkin.PresentedCredentialKindQRToken,
		PresentedValue: issued.QRToken,
		IdempotencyKey: `verify:service:001`,
	}
	return verifierFixture{
		now:          now,
		protector:    protector,
		issued:       issued,
		registration: registrationValue,
		command:      command,
	}
}

func verifierForFixture(
	fixture verifierFixture,
	tx *fakeVerifyCredentialTransaction,
) *Verifier {
	return &Verifier{
		transactions:   &fakeVerifyCredentialTransactionStarter{tx: tx},
		operationLocks: &fakeVerifyCredentialOperationLocker{},
		protector:      fixture.protector,
		generationID:   uuid.New(),
		now:            func() time.Time { return fixture.now },
	}
}

type fakeVerifyCredentialTransactionStarter struct {
	tx      *fakeVerifyCredentialTransaction
	conn    *sql.Conn
	options *sql.TxOptions
	err     error
	calls   int
	order   *[]string
}

func (starter *fakeVerifyCredentialTransactionStarter) beginVerifyCredentialTx(
	_ context.Context,
	conn *sql.Conn,
	options *sql.TxOptions,
) (verifyCredentialTransaction, error) {
	starter.calls++
	starter.conn = conn
	starter.options = options
	if starter.order != nil {
		*starter.order = append(*starter.order, `begin`)
	}
	if starter.err != nil {
		return nil, starter.err
	}
	return starter.tx, nil
}

type fakeVerifyCredentialOperationLocker struct {
	conn           *sql.Conn
	tenantID       uuid.UUID
	actorID        uuid.UUID
	idempotencyKey string
	err            error
	releaseErr     error
	calls          int
	releases       int
	order          *[]string
}

func (locker *fakeVerifyCredentialOperationLocker) lock(
	_ context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	idempotencyKey string,
) (*sql.Conn, func() error, error) {
	locker.calls++
	locker.tenantID = tenantID
	locker.actorID = actorID
	locker.idempotencyKey = idempotencyKey
	if locker.order != nil {
		*locker.order = append(*locker.order, `lock`)
	}
	if locker.err != nil {
		return nil, nil, locker.err
	}
	return locker.conn, func() error {
		locker.releases++
		if locker.order != nil {
			*locker.order = append(*locker.order, `release`)
		}
		return locker.releaseErr
	}, nil
}

type fakeVerifyCredentialTransaction struct {
	authorization       RecordCheckinAuthorization
	authorizationErr    error
	idempotencyAttempt  checkin.VerificationAttempt
	idempotencyErr      error
	generationErr       error
	seriesStatus        activity.SeriesStatus
	seriesErr           error
	instanceStatus      activity.InstanceStatus
	instanceErr         error
	session             recordCheckinSession
	sessionErr          error
	lookupCredential    checkin.Credential
	lookupCredentialErr error
	registration        registration.Registration
	registrationErr     error
	lockedCredential    checkin.Credential
	lockedCredentialErr error
	existingCheckin     checkin.Checkin
	existingCheckinErr  error
	created             checkin.VerificationAttempt
	createErr           error
	commitErr           error
	committed           bool
	rolledBack          bool
	operations          []string
}

func newFakeVerifyCredentialTransaction(
	fixture verifierFixture,
) *fakeVerifyCredentialTransaction {
	return &fakeVerifyCredentialTransaction{
		idempotencyErr: ErrVerificationAttemptNotFound,
		seriesStatus:   activity.SeriesStatusActive,
		instanceStatus: activity.InstanceStatusPublished,
		session: recordCheckinSession{
			status: activity.SessionStatusPublished,
		},
		lookupCredential:   fixture.issued.Credential,
		registration:       fixture.registration,
		lockedCredential:   fixture.issued.Credential,
		existingCheckinErr: ErrCheckinNotFound,
	}
}

func (tx *fakeVerifyCredentialTransaction) authorizeVerifyCredential(
	_ context.Context,
	request RecordCheckinAuthorization,
) error {
	tx.operations = append(tx.operations, `authorize`)
	tx.authorization = request
	return tx.authorizationErr
}

func (tx *fakeVerifyCredentialTransaction) getVerificationAttemptByIdempotencyKey(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	string,
) (checkin.VerificationAttempt, error) {
	tx.operations = append(tx.operations, `idempotency`)
	return tx.idempotencyAttempt, tx.idempotencyErr
}

func (tx *fakeVerifyCredentialTransaction) lockVerifyGeneration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) error {
	tx.operations = append(tx.operations, `generation`)
	return tx.generationErr
}

func (tx *fakeVerifyCredentialTransaction) lockVerifySeries(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (activity.SeriesStatus, error) {
	tx.operations = append(tx.operations, `series`)
	return tx.seriesStatus, tx.seriesErr
}

func (tx *fakeVerifyCredentialTransaction) lockVerifyInstance(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (activity.InstanceStatus, error) {
	tx.operations = append(tx.operations, `instance`)
	return tx.instanceStatus, tx.instanceErr
}

func (tx *fakeVerifyCredentialTransaction) lockVerifySession(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (recordCheckinSession, error) {
	tx.operations = append(tx.operations, `session`)
	return tx.session, tx.sessionErr
}

func (tx *fakeVerifyCredentialTransaction) getVerificationCredentialByJTI(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.Credential, error) {
	tx.operations = append(tx.operations, `credential JTI`)
	return tx.lookupCredential, tx.lookupCredentialErr
}

func (tx *fakeVerifyCredentialTransaction) getVerificationCredentialByBackupHash(
	context.Context,
	uuid.UUID,
	checkin.CredentialDigest,
) (checkin.Credential, error) {
	tx.operations = append(tx.operations, `credential backup`)
	return tx.lookupCredential, tx.lookupCredentialErr
}

func (tx *fakeVerifyCredentialTransaction) lockVerifyRegistration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (registration.Registration, error) {
	tx.operations = append(tx.operations, `registration`)
	return tx.registration, tx.registrationErr
}

func (tx *fakeVerifyCredentialTransaction) lockVerifyCredential(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.Credential, error) {
	tx.operations = append(tx.operations, `credential lock`)
	return tx.lockedCredential, tx.lockedCredentialErr
}

func (tx *fakeVerifyCredentialTransaction) lockVerifyCheckinByRegistration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.Checkin, error) {
	tx.operations = append(tx.operations, `checkin`)
	return tx.existingCheckin, tx.existingCheckinErr
}

func (tx *fakeVerifyCredentialTransaction) createVerificationAttempt(
	_ context.Context,
	value checkin.VerificationAttempt,
) (checkin.VerificationAttempt, error) {
	tx.operations = append(tx.operations, `create`)
	tx.created = value
	return value, tx.createErr
}

func (tx *fakeVerifyCredentialTransaction) Commit() error {
	tx.operations = append(tx.operations, `commit`)
	if tx.commitErr == nil {
		tx.committed = true
	}
	return tx.commitErr
}

func (tx *fakeVerifyCredentialTransaction) Rollback() error {
	tx.operations = append(tx.operations, `rollback`)
	tx.rolledBack = true
	return nil
}
