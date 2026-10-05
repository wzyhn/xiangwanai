package checkinpostgres

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const verifyCredentialOperationLockReleaseTimeout = 5 * time.Second

var (
	ErrInvalidVerifyCredentialCommand = errors.New(
		`invalid xiangwan verify Checkin credential command`,
	)
	ErrCheckinVerificationTargetNotFound = errors.New(
		`xiangwan Checkin verification target not found`,
	)
	ErrCheckinVerificationIdempotencyConflict = errors.New(
		`xiangwan Checkin verification idempotency conflict`,
	)
	ErrCheckinVerificationGenerationInactive = errors.New(
		`xiangwan Checkin verification generation is inactive`,
	)
	ErrCheckinVerificationTransactionConflict = errors.New(
		`xiangwan Checkin verification transaction conflict`,
	)
)

type VerifyCredentialCommand struct {
	TenantID       uuid.UUID
	SeriesID       uuid.UUID
	InstanceID     uuid.UUID
	SessionID      uuid.UUID
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	PresentedKind  checkin.PresentedCredentialKind
	PresentedValue string
	IdempotencyKey string
}

type VerifyCredentialResult struct {
	Attempt   checkin.VerificationAttempt
	Duplicate bool
}

// Verifier turns one presented credential into an immutable minimal decision.
// It fingerprints the request with the credential HMAC key, never persists the
// presented value, and revalidates all current PostgreSQL facts under the same
// serializable transaction that stores the attempt.
type Verifier struct {
	transactions   verifyCredentialTransactionStarter
	operationLocks verifyCredentialOperationLocker
	protector      *checkin.CredentialProtector
	generationID   uuid.UUID
	now            func() time.Time
}

func NewVerifier(
	db *sql.DB,
	protector *checkin.CredentialProtector,
	authorizer CheckinOperatorAuthorizer,
	generationID uuid.UUID,
) *Verifier {
	return &Verifier{
		transactions: sqlVerifyCredentialTransactionStarter{
			authorizer: authorizer,
		},
		operationLocks: sqlVerifyCredentialOperationLocker{db: db},
		protector:      protector,
		generationID:   generationID,
		now:            time.Now,
	}
}

func (verifier *Verifier) Verify(
	ctx context.Context,
	command VerifyCredentialCommand,
) (result VerifyCredentialResult, resultErr error) {
	if verifier == nil || verifier.transactions == nil || verifier.operationLocks == nil ||
		verifier.protector == nil || verifier.generationID == uuid.Nil ||
		verifier.now == nil {
		return VerifyCredentialResult{}, ErrInvalidVerifyCredentialCommand
	}
	if err := validateVerifyCredentialCommand(command); err != nil {
		return VerifyCredentialResult{}, err
	}
	fingerprint, err := verifier.protector.FingerprintVerificationRequest(
		checkin.VerificationRequestFingerprintCommand{
			TenantID:            command.TenantID,
			RequestedSeriesID:   command.SeriesID,
			RequestedInstanceID: command.InstanceID,
			RequestedSessionID:  command.SessionID,
			ActorID:             command.ActorID,
			PresentedKind:       command.PresentedKind,
			PresentedValue:      command.PresentedValue,
		},
	)
	if err != nil {
		return VerifyCredentialResult{}, ErrInvalidVerifyCredentialCommand
	}
	conn, release, err := verifier.operationLocks.lock(
		ctx,
		command.TenantID,
		command.ActorID,
		command.IdempotencyKey,
	)
	if err != nil {
		return VerifyCredentialResult{}, err
	}
	defer func() {
		resultErr = errors.Join(resultErr, release())
	}()

	tx, err := verifier.transactions.beginVerifyCredentialTx(
		ctx,
		conn,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return VerifyCredentialResult{}, fmt.Errorf(
			`begin xiangwan Checkin verification transaction: %w`,
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	authorization := RecordCheckinAuthorization{
		TenantID:       command.TenantID,
		SeriesID:       command.SeriesID,
		InstanceID:     command.InstanceID,
		SessionID:      command.SessionID,
		ActorID:        command.ActorID,
		IdentityLinkID: command.IdentityLinkID,
	}
	if err := tx.authorizeVerifyCredential(ctx, authorization); err != nil {
		if errors.Is(err, ErrCheckinOperatorForbidden) ||
			errors.Is(err, ErrCheckinAuthorizationUnavailable) {
			return VerifyCredentialResult{}, err
		}
		return VerifyCredentialResult{}, fmt.Errorf(
			`authorize xiangwan Checkin verification: %w`,
			err,
		)
	}

	replayed, err := tx.getVerificationAttemptByIdempotencyKey(
		ctx,
		command.TenantID,
		command.ActorID,
		command.IdempotencyKey,
	)
	switch {
	case err == nil:
		if !verificationAttemptMatchesRequest(replayed, command, fingerprint) {
			return VerifyCredentialResult{},
				ErrCheckinVerificationIdempotencyConflict
		}
		if err := tx.Commit(); err != nil {
			return VerifyCredentialResult{},
				classifyVerifyCredentialTransactionError(err)
		}
		committed = true
		return VerifyCredentialResult{Attempt: replayed, Duplicate: true}, nil
	case !errors.Is(err, ErrVerificationAttemptNotFound):
		return VerifyCredentialResult{}, err
	}
	if err := tx.lockVerifyGeneration(
		ctx,
		command.TenantID,
		verifier.generationID,
	); err != nil {
		return VerifyCredentialResult{}, err
	}

	seriesStatus, err := tx.lockVerifySeries(
		ctx,
		command.TenantID,
		command.SeriesID,
	)
	if err != nil {
		return VerifyCredentialResult{}, err
	}
	instanceStatus, err := tx.lockVerifyInstance(
		ctx,
		command.TenantID,
		command.SeriesID,
		command.InstanceID,
	)
	if err != nil {
		return VerifyCredentialResult{}, err
	}
	session, err := tx.lockVerifySession(
		ctx,
		command.TenantID,
		command.InstanceID,
		command.SessionID,
	)
	if err != nil {
		return VerifyCredentialResult{}, err
	}
	targetAvailable := seriesStatus == activity.SeriesStatusActive &&
		instanceStatus == activity.InstanceStatusPublished &&
		session.status == activity.SessionStatusPublished

	candidate, matched, err := verifier.findPresentedCredential(
		ctx,
		tx,
		command,
	)
	if err != nil {
		return VerifyCredentialResult{}, err
	}
	verifiedAt := verifier.now().UTC()
	attemptCommand := checkin.NewVerificationAttemptCommand{
		TenantID:            command.TenantID,
		RequestedSeriesID:   command.SeriesID,
		RequestedInstanceID: command.InstanceID,
		RequestedSessionID:  command.SessionID,
		ActorID:             command.ActorID,
		PresentedKind:       command.PresentedKind,
		IdempotencyKey:      command.IdempotencyKey,
		RequestFingerprint:  fingerprint,
		At:                  verifiedAt,
	}
	if matched {
		current, currentRegistration, currentErr := verifier.lockCurrentCredential(
			ctx,
			tx,
			command,
			candidate,
		)
		if currentErr != nil {
			return VerifyCredentialResult{}, currentErr
		}
		if current != nil {
			attemptCommand.CredentialMatched = true
			attemptCommand.Credential = current
			if current.SeriesID == command.SeriesID &&
				current.InstanceID == command.InstanceID &&
				current.SessionID == command.SessionID {
				if currentRegistration == nil {
					return VerifyCredentialResult{},
						ErrCheckinVerificationTransactionConflict
				}
				eligible, existingCheckin, eligibilityErr :=
					verifier.loadVerificationEligibility(
						ctx,
						tx,
						*current,
						*currentRegistration,
						targetAvailable,
						verifiedAt,
					)
				if eligibilityErr != nil {
					return VerifyCredentialResult{}, eligibilityErr
				}
				attemptCommand.RegistrationEligible = eligible
				attemptCommand.Checkin = existingCheckin
			}
		}
	}
	attempt, err := checkin.NewVerificationAttempt(attemptCommand)
	if err != nil {
		return VerifyCredentialResult{}, fmt.Errorf(
			`build xiangwan Checkin verification attempt: %w`,
			err,
		)
	}
	created, err := tx.createVerificationAttempt(ctx, attempt)
	if err != nil {
		return VerifyCredentialResult{},
			classifyVerifyCredentialTransactionError(err)
	}
	if err := tx.Commit(); err != nil {
		return VerifyCredentialResult{},
			classifyVerifyCredentialTransactionError(err)
	}
	committed = true
	return VerifyCredentialResult{Attempt: created}, nil
}

func (verifier *Verifier) findPresentedCredential(
	ctx context.Context,
	tx verifyCredentialTransaction,
	command VerifyCredentialCommand,
) (checkin.Credential, bool, error) {
	switch command.PresentedKind {
	case checkin.PresentedCredentialKindQRToken:
		jti, _, err := verifier.protector.HashQRToken(command.PresentedValue)
		if err != nil {
			return checkin.Credential{}, false, nil
		}
		value, err := tx.getVerificationCredentialByJTI(
			ctx,
			command.TenantID,
			jti,
		)
		switch {
		case errors.Is(err, ErrCredentialNotFound):
			return checkin.Credential{}, false, nil
		case err != nil:
			return checkin.Credential{}, false, err
		case !verifier.protector.MatchesQRToken(value, command.PresentedValue):
			return checkin.Credential{}, false, nil
		default:
			return value, true, nil
		}
	case checkin.PresentedCredentialKindBackupCode:
		digest, err := verifier.protector.HashBackupCode(command.PresentedValue)
		if err != nil {
			return checkin.Credential{}, false, nil
		}
		value, err := tx.getVerificationCredentialByBackupHash(
			ctx,
			command.TenantID,
			digest,
		)
		switch {
		case errors.Is(err, ErrCredentialNotFound):
			return checkin.Credential{}, false, nil
		case err != nil:
			return checkin.Credential{}, false, err
		case !verifier.protector.MatchesBackupCode(
			value,
			command.PresentedValue,
		):
			return checkin.Credential{}, false, nil
		default:
			return value, true, nil
		}
	default:
		return checkin.Credential{}, false, ErrInvalidVerifyCredentialCommand
	}
}

func (verifier *Verifier) lockCurrentCredential(
	ctx context.Context,
	tx verifyCredentialTransaction,
	command VerifyCredentialCommand,
	candidate checkin.Credential,
) (*checkin.Credential, *registration.Registration, error) {
	var lockedRegistration *registration.Registration
	if candidate.SeriesID == command.SeriesID &&
		candidate.InstanceID == command.InstanceID &&
		candidate.SessionID == command.SessionID {
		current, err := tx.lockVerifyRegistration(
			ctx,
			command.TenantID,
			candidate.RegistrationID,
		)
		if err != nil {
			return nil, nil, err
		}
		if !registrationMatchesCredential(current, candidate) {
			return nil, nil, ErrCheckinVerificationTransactionConflict
		}
		lockedRegistration = &current
	}
	current, err := tx.lockVerifyCredential(
		ctx,
		command.TenantID,
		candidate.ID,
	)
	if errors.Is(err, ErrCredentialNotFound) {
		return nil, nil, ErrCheckinVerificationTransactionConflict
	}
	if err != nil {
		return nil, nil, err
	}
	if !credentialsHaveSameIdentity(current, candidate) ||
		!presentedCredentialMatches(
			verifier.protector,
			current,
			command.PresentedKind,
			command.PresentedValue,
		) {
		return nil, nil, ErrCheckinVerificationTransactionConflict
	}
	return &current, lockedRegistration, nil
}

func (verifier *Verifier) loadVerificationEligibility(
	ctx context.Context,
	tx verifyCredentialTransaction,
	credential checkin.Credential,
	currentRegistration registration.Registration,
	targetAvailable bool,
	verifiedAt time.Time,
) (bool, *checkin.Checkin, error) {
	eligible := targetAvailable &&
		currentRegistration.ParticipationStatus ==
			registration.ParticipationStatusConfirmed &&
		currentRegistration.ConfirmedAt != nil &&
		!verifiedAt.Before(*currentRegistration.ConfirmedAt)
	if verifiedAt.Before(credential.IssuedAt) {
		return false, nil, ErrCheckinVerificationTransactionConflict
	}
	if !eligible ||
		credential.CredentialStatus != checkin.CredentialStatusActive ||
		credential.Expired(verifiedAt) {
		return eligible, nil, nil
	}
	existing, err := tx.lockVerifyCheckinByRegistration(
		ctx,
		credential.TenantID,
		credential.RegistrationID,
	)
	switch {
	case errors.Is(err, ErrCheckinNotFound):
		return true, nil, nil
	case err != nil:
		return false, nil, err
	case !checkinMatchesCredential(existing, credential):
		return false, nil, ErrCheckinVerificationTransactionConflict
	default:
		return true, &existing, nil
	}
}

func validateVerifyCredentialCommand(command VerifyCredentialCommand) error {
	if command.TenantID == uuid.Nil ||
		command.SeriesID == uuid.Nil ||
		command.InstanceID == uuid.Nil ||
		command.SessionID == uuid.Nil ||
		command.ActorID == uuid.Nil ||
		command.IdentityLinkID == uuid.Nil ||
		(command.PresentedKind != checkin.PresentedCredentialKindQRToken &&
			command.PresentedKind != checkin.PresentedCredentialKindBackupCode) ||
		command.PresentedValue == `` ||
		len(command.PresentedValue) > checkin.MaxPresentedCredentialLength ||
		!checkinCommandIdempotencyKeyPattern.MatchString(command.IdempotencyKey) {
		return ErrInvalidVerifyCredentialCommand
	}
	return nil
}

func verificationAttemptMatchesRequest(
	value checkin.VerificationAttempt,
	command VerifyCredentialCommand,
	fingerprint checkin.CredentialDigest,
) bool {
	return checkin.ValidateVerificationAttempt(value) == nil &&
		value.TenantID == command.TenantID &&
		value.RequestedSeriesID == command.SeriesID &&
		value.RequestedInstanceID == command.InstanceID &&
		value.RequestedSessionID == command.SessionID &&
		value.ActorID == command.ActorID &&
		value.PresentedKind == command.PresentedKind &&
		value.IdempotencyKey == command.IdempotencyKey &&
		hmac.Equal(value.RequestFingerprint[:], fingerprint[:])
}

func registrationMatchesCredential(
	value registration.Registration,
	credential checkin.Credential,
) bool {
	return value.ID == credential.RegistrationID &&
		value.TenantID == credential.TenantID &&
		value.SeriesID == credential.SeriesID &&
		value.InstanceID == credential.InstanceID &&
		value.SessionID == credential.SessionID &&
		value.PrincipalID == credential.PrincipalID
}

func credentialsHaveSameIdentity(
	current checkin.Credential,
	candidate checkin.Credential,
) bool {
	return checkin.ValidateCredential(current) == nil &&
		current.ID == candidate.ID &&
		current.TenantID == candidate.TenantID &&
		current.RegistrationID == candidate.RegistrationID &&
		current.SeriesID == candidate.SeriesID &&
		current.InstanceID == candidate.InstanceID &&
		current.SessionID == candidate.SessionID &&
		current.PrincipalID == candidate.PrincipalID &&
		current.CredentialJTI == candidate.CredentialJTI &&
		current.CredentialEpoch == candidate.CredentialEpoch
}

func presentedCredentialMatches(
	protector *checkin.CredentialProtector,
	credential checkin.Credential,
	kind checkin.PresentedCredentialKind,
	presented string,
) bool {
	switch kind {
	case checkin.PresentedCredentialKindQRToken:
		return protector.MatchesQRToken(credential, presented)
	case checkin.PresentedCredentialKindBackupCode:
		return protector.MatchesBackupCode(credential, presented)
	default:
		return false
	}
}

func checkinMatchesCredential(
	value checkin.Checkin,
	credential checkin.Credential,
) bool {
	return checkin.Validate(value) == nil &&
		value.TenantID == credential.TenantID &&
		value.RegistrationID == credential.RegistrationID &&
		value.SeriesID == credential.SeriesID &&
		value.InstanceID == credential.InstanceID &&
		value.SessionID == credential.SessionID &&
		value.PrincipalID == credential.PrincipalID
}

type verifyCredentialTransactionStarter interface {
	beginVerifyCredentialTx(
		context.Context,
		*sql.Conn,
		*sql.TxOptions,
	) (verifyCredentialTransaction, error)
}

type verifyCredentialOperationLocker interface {
	lock(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		string,
	) (*sql.Conn, func() error, error)
}

type verifyCredentialTransaction interface {
	authorizeVerifyCredential(context.Context, RecordCheckinAuthorization) error
	getVerificationAttemptByIdempotencyKey(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		string,
	) (checkin.VerificationAttempt, error)
	lockVerifyGeneration(context.Context, uuid.UUID, uuid.UUID) error
	lockVerifySeries(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (activity.SeriesStatus, error)
	lockVerifyInstance(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (activity.InstanceStatus, error)
	lockVerifySession(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (recordCheckinSession, error)
	getVerificationCredentialByJTI(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.Credential, error)
	getVerificationCredentialByBackupHash(
		context.Context,
		uuid.UUID,
		checkin.CredentialDigest,
	) (checkin.Credential, error)
	lockVerifyRegistration(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (registration.Registration, error)
	lockVerifyCredential(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.Credential, error)
	lockVerifyCheckinByRegistration(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.Checkin, error)
	createVerificationAttempt(
		context.Context,
		checkin.VerificationAttempt,
	) (checkin.VerificationAttempt, error)
	Commit() error
	Rollback() error
}

type sqlVerifyCredentialTransactionStarter struct {
	authorizer CheckinOperatorAuthorizer
}

type sqlVerifyCredentialOperationLocker struct {
	db *sql.DB
}

func (locker sqlVerifyCredentialOperationLocker) lock(
	ctx context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	idempotencyKey string,
) (*sql.Conn, func() error, error) {
	conn, err := locker.db.Conn(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("open Checkin verification operation lock connection: %w", err)
	}
	lockKey := tenantID.String() + ":" + actorID.String() + ":" + idempotencyKey
	var locked bool
	err = conn.QueryRowContext(ctx, `
SELECT TRUE
FROM (SELECT pg_advisory_lock(hashtextextended($1, 0))) AS operation_lock
`, lockKey).Scan(&locked)
	if err != nil || !locked {
		discardVerifyCredentialOperationLockConnection(conn)
		if err == nil {
			err = ErrCheckinVerificationTransactionConflict
		}
		return nil, nil, fmt.Errorf("lock Checkin verification operation: %w", err)
	}
	var once sync.Once
	var releaseErr error
	release := func() error {
		once.Do(func() {
			releaseCtx, cancelRelease := context.WithTimeout(
				context.WithoutCancel(ctx),
				verifyCredentialOperationLockReleaseTimeout,
			)
			defer cancelRelease()
			var unlocked bool
			unlockErr := conn.QueryRowContext(
				releaseCtx,
				`SELECT pg_advisory_unlock(hashtextextended($1, 0))`,
				lockKey,
			).Scan(&unlocked)
			if unlockErr == nil && !unlocked {
				unlockErr = errors.New(
					"Checkin verification operation lock was not held by its connection",
				)
			}
			if unlockErr != nil {
				discardVerifyCredentialOperationLockConnection(conn)
				releaseErr = unlockErr
				return
			}
			releaseErr = conn.Close()
		})
		return releaseErr
	}
	return conn, release, nil
}

func discardVerifyCredentialOperationLockConnection(conn *sql.Conn) {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
}

func (starter sqlVerifyCredentialTransactionStarter) beginVerifyCredentialTx(
	ctx context.Context,
	conn *sql.Conn,
	options *sql.TxOptions,
) (verifyCredentialTransaction, error) {
	if conn == nil {
		return nil, errors.New("Checkin verification operation lock connection is unavailable")
	}
	sqlTx, err := conn.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlVerifyCredentialTransaction{
		facts: &sqlRecordCheckinTransaction{
			tx:            sqlTx,
			authorizer:    starter.authorizer,
			checkins:      NewRepository(sqlTx),
			registrations: registrationpostgres.NewRepository(sqlTx),
		},
	}, nil
}

type sqlVerifyCredentialTransaction struct {
	facts *sqlRecordCheckinTransaction
}

func (tx *sqlVerifyCredentialTransaction) authorizeVerifyCredential(
	ctx context.Context,
	request RecordCheckinAuthorization,
) error {
	return tx.facts.authorizeRecordCheckin(ctx, request)
}

func (tx *sqlVerifyCredentialTransaction) getVerificationAttemptByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	idempotencyKey string,
) (checkin.VerificationAttempt, error) {
	return tx.facts.checkins.GetVerificationAttemptByIdempotencyKey(
		ctx,
		tenantID,
		actorID,
		idempotencyKey,
	)
}

func (tx *sqlVerifyCredentialTransaction) lockVerifyGeneration(
	ctx context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	var writeEpoch int64
	err := tx.facts.tx.QueryRowContext(ctx, `
SELECT write_epoch
FROM xiangwan_runtime_generations
WHERE singleton_id = 1
  AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1
  AND active_generation_id = $2
  AND write_epoch > 0
  AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, tenantID, generationID).Scan(&writeEpoch)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCheckinVerificationGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan Checkin verification generation: %w", err)
	}
	return nil
}

func (tx *sqlVerifyCredentialTransaction) lockVerifySeries(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (activity.SeriesStatus, error) {
	value, err := tx.facts.lockRecordSeries(ctx, tenantID, seriesID)
	return value, mapCheckinVerificationTargetError(err)
}

func (tx *sqlVerifyCredentialTransaction) lockVerifyInstance(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (activity.InstanceStatus, error) {
	value, err := tx.facts.lockRecordInstance(
		ctx,
		tenantID,
		seriesID,
		instanceID,
	)
	return value, mapCheckinVerificationTargetError(err)
}

func (tx *sqlVerifyCredentialTransaction) lockVerifySession(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (recordCheckinSession, error) {
	value, err := tx.facts.lockRecordSession(
		ctx,
		tenantID,
		instanceID,
		sessionID,
	)
	return value, mapCheckinVerificationTargetError(err)
}

func (tx *sqlVerifyCredentialTransaction) getVerificationCredentialByJTI(
	ctx context.Context,
	tenantID uuid.UUID,
	jti uuid.UUID,
) (checkin.Credential, error) {
	return tx.facts.checkins.GetCredentialByJTI(ctx, tenantID, jti)
}

func (tx *sqlVerifyCredentialTransaction) getVerificationCredentialByBackupHash(
	ctx context.Context,
	tenantID uuid.UUID,
	digest checkin.CredentialDigest,
) (checkin.Credential, error) {
	return tx.facts.checkins.GetCredentialByBackupHash(ctx, tenantID, digest)
}

func (tx *sqlVerifyCredentialTransaction) lockVerifyRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (registration.Registration, error) {
	value, err := tx.facts.lockRecordRegistration(
		ctx,
		tenantID,
		registrationID,
	)
	return value, mapCheckinVerificationTargetError(err)
}

func (tx *sqlVerifyCredentialTransaction) lockVerifyCredential(
	ctx context.Context,
	tenantID uuid.UUID,
	credentialID uuid.UUID,
) (checkin.Credential, error) {
	return tx.facts.lockRecordCredential(ctx, tenantID, credentialID)
}

func (tx *sqlVerifyCredentialTransaction) lockVerifyCheckinByRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.Checkin, error) {
	return tx.facts.lockCheckinByRegistration(ctx, tenantID, registrationID)
}

func (tx *sqlVerifyCredentialTransaction) createVerificationAttempt(
	ctx context.Context,
	value checkin.VerificationAttempt,
) (checkin.VerificationAttempt, error) {
	return tx.facts.checkins.CreateVerificationAttempt(ctx, value)
}

func (tx *sqlVerifyCredentialTransaction) Commit() error {
	return tx.facts.tx.Commit()
}

func (tx *sqlVerifyCredentialTransaction) Rollback() error {
	return tx.facts.tx.Rollback()
}

func mapCheckinVerificationTargetError(err error) error {
	if errors.Is(err, ErrCheckinRecordTargetNotFound) {
		return ErrCheckinVerificationTargetNotFound
	}
	return err
}

func classifyVerifyCredentialTransactionError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case `23505`, `40001`, `40P01`:
			return fmt.Errorf(
				`%w: %v`,
				ErrCheckinVerificationTransactionConflict,
				err,
			)
		}
	}
	return err
}
