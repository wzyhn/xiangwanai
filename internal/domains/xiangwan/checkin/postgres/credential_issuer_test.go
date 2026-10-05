package checkinpostgres

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var errCredentialIssuancePersistence = errors.New(`credential issuance persistence failure`)

func TestCredentialIssuerCreatesScopedOneTimeSecrets(t *testing.T) {
	t.Parallel()

	fixture := newCredentialIssuerFixture(t)
	tx := newFakeIssueCredentialTransaction(fixture)
	starter := &fakeIssueCredentialTransactionStarter{tx: tx}
	issuer := credentialIssuerForFixture(fixture, starter)

	issued, err := issuer.Issue(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Issue() error = %v`, err)
	}
	if checkin.ValidateCredential(issued.Credential) != nil ||
		issued.Credential.CredentialEpoch != 1 ||
		issued.Credential.TenantID != fixture.registration.TenantID ||
		issued.Credential.RegistrationID != fixture.registration.ID ||
		issued.Credential.SeriesID != fixture.registration.SeriesID ||
		issued.Credential.InstanceID != fixture.registration.InstanceID ||
		issued.Credential.SessionID != fixture.registration.SessionID ||
		issued.Credential.PrincipalID != fixture.registration.PrincipalID ||
		issued.Credential.ExpiresAt.Sub(issued.Credential.IssuedAt) !=
			fixture.command.TTL ||
		issued.QRToken == `` ||
		issued.BackupCode == `` ||
		!fixture.protector.MatchesQRToken(
			issued.Credential,
			issued.QRToken,
		) ||
		!fixture.protector.MatchesBackupCode(
			issued.Credential,
			issued.BackupCode,
		) {
		t.Fatalf(`Issue() returned invalid credential metadata`)
	}
	if starter.options == nil ||
		starter.options.Isolation != sql.LevelSerializable ||
		!tx.committed ||
		tx.rolledBack ||
		!sameCredential(tx.created, issued.Credential) ||
		!reflect.DeepEqual(tx.operations, []string{
			`registration resolve`,
			`generation`,
			`series`,
			`instance`,
			`session`,
			`registration lock`,
			`database time`,
			`latest credential`,
			`create credential`,
			`commit`,
		}) {
		t.Fatalf(
			`transaction options=%+v operations=%#v committed=%v rollback=%v`,
			starter.options,
			tx.operations,
			tx.committed,
			tx.rolledBack,
		)
	}
	persistenceView := fmt.Sprintf(`%+v`, tx.created)
	if strings.Contains(persistenceView, issued.QRToken) ||
		strings.Contains(persistenceView, issued.BackupCode) {
		t.Fatal(`persistence boundary captured presented plaintext`)
	}
}

func TestCredentialIssuerRotatesActiveCredentialBeforeCreatingNextEpoch(
	t *testing.T,
) {
	t.Parallel()

	fixture := newCredentialIssuerFixture(t)
	previous, err := fixture.protector.Issue(checkin.IssueCredentialCommand{
		TenantID:       fixture.registration.TenantID,
		RegistrationID: fixture.registration.ID,
		SeriesID:       fixture.registration.SeriesID,
		InstanceID:     fixture.registration.InstanceID,
		SessionID:      fixture.registration.SessionID,
		PrincipalID:    fixture.registration.PrincipalID,
		Epoch:          7,
		TTL:            5 * time.Minute,
		At:             fixture.now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf(`Issue(previous) error = %v`, err)
	}
	tx := newFakeIssueCredentialTransaction(fixture)
	tx.latest = previous.Credential
	tx.latestErr = nil

	issued, err := credentialIssuerForFixture(
		fixture,
		&fakeIssueCredentialTransactionStarter{tx: tx},
	).Issue(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Issue(rotation) error = %v`, err)
	}
	if issued.Credential.CredentialEpoch != 8 ||
		tx.revoked.ID != previous.Credential.ID ||
		tx.revoked.CredentialStatus != checkin.CredentialStatusRevoked ||
		tx.revoked.RevocationReason == nil ||
		*tx.revoked.RevocationReason != credentialRotationReason ||
		tx.revokedExpectedVersion != previous.Credential.Version ||
		issued.Credential.ID == previous.Credential.ID ||
		issued.Credential.CredentialJTI ==
			previous.Credential.CredentialJTI ||
		!reflect.DeepEqual(tx.operations, []string{
			`registration resolve`,
			`generation`,
			`series`,
			`instance`,
			`session`,
			`registration lock`,
			`database time`,
			`latest credential`,
			`revoke credential`,
			`create credential`,
			`commit`,
		}) {
		t.Fatalf(`rotation issued epoch=%d operations=%#v`,
			issued.Credential.CredentialEpoch,
			tx.operations,
		)
	}
}

func TestCredentialIssuerThrottlesRapidCredentialRotation(t *testing.T) {
	t.Parallel()

	fixture := newCredentialIssuerFixture(t)
	tx := newFakeIssueCredentialTransaction(fixture)
	tx.latest = activeCredentialForIssuance(t, &fixture, 4)
	tx.latest.IssuedAt = fixture.now.Add(-4 * time.Second)
	tx.latest.CreatedAt = tx.latest.IssuedAt
	tx.latest.UpdatedAt = tx.latest.IssuedAt
	tx.latest.ExpiresAt = fixture.now.Add(5 * time.Minute)
	tx.latestErr = nil

	_, err := credentialIssuerForFixture(
		fixture,
		&fakeIssueCredentialTransactionStarter{tx: tx},
	).Issue(context.Background(), fixture.command)
	if !errors.Is(err, ErrRegistrationCredentialRateLimited) ||
		operationsContain(tx.operations, `revoke credential`) ||
		operationsContain(tx.operations, `create credential`) || tx.committed {
		t.Fatalf("Issue(rapid rotation) error=%v operations=%#v", err, tx.operations)
	}
}

func TestCredentialIssuerFencesStaleGenerationBeforeBusinessLocks(t *testing.T) {
	t.Parallel()

	fixture := newCredentialIssuerFixture(t)
	tx := newFakeIssueCredentialTransaction(fixture)
	tx.generationErr = ErrRegistrationCredentialGenerationInactive

	_, err := credentialIssuerForFixture(
		fixture,
		&fakeIssueCredentialTransactionStarter{tx: tx},
	).Issue(context.Background(), fixture.command)
	if !errors.Is(err, ErrRegistrationCredentialGenerationInactive) ||
		operationsContain(tx.operations, `series`) ||
		operationsContain(tx.operations, `create credential`) || tx.committed {
		t.Fatalf("Issue(stale generation) error=%v operations=%#v", err, tx.operations)
	}
}

func TestCredentialIssuerUsesNextEpochAfterTerminalCredential(t *testing.T) {
	t.Parallel()

	fixture := newCredentialIssuerFixture(t)
	previous, err := fixture.protector.Issue(checkin.IssueCredentialCommand{
		TenantID:       fixture.registration.TenantID,
		RegistrationID: fixture.registration.ID,
		SeriesID:       fixture.registration.SeriesID,
		InstanceID:     fixture.registration.InstanceID,
		SessionID:      fixture.registration.SessionID,
		PrincipalID:    fixture.registration.PrincipalID,
		Epoch:          2,
		TTL:            5 * time.Minute,
		At:             fixture.now.Add(-2 * time.Minute),
	})
	if err != nil {
		t.Fatalf(`Issue(previous) error = %v`, err)
	}
	terminal, err := checkin.RevokeCredential(
		previous.Credential,
		`registration cancelled`,
		fixture.now.Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf(`RevokeCredential() error = %v`, err)
	}
	tx := newFakeIssueCredentialTransaction(fixture)
	tx.latest = terminal
	tx.latestErr = nil

	issued, err := credentialIssuerForFixture(
		fixture,
		&fakeIssueCredentialTransactionStarter{tx: tx},
	).Issue(context.Background(), fixture.command)
	if err != nil || issued.Credential.CredentialEpoch != 3 {
		t.Fatalf(`Issue(after terminal) epoch=%d error=%v`,
			issued.Credential.CredentialEpoch,
			err,
		)
	}
	if operationsContain(tx.operations, `revoke credential`) {
		t.Fatalf(`terminal credential was revoked again: %#v`, tx.operations)
	}
}

func TestCredentialIssuerBoundsExpiryBySessionEnd(t *testing.T) {
	t.Parallel()

	fixture := newCredentialIssuerFixture(t)
	tx := newFakeIssueCredentialTransaction(fixture)
	tx.session.endAt = fixture.now.Add(90 * time.Second)
	issued, err := credentialIssuerForFixture(
		fixture,
		&fakeIssueCredentialTransactionStarter{tx: tx},
	).Issue(context.Background(), fixture.command)
	if err != nil {
		t.Fatalf(`Issue() error = %v`, err)
	}
	if !issued.Credential.ExpiresAt.Equal(tx.session.endAt) {
		t.Fatalf(
			`expires_at = %s, want Session end %s`,
			issued.Credential.ExpiresAt,
			tx.session.endAt,
		)
	}
}

func TestCredentialIssuerRejectsUnavailableRegistrationOrTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(*credentialIssuerFixture, *fakeIssueCredentialTransaction)
	}{
		{
			name: `Registration missing`,
			prepare: func(
				_ *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.resolvedErr = registrationpostgres.ErrRegistrationNotFound
			},
		},
		{
			name: `another principal`,
			prepare: func(
				fixture *credentialIssuerFixture,
				_ *fakeIssueCredentialTransaction,
			) {
				fixture.command.PrincipalID = uuid.New()
			},
		},
		{
			name: `pending Registration`,
			prepare: func(
				_ *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.current.ParticipationStatus =
					registration.ParticipationStatusPendingPayment
				tx.current.ConfirmedAt = nil
			},
		},
		{
			name: `inactive Series`,
			prepare: func(
				_ *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.seriesStatus = activity.SeriesStatusArchived
			},
		},
		{
			name: `cancelled Instance`,
			prepare: func(
				_ *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.instanceStatus = activity.InstanceStatusCancelled
			},
		},
		{
			name: `cancelled Session`,
			prepare: func(
				_ *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.session.status = activity.SessionStatusCancelled
			},
		},
		{
			name: `ended by authoritative time`,
			prepare: func(
				fixture *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.session.endAt = fixture.now
			},
		},
		{
			name: `confirmation in future`,
			prepare: func(
				fixture *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				confirmedAt := fixture.now.Add(time.Minute)
				tx.current.ConfirmedAt = &confirmedAt
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCredentialIssuerFixture(t)
			tx := newFakeIssueCredentialTransaction(fixture)
			test.prepare(&fixture, tx)
			_, err := credentialIssuerForFixture(
				fixture,
				&fakeIssueCredentialTransactionStarter{tx: tx},
			).Issue(context.Background(), fixture.command)
			if !errors.Is(err, ErrRegistrationCredentialUnavailable) {
				t.Fatalf(`Issue() error = %v`, err)
			}
			if tx.committed || !tx.rolledBack ||
				operationsContain(tx.operations, `create credential`) {
				t.Fatalf(`failed transaction = %+v`, tx)
			}
		})
	}
}

func TestCredentialIssuerFailsClosedOnFactOrWriteConflict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(*credentialIssuerFixture, *fakeIssueCredentialTransaction)
		want    error
	}{
		{
			name: `Registration identity drift`,
			prepare: func(
				_ *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.current.SessionID = uuid.New()
			},
			want: ErrRegistrationCredentialTransactionConflict,
		},
		{
			name: `latest credential belongs to another Registration`,
			prepare: func(
				fixture *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.latest = activeCredentialForIssuance(t, fixture, 1)
				tx.latest.RegistrationID = uuid.New()
				tx.latestErr = nil
			},
			want: ErrRegistrationCredentialTransactionConflict,
		},
		{
			name: `epoch exhausted`,
			prepare: func(
				fixture *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.latest = activeCredentialForIssuance(
					t,
					fixture,
					math.MaxInt64,
				)
				tx.latestErr = nil
			},
			want: ErrRegistrationCredentialTransactionConflict,
		},
		{
			name: `credential clock ahead`,
			prepare: func(
				fixture *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.latest = activeCredentialForIssuance(t, fixture, 1)
				tx.latest.UpdatedAt = fixture.now.Add(time.Minute)
				tx.latest.IssuedAt = tx.latest.UpdatedAt
				tx.latest.CreatedAt = tx.latest.UpdatedAt
				tx.latest.ExpiresAt = tx.latest.UpdatedAt.Add(time.Minute)
				tx.latestErr = nil
			},
			want: ErrRegistrationCredentialTransactionConflict,
		},
		{
			name: `revoke conflict`,
			prepare: func(
				fixture *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.latest = activeCredentialForIssuance(t, fixture, 1)
				tx.latestErr = nil
				tx.revokeErr = ErrCredentialVersionConflict
			},
			want: ErrRegistrationCredentialTransactionConflict,
		},
		{
			name: `create conflict`,
			prepare: func(
				_ *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.createErr = &pgconn.PgError{Code: `23505`}
			},
			want: ErrRegistrationCredentialTransactionConflict,
		},
		{
			name: `persistence failure`,
			prepare: func(
				_ *credentialIssuerFixture,
				tx *fakeIssueCredentialTransaction,
			) {
				tx.createErr = errCredentialIssuancePersistence
			},
			want: errCredentialIssuancePersistence,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCredentialIssuerFixture(t)
			tx := newFakeIssueCredentialTransaction(fixture)
			test.prepare(&fixture, tx)
			_, err := credentialIssuerForFixture(
				fixture,
				&fakeIssueCredentialTransactionStarter{tx: tx},
			).Issue(context.Background(), fixture.command)
			if !errors.Is(err, test.want) {
				t.Fatalf(`Issue() error = %v, want %v`, err, test.want)
			}
			if tx.committed || !tx.rolledBack {
				t.Fatalf(`failed transaction = %+v`, tx)
			}
		})
	}
}

func TestCredentialIssuerRollsBackIfSecretGenerationFails(t *testing.T) {
	t.Parallel()

	fixture := newCredentialIssuerFixture(t)
	protector, err := checkin.NewCredentialProtector(
		bytes.Repeat([]byte{0x8A}, 32),
		errorReader{},
	)
	if err != nil {
		t.Fatalf(`NewCredentialProtector() error = %v`, err)
	}
	tx := newFakeIssueCredentialTransaction(fixture)
	issuer := credentialIssuerForFixture(
		fixture,
		&fakeIssueCredentialTransactionStarter{tx: tx},
	)
	issuer.protector = protector

	_, err = issuer.Issue(context.Background(), fixture.command)
	if !errors.Is(err, checkin.ErrCredentialEntropy) ||
		tx.committed ||
		!tx.rolledBack ||
		operationsContain(tx.operations, `create credential`) {
		t.Fatalf(`Issue(entropy) error=%v transaction=%+v`, err, tx)
	}
}

func TestCredentialIssuerValidatesBeforeTransaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		prepare func(*credentialIssuerFixture, *CredentialIssuer)
	}{
		{
			name: `tenant`,
			prepare: func(
				fixture *credentialIssuerFixture,
				_ *CredentialIssuer,
			) {
				fixture.command.TenantID = uuid.Nil
			},
		},
		{
			name: `Registration`,
			prepare: func(
				fixture *credentialIssuerFixture,
				_ *CredentialIssuer,
			) {
				fixture.command.RegistrationID = uuid.Nil
			},
		},
		{
			name: `principal`,
			prepare: func(
				fixture *credentialIssuerFixture,
				_ *CredentialIssuer,
			) {
				fixture.command.PrincipalID = uuid.Nil
			},
		},
		{
			name: `TTL`,
			prepare: func(
				fixture *credentialIssuerFixture,
				_ *CredentialIssuer,
			) {
				fixture.command.TTL = checkin.MaxCredentialTTL + time.Second
			},
		},
		{
			name: `protector`,
			prepare: func(
				_ *credentialIssuerFixture,
				issuer *CredentialIssuer,
			) {
				issuer.protector = nil
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newCredentialIssuerFixture(t)
			starter := &fakeIssueCredentialTransactionStarter{}
			issuer := credentialIssuerForFixture(fixture, starter)
			test.prepare(&fixture, issuer)
			_, err := issuer.Issue(context.Background(), fixture.command)
			if !errors.Is(
				err,
				ErrInvalidIssueRegistrationCredentialCommand,
			) {
				t.Fatalf(`Issue() error = %v`, err)
			}
			if starter.calls != 0 {
				t.Fatalf(`transaction starts = %d`, starter.calls)
			}
		})
	}
}

func TestCredentialIssuerPropagatesBeginAndCommitFailures(t *testing.T) {
	t.Parallel()

	fixture := newCredentialIssuerFixture(t)
	beginStarter := &fakeIssueCredentialTransactionStarter{
		err: errCredentialIssuancePersistence,
	}
	_, err := credentialIssuerForFixture(fixture, beginStarter).Issue(
		context.Background(),
		fixture.command,
	)
	if !errors.Is(err, errCredentialIssuancePersistence) {
		t.Fatalf(`Issue(begin) error = %v`, err)
	}

	tx := newFakeIssueCredentialTransaction(fixture)
	tx.commitErr = &pgconn.PgError{Code: `40001`}
	_, err = credentialIssuerForFixture(
		fixture,
		&fakeIssueCredentialTransactionStarter{tx: tx},
	).Issue(context.Background(), fixture.command)
	if !errors.Is(err, ErrRegistrationCredentialTransactionConflict) ||
		tx.committed ||
		!tx.rolledBack {
		t.Fatalf(`Issue(commit) error=%v transaction=%+v`, err, tx)
	}
}

type credentialIssuerFixture struct {
	now          time.Time
	registration registration.Registration
	command      IssueRegistrationCredentialCommand
	protector    *checkin.CredentialProtector
}

func newCredentialIssuerFixture(t *testing.T) credentialIssuerFixture {
	t.Helper()
	now := time.Date(2026, time.September, 13, 8, 0, 0, 0, time.UTC)
	confirmedAt := now.Add(-time.Hour)
	current := registration.Registration{
		ID:                  uuid.New(),
		TenantID:            uuid.New(),
		SeriesID:            uuid.New(),
		InstanceID:          uuid.New(),
		SessionID:           uuid.New(),
		PrincipalID:         uuid.New(),
		ParticipationStatus: registration.ParticipationStatusConfirmed,
		IdempotencyKey:      `registration:credential-issuer:001`,
		ConfirmedAt:         &confirmedAt,
		Version:             2,
		CreatedAt:           now.Add(-2 * time.Hour),
		UpdatedAt:           confirmedAt,
	}
	protector, err := checkin.NewCredentialProtector(
		bytes.Repeat([]byte{0x6E}, 32),
		rand.Reader,
	)
	if err != nil {
		t.Fatalf(`NewCredentialProtector() error = %v`, err)
	}
	return credentialIssuerFixture{
		now:          now,
		registration: current,
		command: IssueRegistrationCredentialCommand{
			TenantID:       current.TenantID,
			RegistrationID: current.ID,
			PrincipalID:    current.PrincipalID,
			TTL:            5 * time.Minute,
		},
		protector: protector,
	}
}

func activeCredentialForIssuance(
	t *testing.T,
	fixture *credentialIssuerFixture,
	epoch int64,
) checkin.Credential {
	t.Helper()
	issued, err := fixture.protector.Issue(checkin.IssueCredentialCommand{
		TenantID:       fixture.registration.TenantID,
		RegistrationID: fixture.registration.ID,
		SeriesID:       fixture.registration.SeriesID,
		InstanceID:     fixture.registration.InstanceID,
		SessionID:      fixture.registration.SessionID,
		PrincipalID:    fixture.registration.PrincipalID,
		Epoch:          epoch,
		TTL:            5 * time.Minute,
		At:             fixture.now.Add(-time.Minute),
	})
	if err != nil {
		t.Fatalf(`Issue(active fixture) error = %v`, err)
	}
	return issued.Credential
}

func credentialIssuerForFixture(
	fixture credentialIssuerFixture,
	starter issueCredentialTransactionStarter,
) *CredentialIssuer {
	return &CredentialIssuer{
		transactions: starter,
		protector:    fixture.protector,
		generationID: uuid.New(),
	}
}

type fakeIssueCredentialTransactionStarter struct {
	tx      issueCredentialTransaction
	err     error
	options *sql.TxOptions
	calls   int
}

func (starter *fakeIssueCredentialTransactionStarter) beginIssueCredentialTx(
	_ context.Context,
	options *sql.TxOptions,
) (issueCredentialTransaction, error) {
	starter.calls++
	starter.options = options
	if starter.err != nil {
		return nil, starter.err
	}
	return starter.tx, nil
}

type fakeIssueCredentialTransaction struct {
	generationErr          error
	transactionTime        time.Time
	currentTimeErr         error
	resolved               registration.Registration
	resolvedErr            error
	seriesStatus           activity.SeriesStatus
	seriesErr              error
	instanceStatus         activity.InstanceStatus
	instanceErr            error
	session                issueCredentialSession
	sessionErr             error
	current                registration.Registration
	currentErr             error
	latest                 checkin.Credential
	latestErr              error
	revoked                checkin.Credential
	revokedExpectedVersion int64
	revokeErr              error
	created                checkin.Credential
	createErr              error
	commitErr              error
	committed              bool
	rolledBack             bool
	operations             []string
}

func newFakeIssueCredentialTransaction(
	fixture credentialIssuerFixture,
) *fakeIssueCredentialTransaction {
	return &fakeIssueCredentialTransaction{
		transactionTime: fixture.now,
		resolved:        fixture.registration,
		seriesStatus:    activity.SeriesStatusActive,
		instanceStatus:  activity.InstanceStatusPublished,
		session: issueCredentialSession{
			status: activity.SessionStatusPublished,
			endAt:  fixture.now.Add(time.Hour),
		},
		current:   fixture.registration,
		latestErr: ErrCredentialNotFound,
	}
}

func (tx *fakeIssueCredentialTransaction) lockActiveGeneration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) error {
	tx.operations = append(tx.operations, `generation`)
	return tx.generationErr
}

func (tx *fakeIssueCredentialTransaction) currentTime(
	context.Context,
) (time.Time, error) {
	tx.operations = append(tx.operations, `database time`)
	return tx.transactionTime, tx.currentTimeErr
}

func (tx *fakeIssueCredentialTransaction) getIssueCredentialRegistration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (registration.Registration, error) {
	tx.operations = append(tx.operations, `registration resolve`)
	return tx.resolved, tx.resolvedErr
}

func (tx *fakeIssueCredentialTransaction) lockIssueCredentialSeries(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (activity.SeriesStatus, error) {
	tx.operations = append(tx.operations, `series`)
	return tx.seriesStatus, tx.seriesErr
}

func (tx *fakeIssueCredentialTransaction) lockIssueCredentialInstance(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (activity.InstanceStatus, error) {
	tx.operations = append(tx.operations, `instance`)
	return tx.instanceStatus, tx.instanceErr
}

func (tx *fakeIssueCredentialTransaction) lockIssueCredentialSession(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
) (issueCredentialSession, error) {
	tx.operations = append(tx.operations, `session`)
	return tx.session, tx.sessionErr
}

func (tx *fakeIssueCredentialTransaction) lockIssueCredentialRegistration(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (registration.Registration, error) {
	tx.operations = append(tx.operations, `registration lock`)
	return tx.current, tx.currentErr
}

func (tx *fakeIssueCredentialTransaction) getLatestIssueCredentialForUpdate(
	context.Context,
	uuid.UUID,
	uuid.UUID,
) (checkin.Credential, error) {
	tx.operations = append(tx.operations, `latest credential`)
	return tx.latest, tx.latestErr
}

func (tx *fakeIssueCredentialTransaction) revokeIssuedCredential(
	_ context.Context,
	value checkin.Credential,
	expectedVersion int64,
) (checkin.Credential, error) {
	tx.operations = append(tx.operations, `revoke credential`)
	tx.revoked = value
	tx.revokedExpectedVersion = expectedVersion
	return value, tx.revokeErr
}

func (tx *fakeIssueCredentialTransaction) createIssuedCredential(
	_ context.Context,
	value checkin.Credential,
) (checkin.Credential, error) {
	tx.operations = append(tx.operations, `create credential`)
	tx.created = value
	return value, tx.createErr
}

func (tx *fakeIssueCredentialTransaction) Commit() error {
	tx.operations = append(tx.operations, `commit`)
	if tx.commitErr != nil {
		return tx.commitErr
	}
	tx.committed = true
	return nil
}

func (tx *fakeIssueCredentialTransaction) Rollback() error {
	tx.operations = append(tx.operations, `rollback`)
	tx.rolledBack = true
	return nil
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
