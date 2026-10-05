package checkinpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidRecordCheckinCommand = errors.New(
		`invalid xiangwan record Checkin command`,
	)
	ErrCheckinOperatorForbidden = errors.New(
		`xiangwan Checkin operator is forbidden`,
	)
	ErrCheckinAuthorizationUnavailable = errors.New(
		`xiangwan Checkin authorization is unavailable`,
	)
	ErrCheckinRecordTargetNotFound = errors.New(
		`xiangwan Checkin record target not found`,
	)
	ErrCheckinRecordUnavailable = errors.New(
		`xiangwan Checkin record target is unavailable`,
	)
	ErrCheckinVerificationRejected = errors.New(
		`xiangwan Checkin verification is rejected`,
	)
	ErrCheckinRecordIdempotencyConflict = errors.New(
		`xiangwan Checkin record idempotency conflict`,
	)
	ErrCheckinRecordGenerationInactive = errors.New(
		`xiangwan Checkin record generation is inactive`,
	)
	ErrCheckinRecordTransactionConflict = errors.New(
		`xiangwan Checkin record transaction conflict`,
	)
)

var checkinCommandIdempotencyKeyPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`,
)

type RecordCheckinCommand struct {
	TenantID              uuid.UUID
	SeriesID              uuid.UUID
	InstanceID            uuid.UUID
	SessionID             uuid.UUID
	RegistrationID        uuid.UUID
	CredentialID          uuid.UUID
	VerificationAttemptID uuid.UUID
	ActorID               uuid.UUID
	IdentityLinkID        uuid.UUID
	IdempotencyKey        string
}

type RecordCheckinResult struct {
	Checkin   checkin.Checkin
	Event     *checkin.Event
	Duplicate bool
}

type RecordCheckinAuthorization struct {
	TenantID       uuid.UUID
	SeriesID       uuid.UUID
	InstanceID     uuid.UUID
	SessionID      uuid.UUID
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
}

type OperatorAuthorizationQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CheckinOperatorAuthorizer must verify the actor's current, Session-scoped
// on-site capability through the supplied transaction query boundary. It must
// return ErrCheckinOperatorForbidden for a definitive denial.
type CheckinOperatorAuthorizer interface {
	AuthorizeRecordCheckin(
		context.Context,
		OperatorAuthorizationQuery,
		RecordCheckinAuthorization,
	) error
}

// Recorder commits the Checkin fact, immutable event, and Session attendance
// counter in one serializable PostgreSQL transaction. It revalidates the
// current Registration and credential even when a verification receipt was
// created immediately beforehand.
type Recorder struct {
	transactions recordCheckinTransactionStarter
	generationID uuid.UUID
	now          func() time.Time
}

func NewRecorder(
	db *sql.DB,
	authorizer CheckinOperatorAuthorizer,
	generationID uuid.UUID,
) *Recorder {
	return &Recorder{
		transactions: sqlRecordCheckinTransactionStarter{
			db:         db,
			authorizer: authorizer,
		},
		generationID: generationID,
		now:          time.Now,
	}
}

func (recorder *Recorder) Record(
	ctx context.Context,
	command RecordCheckinCommand,
) (RecordCheckinResult, error) {
	if recorder == nil || recorder.transactions == nil ||
		recorder.generationID == uuid.Nil || recorder.now == nil {
		return RecordCheckinResult{}, ErrInvalidRecordCheckinCommand
	}
	if err := validateRecordCheckinCommand(command); err != nil {
		return RecordCheckinResult{}, err
	}
	tx, err := recorder.transactions.beginRecordCheckinTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return RecordCheckinResult{}, fmt.Errorf(
			`begin xiangwan record Checkin transaction: %w`,
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
	if err := tx.authorizeRecordCheckin(ctx, authorization); err != nil {
		if errors.Is(err, ErrCheckinOperatorForbidden) ||
			errors.Is(err, ErrCheckinAuthorizationUnavailable) {
			return RecordCheckinResult{}, err
		}
		return RecordCheckinResult{}, fmt.Errorf(
			`authorize xiangwan record Checkin: %w`,
			err,
		)
	}

	replayed, err := tx.getCheckinEventByIdempotencyKey(
		ctx,
		command.TenantID,
		command.ActorID,
		command.IdempotencyKey,
	)
	switch {
	case err == nil:
		return recorder.replayRecord(ctx, tx, command, replayed, &committed)
	case !errors.Is(err, ErrCheckinEventNotFound):
		return RecordCheckinResult{}, err
	}
	if err := tx.lockRecordGeneration(
		ctx,
		command.TenantID,
		recorder.generationID,
	); err != nil {
		return RecordCheckinResult{}, err
	}

	seriesStatus, err := tx.lockRecordSeries(
		ctx,
		command.TenantID,
		command.SeriesID,
	)
	if err != nil {
		return RecordCheckinResult{}, err
	}
	instanceStatus, err := tx.lockRecordInstance(
		ctx,
		command.TenantID,
		command.SeriesID,
		command.InstanceID,
	)
	if err != nil {
		return RecordCheckinResult{}, err
	}
	session, err := tx.lockRecordSession(
		ctx,
		command.TenantID,
		command.InstanceID,
		command.SessionID,
	)
	if err != nil {
		return RecordCheckinResult{}, err
	}
	if seriesStatus != activity.SeriesStatusActive ||
		instanceStatus != activity.InstanceStatusPublished ||
		session.status != activity.SessionStatusPublished {
		return RecordCheckinResult{}, ErrCheckinRecordUnavailable
	}

	currentRegistration, err := tx.lockRecordRegistration(
		ctx,
		command.TenantID,
		command.RegistrationID,
	)
	if err != nil {
		return RecordCheckinResult{}, err
	}
	if !registrationMatchesRecordCommand(currentRegistration, command) {
		return RecordCheckinResult{}, ErrCheckinRecordTargetNotFound
	}
	if currentRegistration.ParticipationStatus !=
		registration.ParticipationStatusConfirmed ||
		currentRegistration.ConfirmedAt == nil {
		return RecordCheckinResult{}, ErrCheckinRecordUnavailable
	}

	attempt, err := tx.getRecordVerificationAttempt(
		ctx,
		command.TenantID,
		command.VerificationAttemptID,
	)
	if errors.Is(err, ErrVerificationAttemptNotFound) {
		return RecordCheckinResult{}, ErrCheckinVerificationRejected
	}
	if err != nil {
		return RecordCheckinResult{}, err
	}
	if !attemptMatchesRecordCommand(attempt, command, currentRegistration) {
		return RecordCheckinResult{}, ErrCheckinVerificationRejected
	}
	credential, err := tx.lockRecordCredential(
		ctx,
		command.TenantID,
		command.CredentialID,
	)
	if errors.Is(err, ErrCredentialNotFound) {
		return RecordCheckinResult{}, ErrCheckinVerificationRejected
	}
	if err != nil {
		return RecordCheckinResult{}, err
	}
	if !credentialMatchesRecordCommand(credential, command, currentRegistration) {
		return RecordCheckinResult{}, ErrCheckinVerificationRejected
	}

	processedAt := recorder.now().UTC()
	if processedAt.IsZero() ||
		processedAt.Before(*currentRegistration.ConfirmedAt) ||
		attempt.CredentialJTI == nil ||
		*attempt.CredentialJTI != credential.CredentialJTI ||
		attempt.OccurredAt.Before(*currentRegistration.ConfirmedAt) ||
		attempt.OccurredAt.Before(credential.IssuedAt) ||
		!attempt.OccurredAt.Before(credential.ExpiresAt) ||
		attempt.OccurredAt.After(processedAt) ||
		credential.CredentialStatus != checkin.CredentialStatusActive ||
		credential.Expired(processedAt) {
		return RecordCheckinResult{}, ErrCheckinVerificationRejected
	}

	existing, err := tx.lockCheckinByRegistration(
		ctx,
		command.TenantID,
		command.RegistrationID,
	)
	switch {
	case err == nil:
		if !checkinMatchesRecordCommand(existing, command, currentRegistration) ||
			(attempt.Decision == checkin.VerificationDecisionAlreadyCheckedIn &&
				(attempt.CheckinID == nil || *attempt.CheckinID != existing.ID)) {
			return RecordCheckinResult{}, ErrCheckinVerificationRejected
		}
		if err := tx.Commit(); err != nil {
			return RecordCheckinResult{}, classifyRecordCheckinTransactionError(err)
		}
		committed = true
		return RecordCheckinResult{Checkin: existing, Duplicate: true}, nil
	case !errors.Is(err, ErrCheckinNotFound):
		return RecordCheckinResult{}, err
	case attempt.Decision == checkin.VerificationDecisionAlreadyCheckedIn:
		return RecordCheckinResult{}, ErrCheckinVerificationRejected
	}
	if session.checkedInCount >= session.confirmedCount {
		return RecordCheckinResult{}, ErrCheckinRecordTransactionConflict
	}

	candidate, err := checkin.New(checkin.NewCommand{
		TenantID:       currentRegistration.TenantID,
		RegistrationID: currentRegistration.ID,
		SeriesID:       currentRegistration.SeriesID,
		InstanceID:     currentRegistration.InstanceID,
		SessionID:      currentRegistration.SessionID,
		PrincipalID:    currentRegistration.PrincipalID,
		CheckedInBy:    command.ActorID,
		At:             processedAt,
	})
	if err != nil {
		return RecordCheckinResult{}, ErrInvalidRecordCheckinCommand
	}
	event, err := checkin.NewEvent(nil, candidate, command.IdempotencyKey)
	if err != nil {
		return RecordCheckinResult{}, ErrInvalidRecordCheckinCommand
	}
	created, err := tx.createRecordedCheckin(ctx, candidate)
	if err != nil {
		return RecordCheckinResult{}, classifyRecordCheckinTransactionError(err)
	}
	event.CheckinID = created.ID
	createdEvent, err := tx.createRecordedCheckinEvent(ctx, event)
	if err != nil {
		return RecordCheckinResult{}, classifyRecordCheckinTransactionError(err)
	}
	if err := tx.incrementRecordedCheckinCount(
		ctx,
		command.TenantID,
		command.SessionID,
		processedAt,
	); err != nil {
		return RecordCheckinResult{}, classifyRecordCheckinTransactionError(err)
	}
	if err := tx.Commit(); err != nil {
		return RecordCheckinResult{}, classifyRecordCheckinTransactionError(err)
	}
	committed = true
	return RecordCheckinResult{
		Checkin: created,
		Event:   &createdEvent,
	}, nil
}

func (recorder *Recorder) replayRecord(
	ctx context.Context,
	tx recordCheckinTransaction,
	command RecordCheckinCommand,
	event checkin.Event,
	committed *bool,
) (RecordCheckinResult, error) {
	if event.EventType != checkin.EventTypeCheckedIn ||
		event.TenantID != command.TenantID ||
		event.RegistrationID != command.RegistrationID ||
		event.SessionID != command.SessionID ||
		event.ActorID != command.ActorID {
		return RecordCheckinResult{}, ErrCheckinRecordIdempotencyConflict
	}
	current, err := tx.lockRecordedCheckin(
		ctx,
		command.TenantID,
		event.CheckinID,
	)
	if errors.Is(err, ErrCheckinNotFound) {
		return RecordCheckinResult{}, ErrCheckinRecordIdempotencyConflict
	}
	if err != nil {
		return RecordCheckinResult{}, err
	}
	if current.SeriesID != command.SeriesID ||
		current.InstanceID != command.InstanceID ||
		current.SessionID != command.SessionID ||
		current.RegistrationID != command.RegistrationID {
		return RecordCheckinResult{}, ErrCheckinRecordIdempotencyConflict
	}
	attempt, err := tx.getRecordVerificationAttempt(
		ctx,
		command.TenantID,
		command.VerificationAttemptID,
	)
	if errors.Is(err, ErrVerificationAttemptNotFound) {
		return RecordCheckinResult{}, ErrCheckinRecordIdempotencyConflict
	}
	if err != nil {
		return RecordCheckinResult{}, err
	}
	if !attemptMatchesReplay(command, attempt, current) {
		return RecordCheckinResult{}, ErrCheckinRecordIdempotencyConflict
	}
	if err := tx.Commit(); err != nil {
		return RecordCheckinResult{}, classifyRecordCheckinTransactionError(err)
	}
	*committed = true
	return RecordCheckinResult{
		Checkin:   current,
		Event:     &event,
		Duplicate: true,
	}, nil
}

func attemptMatchesReplay(
	command RecordCheckinCommand,
	attempt checkin.VerificationAttempt,
	current checkin.Checkin,
) bool {
	return checkin.ValidateVerificationAttempt(attempt) == nil &&
		attempt.ID == command.VerificationAttemptID &&
		attempt.TenantID == command.TenantID &&
		attempt.RequestedSeriesID == command.SeriesID &&
		attempt.RequestedInstanceID == command.InstanceID &&
		attempt.RequestedSessionID == command.SessionID &&
		attempt.ActorID == command.ActorID &&
		attempt.CredentialID != nil &&
		*attempt.CredentialID == command.CredentialID &&
		attempt.RegistrationID != nil &&
		*attempt.RegistrationID == command.RegistrationID &&
		attempt.PrincipalID != nil &&
		*attempt.PrincipalID == current.PrincipalID &&
		(attempt.Decision == checkin.VerificationDecisionValid ||
			(attempt.Decision == checkin.VerificationDecisionAlreadyCheckedIn &&
				attempt.CheckinID != nil &&
				*attempt.CheckinID == current.ID))
}

func validateRecordCheckinCommand(command RecordCheckinCommand) error {
	if command.TenantID == uuid.Nil ||
		command.SeriesID == uuid.Nil ||
		command.InstanceID == uuid.Nil ||
		command.SessionID == uuid.Nil ||
		command.RegistrationID == uuid.Nil ||
		command.CredentialID == uuid.Nil ||
		command.VerificationAttemptID == uuid.Nil ||
		command.ActorID == uuid.Nil ||
		command.IdentityLinkID == uuid.Nil ||
		!checkinCommandIdempotencyKeyPattern.MatchString(command.IdempotencyKey) {
		return ErrInvalidRecordCheckinCommand
	}
	return nil
}

func registrationMatchesRecordCommand(
	value registration.Registration,
	command RecordCheckinCommand,
) bool {
	return value.ID == command.RegistrationID &&
		value.TenantID == command.TenantID &&
		value.SeriesID == command.SeriesID &&
		value.InstanceID == command.InstanceID &&
		value.SessionID == command.SessionID
}

func attemptMatchesRecordCommand(
	value checkin.VerificationAttempt,
	command RecordCheckinCommand,
	currentRegistration registration.Registration,
) bool {
	if checkin.ValidateVerificationAttempt(value) != nil ||
		value.ID != command.VerificationAttemptID ||
		value.TenantID != command.TenantID ||
		value.RequestedSeriesID != command.SeriesID ||
		value.RequestedInstanceID != command.InstanceID ||
		value.RequestedSessionID != command.SessionID ||
		value.ActorID != command.ActorID ||
		value.CredentialID == nil ||
		*value.CredentialID != command.CredentialID ||
		value.RegistrationID == nil ||
		*value.RegistrationID != command.RegistrationID ||
		value.PrincipalID == nil ||
		*value.PrincipalID != currentRegistration.PrincipalID {
		return false
	}
	return value.Decision == checkin.VerificationDecisionValid ||
		value.Decision == checkin.VerificationDecisionAlreadyCheckedIn
}

func credentialMatchesRecordCommand(
	value checkin.Credential,
	command RecordCheckinCommand,
	currentRegistration registration.Registration,
) bool {
	return checkin.ValidateCredential(value) == nil &&
		value.ID == command.CredentialID &&
		value.TenantID == command.TenantID &&
		value.RegistrationID == command.RegistrationID &&
		value.SeriesID == command.SeriesID &&
		value.InstanceID == command.InstanceID &&
		value.SessionID == command.SessionID &&
		value.PrincipalID == currentRegistration.PrincipalID
}

func checkinMatchesRecordCommand(
	value checkin.Checkin,
	command RecordCheckinCommand,
	currentRegistration registration.Registration,
) bool {
	return checkin.Validate(value) == nil &&
		value.TenantID == command.TenantID &&
		value.RegistrationID == command.RegistrationID &&
		value.SeriesID == command.SeriesID &&
		value.InstanceID == command.InstanceID &&
		value.SessionID == command.SessionID &&
		value.PrincipalID == currentRegistration.PrincipalID
}

type recordCheckinSession struct {
	status         activity.SessionStatus
	confirmedCount int
	checkedInCount int
}

type recordCheckinTransactionStarter interface {
	beginRecordCheckinTx(
		context.Context,
		*sql.TxOptions,
	) (recordCheckinTransaction, error)
}

type recordCheckinTransaction interface {
	authorizeRecordCheckin(context.Context, RecordCheckinAuthorization) error
	getCheckinEventByIdempotencyKey(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		string,
	) (checkin.Event, error)
	lockRecordGeneration(context.Context, uuid.UUID, uuid.UUID) error
	lockRecordSeries(context.Context, uuid.UUID, uuid.UUID) (activity.SeriesStatus, error)
	lockRecordInstance(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (activity.InstanceStatus, error)
	lockRecordSession(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (recordCheckinSession, error)
	lockRecordRegistration(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (registration.Registration, error)
	getRecordVerificationAttempt(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.VerificationAttempt, error)
	lockRecordCredential(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.Credential, error)
	lockCheckinByRegistration(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.Checkin, error)
	lockRecordedCheckin(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.Checkin, error)
	createRecordedCheckin(context.Context, checkin.Checkin) (checkin.Checkin, error)
	createRecordedCheckinEvent(context.Context, checkin.Event) (checkin.Event, error)
	incrementRecordedCheckinCount(context.Context, uuid.UUID, uuid.UUID, time.Time) error
	Commit() error
	Rollback() error
}

type sqlRecordCheckinTransactionStarter struct {
	db         *sql.DB
	authorizer CheckinOperatorAuthorizer
}

func (starter sqlRecordCheckinTransactionStarter) beginRecordCheckinTx(
	ctx context.Context,
	options *sql.TxOptions,
) (recordCheckinTransaction, error) {
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlRecordCheckinTransaction{
		tx:            tx,
		authorizer:    starter.authorizer,
		checkins:      NewRepository(tx),
		registrations: registrationpostgres.NewRepository(tx),
	}, nil
}

type sqlRecordCheckinTransaction struct {
	tx            *sql.Tx
	authorizer    CheckinOperatorAuthorizer
	checkins      *Repository
	registrations *registrationpostgres.Repository
}

func (tx *sqlRecordCheckinTransaction) authorizeRecordCheckin(
	ctx context.Context,
	request RecordCheckinAuthorization,
) error {
	if tx.authorizer == nil {
		return ErrCheckinAuthorizationUnavailable
	}
	return tx.authorizer.AuthorizeRecordCheckin(ctx, tx.tx, request)
}

func (tx *sqlRecordCheckinTransaction) getCheckinEventByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	idempotencyKey string,
) (checkin.Event, error) {
	return tx.checkins.GetEventByIdempotencyKey(
		ctx,
		tenantID,
		actorID,
		idempotencyKey,
	)
}

func (tx *sqlRecordCheckinTransaction) lockRecordGeneration(
	ctx context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) error {
	var writeEpoch int64
	err := tx.tx.QueryRowContext(ctx, `
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
		return ErrCheckinRecordGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan Checkin record generation: %w", err)
	}
	return nil
}

func (tx *sqlRecordCheckinTransaction) lockRecordSeries(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (activity.SeriesStatus, error) {
	var status activity.SeriesStatus
	err := tx.tx.QueryRowContext(ctx, `
SELECT status
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, seriesID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ``, ErrCheckinRecordTargetNotFound
	}
	if err != nil {
		return ``, fmt.Errorf(`lock xiangwan Checkin Series: %w`, err)
	}
	return status, nil
}

func (tx *sqlRecordCheckinTransaction) lockRecordInstance(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) (activity.InstanceStatus, error) {
	var status activity.InstanceStatus
	err := tx.tx.QueryRowContext(ctx, `
SELECT status
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND series_id = $2 AND id = $3
FOR UPDATE
`, tenantID, seriesID, instanceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ``, ErrCheckinRecordTargetNotFound
	}
	if err != nil {
		return ``, fmt.Errorf(`lock xiangwan Checkin Instance: %w`, err)
	}
	return status, nil
}

func (tx *sqlRecordCheckinTransaction) lockRecordSession(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (recordCheckinSession, error) {
	var value recordCheckinSession
	err := tx.tx.QueryRowContext(ctx, `
SELECT status, confirmed_registration_count, checked_in_registration_count
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2 AND id = $3
FOR UPDATE
`, tenantID, instanceID, sessionID).Scan(
		&value.status,
		&value.confirmedCount,
		&value.checkedInCount,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return recordCheckinSession{}, ErrCheckinRecordTargetNotFound
	}
	if err != nil {
		return recordCheckinSession{}, fmt.Errorf(`lock xiangwan Checkin Session: %w`, err)
	}
	return value, nil
}

func (tx *sqlRecordCheckinTransaction) lockRecordRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (registration.Registration, error) {
	value, err := tx.registrations.GetForUpdate(ctx, tenantID, registrationID)
	if errors.Is(err, registrationpostgres.ErrRegistrationNotFound) {
		return registration.Registration{}, ErrCheckinRecordTargetNotFound
	}
	return value, err
}

func (tx *sqlRecordCheckinTransaction) getRecordVerificationAttempt(
	ctx context.Context,
	tenantID uuid.UUID,
	attemptID uuid.UUID,
) (checkin.VerificationAttempt, error) {
	return tx.checkins.GetVerificationAttempt(ctx, tenantID, attemptID)
}

func (tx *sqlRecordCheckinTransaction) lockRecordCredential(
	ctx context.Context,
	tenantID uuid.UUID,
	credentialID uuid.UUID,
) (checkin.Credential, error) {
	return tx.checkins.GetCredentialForUpdate(ctx, tenantID, credentialID)
}

func (tx *sqlRecordCheckinTransaction) lockCheckinByRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.Checkin, error) {
	return tx.checkins.GetByRegistrationForUpdate(ctx, tenantID, registrationID)
}

func (tx *sqlRecordCheckinTransaction) lockRecordedCheckin(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
) (checkin.Checkin, error) {
	return tx.checkins.GetForUpdate(ctx, tenantID, checkinID)
}

func (tx *sqlRecordCheckinTransaction) createRecordedCheckin(
	ctx context.Context,
	value checkin.Checkin,
) (checkin.Checkin, error) {
	return tx.checkins.Create(ctx, value)
}

func (tx *sqlRecordCheckinTransaction) createRecordedCheckinEvent(
	ctx context.Context,
	value checkin.Event,
) (checkin.Event, error) {
	return tx.checkins.CreateEvent(ctx, value)
}

func (tx *sqlRecordCheckinTransaction) incrementRecordedCheckinCount(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
	at time.Time,
) error {
	result, err := tx.tx.ExecContext(ctx, `
UPDATE xiangwan_activity_sessions
SET checked_in_registration_count = checked_in_registration_count + 1,
    version = version + 1,
    updated_at = GREATEST(updated_at, $3)
WHERE tenant_id = $1
  AND id = $2
  AND status = 'published'
  AND checked_in_registration_count < confirmed_registration_count
`, tenantID, sessionID, at)
	if err != nil {
		return fmt.Errorf(`increment xiangwan checked-in count: %w`, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(`read xiangwan checked-in count result: %w`, err)
	}
	if affected != 1 {
		return ErrCheckinRecordTransactionConflict
	}
	return nil
}

func (tx *sqlRecordCheckinTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlRecordCheckinTransaction) Rollback() error {
	return tx.tx.Rollback()
}

func classifyRecordCheckinTransactionError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case `23505`, `40001`, `40P01`:
			return fmt.Errorf(`%w: %v`, ErrCheckinRecordTransactionConflict, err)
		}
	}
	return err
}
