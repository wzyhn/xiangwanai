package checkinpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

const checkinCorrectionMaxAttempts = 25

var (
	ErrInvalidRevokeCheckinCommand = errors.New(
		`invalid xiangwan revoke Checkin command`,
	)
	ErrCheckinRevocationForbidden = errors.New(
		`xiangwan Checkin revocation requires super admin`,
	)
	ErrCheckinRevocationAuthorizationUnavailable = errors.New(
		`xiangwan Checkin revocation authorization is unavailable`,
	)
	ErrCheckinRevocationTargetNotFound = errors.New(
		`xiangwan Checkin revocation target not found`,
	)
	ErrCheckinRevocationVersionConflict = errors.New(
		`xiangwan Checkin revocation version conflict`,
	)
	ErrCheckinAlreadyRevoked = errors.New(
		`xiangwan Checkin is already revoked`,
	)
	ErrCheckinRevocationIdempotencyConflict = errors.New(
		`xiangwan Checkin revocation idempotency conflict`,
	)
	ErrCheckinRevocationGenerationInactive = errors.New(
		`xiangwan Checkin revocation generation is inactive`,
	)
	ErrCheckinRevocationTransactionConflict = errors.New(
		`xiangwan Checkin revocation transaction conflict`,
	)
	ErrCheckinCorrectionOutboxNotFound = errors.New(
		`xiangwan Checkin correction outbox item not found`,
	)
)

type RevokeCheckinCommand struct {
	TenantID        uuid.UUID
	SeriesID        uuid.UUID
	InstanceID      uuid.UUID
	SessionID       uuid.UUID
	RegistrationID  uuid.UUID
	CheckinID       uuid.UUID
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	RequestID       string
	ExpectedVersion int64
	Reason          string
	IdempotencyKey  string
}

type RevokeCheckinResult struct {
	Checkin            checkin.Checkin
	Event              checkin.Event
	CorrectionOutboxID uuid.UUID
	Duplicate          bool
}

type RevokeCheckinAuthorization struct {
	TenantID       uuid.UUID
	SeriesID       uuid.UUID
	InstanceID     uuid.UUID
	SessionID      uuid.UUID
	RegistrationID uuid.UUID
	CheckinID      uuid.UUID
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
}

// CheckinRevocationAuthorizer must verify a current tenant super-admin grant
// through the supplied transaction query boundary. It must return
// ErrCheckinRevocationForbidden for a definitive denial.
type CheckinRevocationAuthorizer interface {
	AuthorizeRevokeCheckin(
		context.Context,
		OperatorAuthorizationQuery,
		RevokeCheckinAuthorization,
	) error
}

// Revoker records the terminal Checkin transition, immutable audit event,
// attendance counter reversal, and durable downstream correction task in one
// serializable PostgreSQL transaction. Brand or activity suspension must not
// prevent this convergence command.
type Revoker struct {
	transactions revokeCheckinTransactionStarter
	generationID uuid.UUID
	now          func() time.Time
	audit        func(context.Context, *sql.Tx, RevokeCheckinCommand, RevokeCheckinResult) error
}

func NewRevoker(
	db *sql.DB,
	authorizer CheckinRevocationAuthorizer,
	generationID uuid.UUID,
) *Revoker {
	return NewRevokerWithAudit(db, authorizer, generationID, nil)
}

func NewRevokerWithAudit(db *sql.DB, authorizer CheckinRevocationAuthorizer, generationID uuid.UUID, audit func(context.Context, *sql.Tx, RevokeCheckinCommand, RevokeCheckinResult) error) *Revoker {
	return &Revoker{
		transactions: sqlRevokeCheckinTransactionStarter{
			db:         db,
			authorizer: authorizer,
		},
		generationID: generationID,
		now:          time.Now,
		audit:        audit,
	}
}

func (revoker *Revoker) Revoke(
	ctx context.Context,
	command RevokeCheckinCommand,
) (RevokeCheckinResult, error) {
	if revoker == nil || revoker.transactions == nil ||
		revoker.generationID == uuid.Nil || revoker.now == nil {
		return RevokeCheckinResult{}, ErrInvalidRevokeCheckinCommand
	}
	normalized, err := normalizeRevokeCheckinCommand(command)
	if err != nil {
		return RevokeCheckinResult{}, err
	}
	command = normalized

	tx, err := revoker.transactions.beginRevokeCheckinTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return RevokeCheckinResult{}, fmt.Errorf(
			`begin xiangwan revoke Checkin transaction: %w`,
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := tx.lockRevokeGeneration(ctx, command.TenantID, revoker.generationID); err != nil {
		return RevokeCheckinResult{}, err
	}
	authorization := RevokeCheckinAuthorization{
		TenantID:       command.TenantID,
		SeriesID:       command.SeriesID,
		InstanceID:     command.InstanceID,
		SessionID:      command.SessionID,
		RegistrationID: command.RegistrationID,
		CheckinID:      command.CheckinID,
		ActorID:        command.ActorID,
		IdentityLinkID: command.IdentityLinkID,
	}
	if err := tx.authorizeRevokeCheckin(ctx, authorization); err != nil {
		if errors.Is(err, ErrCheckinRevocationForbidden) ||
			errors.Is(err, ErrCheckinRevocationAuthorizationUnavailable) {
			return RevokeCheckinResult{}, err
		}
		return RevokeCheckinResult{}, fmt.Errorf(
			`authorize xiangwan revoke Checkin: %w`,
			err,
		)
	}

	replayed, err := tx.getRevocationEventByIdempotencyKey(
		ctx,
		command.TenantID,
		command.ActorID,
		command.IdempotencyKey,
	)
	switch {
	case err == nil:
		return revoker.replayRevocation(
			ctx,
			tx,
			command,
			replayed,
			&committed,
		)
	case !errors.Is(err, ErrCheckinEventNotFound):
		return RevokeCheckinResult{}, err
	}
	if err := tx.lockRevokeSeries(
		ctx,
		command.TenantID,
		command.SeriesID,
	); err != nil {
		return RevokeCheckinResult{}, err
	}
	if err := tx.lockRevokeInstance(
		ctx,
		command.TenantID,
		command.SeriesID,
		command.InstanceID,
	); err != nil {
		return RevokeCheckinResult{}, err
	}
	session, err := tx.lockRevokeSession(
		ctx,
		command.TenantID,
		command.InstanceID,
		command.SessionID,
	)
	if err != nil {
		return RevokeCheckinResult{}, err
	}
	currentRegistration, err := tx.lockRevokeRegistration(
		ctx,
		command.TenantID,
		command.RegistrationID,
	)
	if err != nil {
		return RevokeCheckinResult{}, err
	}
	if !registrationMatchesRevokeCommand(currentRegistration, command) {
		return RevokeCheckinResult{}, ErrCheckinRevocationTargetNotFound
	}
	current, err := tx.lockRevokeCheckin(
		ctx,
		command.TenantID,
		command.CheckinID,
	)
	if err != nil {
		return RevokeCheckinResult{}, err
	}
	if !checkinMatchesRevokeCommand(current, command) ||
		current.PrincipalID != currentRegistration.PrincipalID {
		return RevokeCheckinResult{}, ErrCheckinRevocationTargetNotFound
	}
	if current.CheckinStatus == checkin.StatusRevoked {
		return RevokeCheckinResult{}, ErrCheckinAlreadyRevoked
	}
	if current.Version != command.ExpectedVersion {
		return RevokeCheckinResult{}, ErrCheckinRevocationVersionConflict
	}
	if session.checkedInCount <= 0 {
		return RevokeCheckinResult{}, ErrCheckinRevocationTransactionConflict
	}

	revokedAt := revoker.now().UTC()
	revoked, err := checkin.Revoke(current, checkin.RevokeCommand{
		RevokedBy: command.ActorID,
		Reason:    command.Reason,
		At:        revokedAt,
	})
	if err != nil {
		return RevokeCheckinResult{}, ErrInvalidRevokeCheckinCommand
	}
	event, err := checkin.NewEvent(&current, revoked, command.IdempotencyKey)
	if err != nil {
		return RevokeCheckinResult{}, ErrInvalidRevokeCheckinCommand
	}
	updated, err := tx.updateRevokedCheckin(
		ctx,
		revoked,
		command.ExpectedVersion,
	)
	if errors.Is(err, ErrCheckinVersionConflict) {
		return RevokeCheckinResult{}, ErrCheckinRevocationVersionConflict
	}
	if err != nil {
		return RevokeCheckinResult{}, classifyRevokeCheckinTransactionError(err)
	}
	event.CheckinID = updated.ID
	createdEvent, err := tx.createRevocationEvent(ctx, event)
	if err != nil {
		return RevokeCheckinResult{}, classifyRevokeCheckinTransactionError(err)
	}
	correction := newCheckinCorrectionOutbox(updated, createdEvent)
	createdCorrection, err := tx.createCheckinCorrectionOutbox(
		ctx,
		correction,
	)
	if err != nil {
		return RevokeCheckinResult{}, classifyRevokeCheckinTransactionError(err)
	}
	if err := tx.decrementRevokedCheckinCount(
		ctx,
		command.TenantID,
		command.InstanceID,
		command.SessionID,
		revokedAt,
	); err != nil {
		return RevokeCheckinResult{}, classifyRevokeCheckinTransactionError(err)
	}
	result := RevokeCheckinResult{Checkin: updated, Event: createdEvent, CorrectionOutboxID: createdCorrection.ID}
	if revoker.audit != nil {
		carrier, ok := tx.(*sqlRevokeCheckinTransaction)
		if !ok || command.IdentityLinkID == uuid.Nil || command.RequestID == "" {
			return RevokeCheckinResult{}, ErrInvalidRevokeCheckinCommand
		}
		if err := revoker.audit(ctx, carrier.facts.tx, command, result); err != nil {
			return RevokeCheckinResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return RevokeCheckinResult{}, classifyRevokeCheckinTransactionError(err)
	}
	committed = true
	return result, nil
}

func (revoker *Revoker) replayRevocation(
	ctx context.Context,
	tx revokeCheckinTransaction,
	command RevokeCheckinCommand,
	event checkin.Event,
	committed *bool,
) (RevokeCheckinResult, error) {
	if !revocationEventMatchesCommand(event, command) {
		return RevokeCheckinResult{}, ErrCheckinRevocationIdempotencyConflict
	}
	current, err := tx.getReplayedRevokedCheckin(
		ctx,
		command.TenantID,
		command.CheckinID,
	)
	if errors.Is(err, ErrCheckinNotFound) {
		return RevokeCheckinResult{}, ErrCheckinRevocationIdempotencyConflict
	}
	if err != nil {
		return RevokeCheckinResult{}, err
	}
	if !checkinMatchesRevokeCommand(current, command) ||
		current.CheckinStatus != checkin.StatusRevoked ||
		current.Version != event.ResultingCheckinVersion ||
		current.RevokedBy == nil ||
		*current.RevokedBy != command.ActorID ||
		current.RevocationReason == nil ||
		*current.RevocationReason != command.Reason {
		return RevokeCheckinResult{}, ErrCheckinRevocationIdempotencyConflict
	}
	correction, err := tx.getCheckinCorrectionOutbox(
		ctx,
		command.TenantID,
		command.CheckinID,
	)
	if errors.Is(err, ErrCheckinCorrectionOutboxNotFound) {
		return RevokeCheckinResult{}, ErrCheckinRevocationIdempotencyConflict
	}
	if err != nil {
		return RevokeCheckinResult{}, err
	}
	if !correctionMatchesRevocation(correction, current, event) {
		return RevokeCheckinResult{}, ErrCheckinRevocationIdempotencyConflict
	}
	if err := tx.Commit(); err != nil {
		return RevokeCheckinResult{}, classifyRevokeCheckinTransactionError(err)
	}
	*committed = true
	return RevokeCheckinResult{
		Checkin:            current,
		Event:              event,
		CorrectionOutboxID: correction.ID,
		Duplicate:          true,
	}, nil
}

func normalizeRevokeCheckinCommand(
	command RevokeCheckinCommand,
) (RevokeCheckinCommand, error) {
	command.Reason = strings.TrimSpace(command.Reason)
	if command.TenantID == uuid.Nil ||
		command.SeriesID == uuid.Nil ||
		command.InstanceID == uuid.Nil ||
		command.SessionID == uuid.Nil ||
		command.RegistrationID == uuid.Nil ||
		command.CheckinID == uuid.Nil ||
		command.ActorID == uuid.Nil ||
		command.ExpectedVersion < 1 ||
		command.Reason == `` ||
		len([]rune(command.Reason)) > 500 ||
		!checkinCommandIdempotencyKeyPattern.MatchString(command.IdempotencyKey) {
		return RevokeCheckinCommand{}, ErrInvalidRevokeCheckinCommand
	}
	return command, nil
}

func registrationMatchesRevokeCommand(
	value registration.Registration,
	command RevokeCheckinCommand,
) bool {
	return value.ID == command.RegistrationID &&
		value.TenantID == command.TenantID &&
		value.SeriesID == command.SeriesID &&
		value.InstanceID == command.InstanceID &&
		value.SessionID == command.SessionID
}

func checkinMatchesRevokeCommand(
	value checkin.Checkin,
	command RevokeCheckinCommand,
) bool {
	return checkin.Validate(value) == nil &&
		value.ID == command.CheckinID &&
		value.TenantID == command.TenantID &&
		value.RegistrationID == command.RegistrationID &&
		value.SeriesID == command.SeriesID &&
		value.InstanceID == command.InstanceID &&
		value.SessionID == command.SessionID
}

func revocationEventMatchesCommand(
	value checkin.Event,
	command RevokeCheckinCommand,
) bool {
	return checkin.ValidateEvent(value) == nil &&
		value.TenantID == command.TenantID &&
		value.CheckinID == command.CheckinID &&
		value.RegistrationID == command.RegistrationID &&
		value.SessionID == command.SessionID &&
		value.EventType == checkin.EventTypeRevoked &&
		value.EventSequence == command.ExpectedVersion+1 &&
		value.ResultingCheckinVersion == command.ExpectedVersion+1 &&
		value.ActorID == command.ActorID &&
		value.Reason != nil &&
		*value.Reason == command.Reason
}

type checkinCorrectionOutbox struct {
	ID             uuid.UUID
	TenantID       uuid.UUID
	CheckinID      uuid.UUID
	CheckinEventID uuid.UUID
	RegistrationID uuid.UUID
	SeriesID       uuid.UUID
	InstanceID     uuid.UUID
	SessionID      uuid.UUID
	PrincipalID    uuid.UUID
	OutboxStatus   string
	AvailableAt    time.Time
	AttemptCount   int
	MaxAttempts    int
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func newCheckinCorrectionOutbox(
	revoked checkin.Checkin,
	event checkin.Event,
) checkinCorrectionOutbox {
	return checkinCorrectionOutbox{
		ID:             uuid.New(),
		TenantID:       revoked.TenantID,
		CheckinID:      revoked.ID,
		CheckinEventID: event.ID,
		RegistrationID: revoked.RegistrationID,
		SeriesID:       revoked.SeriesID,
		InstanceID:     revoked.InstanceID,
		SessionID:      revoked.SessionID,
		PrincipalID:    revoked.PrincipalID,
		OutboxStatus:   `pending`,
		AvailableAt:    event.OccurredAt,
		MaxAttempts:    checkinCorrectionMaxAttempts,
		Version:        1,
		CreatedAt:      event.OccurredAt,
		UpdatedAt:      event.OccurredAt,
	}
}

func correctionMatchesRevocation(
	value checkinCorrectionOutbox,
	revoked checkin.Checkin,
	event checkin.Event,
) bool {
	return value.ID != uuid.Nil &&
		value.TenantID == revoked.TenantID &&
		value.CheckinID == revoked.ID &&
		value.CheckinEventID == event.ID &&
		value.RegistrationID == revoked.RegistrationID &&
		value.SeriesID == revoked.SeriesID &&
		value.InstanceID == revoked.InstanceID &&
		value.SessionID == revoked.SessionID &&
		value.PrincipalID == revoked.PrincipalID
}

type revokeCheckinTransactionStarter interface {
	beginRevokeCheckinTx(
		context.Context,
		*sql.TxOptions,
	) (revokeCheckinTransaction, error)
}

type revokeCheckinTransaction interface {
	authorizeRevokeCheckin(context.Context, RevokeCheckinAuthorization) error
	getRevocationEventByIdempotencyKey(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		string,
	) (checkin.Event, error)
	lockRevokeGeneration(context.Context, uuid.UUID, uuid.UUID) error
	lockRevokeSeries(context.Context, uuid.UUID, uuid.UUID) error
	lockRevokeInstance(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) error
	lockRevokeSession(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (recordCheckinSession, error)
	lockRevokeRegistration(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (registration.Registration, error)
	lockRevokeCheckin(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.Checkin, error)
	getReplayedRevokedCheckin(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.Checkin, error)
	updateRevokedCheckin(
		context.Context,
		checkin.Checkin,
		int64,
	) (checkin.Checkin, error)
	createRevocationEvent(context.Context, checkin.Event) (checkin.Event, error)
	createCheckinCorrectionOutbox(
		context.Context,
		checkinCorrectionOutbox,
	) (checkinCorrectionOutbox, error)
	getCheckinCorrectionOutbox(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkinCorrectionOutbox, error)
	decrementRevokedCheckinCount(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) error
	Commit() error
	Rollback() error
}

type sqlRevokeCheckinTransactionStarter struct {
	db         *sql.DB
	authorizer CheckinRevocationAuthorizer
}

func (starter sqlRevokeCheckinTransactionStarter) beginRevokeCheckinTx(
	ctx context.Context,
	options *sql.TxOptions,
) (revokeCheckinTransaction, error) {
	sqlTx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlRevokeCheckinTransaction{
		facts: &sqlRecordCheckinTransaction{
			tx:            sqlTx,
			checkins:      NewRepository(sqlTx),
			registrations: registrationpostgres.NewRepository(sqlTx),
		},
		authorizer: starter.authorizer,
	}, nil
}

type sqlRevokeCheckinTransaction struct {
	facts      *sqlRecordCheckinTransaction
	authorizer CheckinRevocationAuthorizer
}

func (tx *sqlRevokeCheckinTransaction) authorizeRevokeCheckin(
	ctx context.Context,
	request RevokeCheckinAuthorization,
) error {
	if tx.authorizer == nil {
		return ErrCheckinRevocationAuthorizationUnavailable
	}
	return tx.authorizer.AuthorizeRevokeCheckin(ctx, tx.facts.tx, request)
}

func (tx *sqlRevokeCheckinTransaction) getRevocationEventByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	idempotencyKey string,
) (checkin.Event, error) {
	return tx.facts.checkins.GetEventByIdempotencyKey(
		ctx,
		tenantID,
		actorID,
		idempotencyKey,
	)
}

func (tx *sqlRevokeCheckinTransaction) lockRevokeGeneration(
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
		return ErrCheckinRevocationGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan Checkin revocation generation: %w", err)
	}
	return nil
}

func (tx *sqlRevokeCheckinTransaction) lockRevokeSeries(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) error {
	_, err := tx.facts.lockRecordSeries(ctx, tenantID, seriesID)
	return mapCheckinRevocationTargetError(err)
}

func (tx *sqlRevokeCheckinTransaction) lockRevokeInstance(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
) error {
	_, err := tx.facts.lockRecordInstance(
		ctx,
		tenantID,
		seriesID,
		instanceID,
	)
	return mapCheckinRevocationTargetError(err)
}

func (tx *sqlRevokeCheckinTransaction) lockRevokeSession(
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
	return value, mapCheckinRevocationTargetError(err)
}

func (tx *sqlRevokeCheckinTransaction) lockRevokeRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (registration.Registration, error) {
	value, err := tx.facts.lockRecordRegistration(
		ctx,
		tenantID,
		registrationID,
	)
	return value, mapCheckinRevocationTargetError(err)
}

func (tx *sqlRevokeCheckinTransaction) lockRevokeCheckin(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
) (checkin.Checkin, error) {
	value, err := tx.facts.checkins.GetForUpdate(ctx, tenantID, checkinID)
	if errors.Is(err, ErrCheckinNotFound) {
		return checkin.Checkin{}, ErrCheckinRevocationTargetNotFound
	}
	return value, err
}

func (tx *sqlRevokeCheckinTransaction) getReplayedRevokedCheckin(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
) (checkin.Checkin, error) {
	return tx.facts.checkins.Get(ctx, tenantID, checkinID)
}

func (tx *sqlRevokeCheckinTransaction) updateRevokedCheckin(
	ctx context.Context,
	value checkin.Checkin,
	expectedVersion int64,
) (checkin.Checkin, error) {
	return tx.facts.checkins.Update(ctx, value, expectedVersion)
}

func (tx *sqlRevokeCheckinTransaction) createRevocationEvent(
	ctx context.Context,
	value checkin.Event,
) (checkin.Event, error) {
	return tx.facts.checkins.CreateEvent(ctx, value)
}

const checkinCorrectionOutboxProjection = `
    id, tenant_id, checkin_id, checkin_event_id,
    registration_id, series_id, instance_id, session_id, principal_id,
    outbox_status, available_at, attempt_count, max_attempts,
    version, created_at, updated_at
`

func (tx *sqlRevokeCheckinTransaction) createCheckinCorrectionOutbox(
	ctx context.Context,
	value checkinCorrectionOutbox,
) (checkinCorrectionOutbox, error) {
	created, err := scanCheckinCorrectionOutbox(tx.facts.tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_checkin_correction_outbox (
    id, tenant_id, checkin_id, checkin_event_id,
    registration_id, series_id, instance_id, session_id, principal_id,
    outbox_status, available_at, attempt_count, max_attempts,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9,
    $10, $11, $12, $13,
    $14, $15, $16
)
RETURNING`+checkinCorrectionOutboxProjection,
		value.ID,
		value.TenantID,
		value.CheckinID,
		value.CheckinEventID,
		value.RegistrationID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.OutboxStatus,
		value.AvailableAt,
		value.AttemptCount,
		value.MaxAttempts,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return checkinCorrectionOutbox{}, fmt.Errorf(
			`create xiangwan Checkin correction outbox item: %w`,
			err,
		)
	}
	return created, nil
}

func (tx *sqlRevokeCheckinTransaction) getCheckinCorrectionOutbox(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
) (checkinCorrectionOutbox, error) {
	value, err := scanCheckinCorrectionOutbox(tx.facts.tx.QueryRowContext(ctx, `
SELECT`+checkinCorrectionOutboxProjection+`
FROM xiangwan_checkin_correction_outbox
WHERE tenant_id = $1 AND checkin_id = $2
`, tenantID, checkinID))
	if errors.Is(err, sql.ErrNoRows) {
		return checkinCorrectionOutbox{}, ErrCheckinCorrectionOutboxNotFound
	}
	if err != nil {
		return checkinCorrectionOutbox{}, fmt.Errorf(
			`get xiangwan Checkin correction outbox item: %w`,
			err,
		)
	}
	return value, nil
}

func scanCheckinCorrectionOutbox(
	row rowScanner,
) (checkinCorrectionOutbox, error) {
	var value checkinCorrectionOutbox
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.CheckinID,
		&value.CheckinEventID,
		&value.RegistrationID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PrincipalID,
		&value.OutboxStatus,
		&value.AvailableAt,
		&value.AttemptCount,
		&value.MaxAttempts,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	return value, err
}

func (tx *sqlRevokeCheckinTransaction) decrementRevokedCheckinCount(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
	at time.Time,
) error {
	result, err := tx.facts.tx.ExecContext(ctx, `
UPDATE xiangwan_activity_sessions
SET checked_in_registration_count = checked_in_registration_count - 1,
    version = version + 1,
    updated_at = GREATEST(updated_at, $4)
WHERE tenant_id = $1
  AND instance_id = $2
  AND id = $3
  AND checked_in_registration_count > 0
`, tenantID, instanceID, sessionID, at)
	if err != nil {
		return fmt.Errorf(`decrement xiangwan checked-in count: %w`, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(`read xiangwan checked-in decrement result: %w`, err)
	}
	if affected != 1 {
		return ErrCheckinRevocationTransactionConflict
	}
	return nil
}

func (tx *sqlRevokeCheckinTransaction) Commit() error {
	return tx.facts.tx.Commit()
}

func (tx *sqlRevokeCheckinTransaction) Rollback() error {
	return tx.facts.tx.Rollback()
}

func mapCheckinRevocationTargetError(err error) error {
	if errors.Is(err, ErrCheckinRecordTargetNotFound) {
		return ErrCheckinRevocationTargetNotFound
	}
	return err
}

func classifyRevokeCheckinTransactionError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case `23505`, `40001`, `40P01`:
			return fmt.Errorf(
				`%w: %v`,
				ErrCheckinRevocationTransactionConflict,
				err,
			)
		}
	}
	return err
}
