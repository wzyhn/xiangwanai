package checkinpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvalidIssueRegistrationCredentialCommand = errors.New(
		`invalid xiangwan issue Registration credential command`,
	)
	ErrRegistrationCredentialUnavailable = errors.New(
		`xiangwan Registration credential unavailable`,
	)
	ErrRegistrationCredentialRateLimited = errors.New(
		`xiangwan Registration credential was issued recently`,
	)
	ErrRegistrationCredentialGenerationInactive = errors.New(
		`xiangwan Registration credential generation is inactive`,
	)
	ErrRegistrationCredentialTransactionConflict = errors.New(
		`xiangwan Registration credential transaction conflict`,
	)
)

const (
	credentialRotationReason  = `reissued`
	credentialReissueInterval = 5 * time.Second
	// CredentialReissueRetryAfterSeconds is the stable client backoff floor.
	CredentialReissueRetryAfterSeconds = int(credentialReissueInterval / time.Second)
)

type IssueRegistrationCredentialCommand struct {
	TenantID       uuid.UUID
	RegistrationID uuid.UUID
	PrincipalID    uuid.UUID
	TTL            time.Duration
}

// CredentialIssuer implements the EPHEMERAL-ISSUE contract. Each successful
// call creates new one-time plaintext and atomically makes every older
// credential unusable. PostgreSQL receives only digests, scope, epoch, expiry,
// and the terminal rotation fact.
type CredentialIssuer struct {
	transactions issueCredentialTransactionStarter
	protector    *checkin.CredentialProtector
	generationID uuid.UUID
}

func NewCredentialIssuer(
	db *sql.DB,
	protector *checkin.CredentialProtector,
	generationID uuid.UUID,
) *CredentialIssuer {
	return &CredentialIssuer{
		transactions: sqlIssueCredentialTransactionStarter{db: db},
		protector:    protector,
		generationID: generationID,
	}
}

func (issuer *CredentialIssuer) Issue(
	ctx context.Context,
	command IssueRegistrationCredentialCommand,
) (checkin.IssuedCredential, error) {
	if err := validateIssueRegistrationCredentialCommand(issuer, command); err != nil {
		return checkin.IssuedCredential{}, err
	}
	tx, err := issuer.transactions.beginIssueCredentialTx(
		ctx,
		&sql.TxOptions{Isolation: sql.LevelSerializable},
	)
	if err != nil {
		return checkin.IssuedCredential{}, fmt.Errorf(
			`begin xiangwan Registration credential transaction: %w`,
			err,
		)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	resolved, err := tx.getIssueCredentialRegistration(
		ctx,
		command.TenantID,
		command.RegistrationID,
	)
	if errors.Is(err, registrationpostgres.ErrRegistrationNotFound) {
		return checkin.IssuedCredential{}, ErrRegistrationCredentialUnavailable
	}
	if err != nil {
		return checkin.IssuedCredential{}, err
	}
	if resolved.PrincipalID != command.PrincipalID {
		return checkin.IssuedCredential{}, ErrRegistrationCredentialUnavailable
	}
	if err := tx.lockActiveGeneration(
		ctx,
		command.TenantID,
		issuer.generationID,
	); err != nil {
		return checkin.IssuedCredential{}, err
	}

	seriesStatus, err := tx.lockIssueCredentialSeries(
		ctx,
		resolved.TenantID,
		resolved.SeriesID,
	)
	if err != nil {
		return checkin.IssuedCredential{}, err
	}
	instanceStatus, err := tx.lockIssueCredentialInstance(
		ctx,
		resolved.TenantID,
		resolved.SeriesID,
		resolved.InstanceID,
	)
	if err != nil {
		return checkin.IssuedCredential{}, err
	}
	session, err := tx.lockIssueCredentialSession(
		ctx,
		resolved.TenantID,
		resolved.InstanceID,
		resolved.SessionID,
	)
	if err != nil {
		return checkin.IssuedCredential{}, err
	}
	current, err := tx.lockIssueCredentialRegistration(
		ctx,
		command.TenantID,
		command.RegistrationID,
	)
	if errors.Is(err, registrationpostgres.ErrRegistrationNotFound) {
		return checkin.IssuedCredential{},
			ErrRegistrationCredentialTransactionConflict
	}
	if err != nil {
		return checkin.IssuedCredential{}, err
	}
	if !sameRegistrationIdentity(current, resolved) {
		return checkin.IssuedCredential{},
			ErrRegistrationCredentialTransactionConflict
	}
	issuedAt, err := tx.currentTime(ctx)
	if err != nil {
		return checkin.IssuedCredential{},
			classifyIssueCredentialWriteError(err)
	}
	issuedAt = issuedAt.UTC().Truncate(time.Microsecond)
	if issuedAt.IsZero() {
		return checkin.IssuedCredential{},
			ErrRegistrationCredentialTransactionConflict
	}
	if !registrationCanIssueCredential(
		current,
		command.PrincipalID,
		seriesStatus,
		instanceStatus,
		session,
		issuedAt,
	) {
		return checkin.IssuedCredential{}, ErrRegistrationCredentialUnavailable
	}

	ttl := command.TTL
	untilSessionEnd := session.endAt.Sub(issuedAt)
	if untilSessionEnd < ttl {
		ttl = untilSessionEnd
	}
	if ttl <= 0 {
		return checkin.IssuedCredential{}, ErrRegistrationCredentialUnavailable
	}

	epoch := int64(1)
	latest, err := tx.getLatestIssueCredentialForUpdate(
		ctx,
		command.TenantID,
		command.RegistrationID,
	)
	switch {
	case errors.Is(err, ErrCredentialNotFound):
	case err != nil:
		return checkin.IssuedCredential{}, err
	default:
		if checkin.ValidateCredential(latest) != nil ||
			!credentialMatchesRegistration(latest, current) ||
			latest.CredentialEpoch == math.MaxInt64 ||
			issuedAt.Before(latest.UpdatedAt) {
			return checkin.IssuedCredential{},
				ErrRegistrationCredentialTransactionConflict
		}
		if issuedAt.Sub(latest.IssuedAt) < credentialReissueInterval {
			return checkin.IssuedCredential{},
				ErrRegistrationCredentialRateLimited
		}
		epoch = latest.CredentialEpoch + 1
		if latest.CredentialStatus == checkin.CredentialStatusActive {
			revoked, revokeErr := checkin.RevokeCredential(
				latest,
				credentialRotationReason,
				issuedAt,
			)
			if revokeErr != nil {
				return checkin.IssuedCredential{},
					ErrRegistrationCredentialTransactionConflict
			}
			persistedRevocation, revokeErr := tx.revokeIssuedCredential(
				ctx,
				revoked,
				latest.Version,
			)
			if revokeErr != nil {
				return checkin.IssuedCredential{},
					classifyIssueCredentialWriteError(revokeErr)
			}
			if !sameCredential(persistedRevocation, revoked) {
				return checkin.IssuedCredential{},
					ErrRegistrationCredentialTransactionConflict
			}
		}
	}

	issued, err := issuer.protector.Issue(checkin.IssueCredentialCommand{
		TenantID:       current.TenantID,
		RegistrationID: current.ID,
		SeriesID:       current.SeriesID,
		InstanceID:     current.InstanceID,
		SessionID:      current.SessionID,
		PrincipalID:    current.PrincipalID,
		Epoch:          epoch,
		TTL:            ttl,
		At:             issuedAt,
	})
	if err != nil {
		return checkin.IssuedCredential{}, err
	}
	created, err := tx.createIssuedCredential(ctx, issued.Credential)
	if err != nil {
		return checkin.IssuedCredential{},
			classifyIssueCredentialWriteError(err)
	}
	if !sameCredential(created, issued.Credential) {
		return checkin.IssuedCredential{},
			ErrRegistrationCredentialTransactionConflict
	}
	issued.Credential = created

	if err := tx.Commit(); err != nil {
		return checkin.IssuedCredential{},
			classifyIssueCredentialWriteError(err)
	}
	committed = true
	return issued, nil
}

func validateIssueRegistrationCredentialCommand(
	issuer *CredentialIssuer,
	command IssueRegistrationCredentialCommand,
) error {
	if issuer == nil ||
		issuer.transactions == nil ||
		issuer.protector == nil ||
		issuer.generationID == uuid.Nil ||
		command.TenantID == uuid.Nil ||
		command.RegistrationID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		command.TTL <= 0 ||
		command.TTL > checkin.MaxCredentialTTL {
		return ErrInvalidIssueRegistrationCredentialCommand
	}
	return nil
}

func sameRegistrationIdentity(
	left registration.Registration,
	right registration.Registration,
) bool {
	return left.ID == right.ID &&
		left.TenantID == right.TenantID &&
		left.SeriesID == right.SeriesID &&
		left.InstanceID == right.InstanceID &&
		left.SessionID == right.SessionID &&
		left.PrincipalID == right.PrincipalID
}

func registrationCanIssueCredential(
	value registration.Registration,
	principalID uuid.UUID,
	seriesStatus activity.SeriesStatus,
	instanceStatus activity.InstanceStatus,
	session issueCredentialSession,
	at time.Time,
) bool {
	return value.PrincipalID == principalID &&
		value.ParticipationStatus == registration.ParticipationStatusConfirmed &&
		value.ConfirmedAt != nil &&
		!at.Before(*value.ConfirmedAt) &&
		seriesStatus == activity.SeriesStatusActive &&
		instanceStatus == activity.InstanceStatusPublished &&
		session.status == activity.SessionStatusPublished &&
		!session.endAt.IsZero() &&
		at.Before(session.endAt)
}

func credentialMatchesRegistration(
	value checkin.Credential,
	current registration.Registration,
) bool {
	return value.TenantID == current.TenantID &&
		value.RegistrationID == current.ID &&
		value.SeriesID == current.SeriesID &&
		value.InstanceID == current.InstanceID &&
		value.SessionID == current.SessionID &&
		value.PrincipalID == current.PrincipalID
}

func sameCredential(left checkin.Credential, right checkin.Credential) bool {
	return checkin.ValidateCredential(left) == nil &&
		left.ID == right.ID &&
		left.TenantID == right.TenantID &&
		left.RegistrationID == right.RegistrationID &&
		left.SeriesID == right.SeriesID &&
		left.InstanceID == right.InstanceID &&
		left.SessionID == right.SessionID &&
		left.PrincipalID == right.PrincipalID &&
		left.CredentialJTI == right.CredentialJTI &&
		left.QRTokenHash == right.QRTokenHash &&
		left.BackupCodeHash == right.BackupCodeHash &&
		left.CredentialEpoch == right.CredentialEpoch &&
		left.CredentialStatus == right.CredentialStatus &&
		left.IssuedAt.Equal(right.IssuedAt) &&
		left.ExpiresAt.Equal(right.ExpiresAt) &&
		sameOptionalTime(left.RevokedAt, right.RevokedAt) &&
		sameOptionalString(left.RevocationReason, right.RevocationReason) &&
		left.Version == right.Version &&
		left.CreatedAt.Equal(right.CreatedAt) &&
		left.UpdatedAt.Equal(right.UpdatedAt)
}

func sameOptionalTime(left *time.Time, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func sameOptionalString(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

type issueCredentialSession struct {
	status activity.SessionStatus
	endAt  time.Time
}

type issueCredentialTransactionStarter interface {
	beginIssueCredentialTx(
		context.Context,
		*sql.TxOptions,
	) (issueCredentialTransaction, error)
}

type issueCredentialTransaction interface {
	lockActiveGeneration(context.Context, uuid.UUID, uuid.UUID) error
	currentTime(context.Context) (time.Time, error)
	getIssueCredentialRegistration(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (registration.Registration, error)
	lockIssueCredentialSeries(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (activity.SeriesStatus, error)
	lockIssueCredentialInstance(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (activity.InstanceStatus, error)
	lockIssueCredentialSession(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
	) (issueCredentialSession, error)
	lockIssueCredentialRegistration(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (registration.Registration, error)
	getLatestIssueCredentialForUpdate(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (checkin.Credential, error)
	revokeIssuedCredential(
		context.Context,
		checkin.Credential,
		int64,
	) (checkin.Credential, error)
	createIssuedCredential(
		context.Context,
		checkin.Credential,
	) (checkin.Credential, error)
	Commit() error
	Rollback() error
}

type sqlIssueCredentialTransactionStarter struct {
	db *sql.DB
}

func (starter sqlIssueCredentialTransactionStarter) beginIssueCredentialTx(
	ctx context.Context,
	options *sql.TxOptions,
) (issueCredentialTransaction, error) {
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlIssueCredentialTransaction{
		tx:            tx,
		credentials:   NewRepository(tx),
		registrations: registrationpostgres.NewRepository(tx),
	}, nil
}

type sqlIssueCredentialTransaction struct {
	tx            *sql.Tx
	credentials   *Repository
	registrations *registrationpostgres.Repository
}

func (tx *sqlIssueCredentialTransaction) lockActiveGeneration(
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
		return ErrRegistrationCredentialGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("lock xiangwan credential generation: %w", err)
	}
	return nil
}

func (tx *sqlIssueCredentialTransaction) currentTime(
	ctx context.Context,
) (time.Time, error) {
	var current time.Time
	if err := tx.tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&current); err != nil {
		return time.Time{}, fmt.Errorf("read xiangwan credential transaction time: %w", err)
	}
	return current, nil
}

func (tx *sqlIssueCredentialTransaction) getIssueCredentialRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (registration.Registration, error) {
	return tx.registrations.Get(ctx, tenantID, registrationID)
}

func (tx *sqlIssueCredentialTransaction) lockIssueCredentialSeries(
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
		return ``, ErrRegistrationCredentialUnavailable
	}
	if err != nil {
		return ``, fmt.Errorf(
			`lock xiangwan credential Series: %w`,
			err,
		)
	}
	return status, nil
}

func (tx *sqlIssueCredentialTransaction) lockIssueCredentialInstance(
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
		return ``, ErrRegistrationCredentialUnavailable
	}
	if err != nil {
		return ``, fmt.Errorf(
			`lock xiangwan credential Instance: %w`,
			err,
		)
	}
	return status, nil
}

func (tx *sqlIssueCredentialTransaction) lockIssueCredentialSession(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.UUID,
) (issueCredentialSession, error) {
	var value issueCredentialSession
	var endAt sql.NullTime
	err := tx.tx.QueryRowContext(ctx, `
SELECT status, session_end_at
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND instance_id = $2 AND id = $3
FOR UPDATE
`, tenantID, instanceID, sessionID).Scan(&value.status, &endAt)
	if errors.Is(err, sql.ErrNoRows) {
		return issueCredentialSession{}, ErrRegistrationCredentialUnavailable
	}
	if err != nil {
		return issueCredentialSession{}, fmt.Errorf(
			`lock xiangwan credential Session: %w`,
			err,
		)
	}
	if endAt.Valid {
		value.endAt = endAt.Time.UTC()
	}
	return value, nil
}

func (tx *sqlIssueCredentialTransaction) lockIssueCredentialRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (registration.Registration, error) {
	return tx.registrations.GetForUpdate(ctx, tenantID, registrationID)
}

func (tx *sqlIssueCredentialTransaction) getLatestIssueCredentialForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.Credential, error) {
	return tx.credentials.GetLatestCredentialByRegistrationForUpdate(
		ctx,
		tenantID,
		registrationID,
	)
}

func (tx *sqlIssueCredentialTransaction) revokeIssuedCredential(
	ctx context.Context,
	value checkin.Credential,
	expectedVersion int64,
) (checkin.Credential, error) {
	return tx.credentials.RevokeCredential(ctx, value, expectedVersion)
}

func (tx *sqlIssueCredentialTransaction) createIssuedCredential(
	ctx context.Context,
	value checkin.Credential,
) (checkin.Credential, error) {
	return tx.credentials.CreateCredential(ctx, value)
}

func (tx *sqlIssueCredentialTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlIssueCredentialTransaction) Rollback() error {
	return tx.tx.Rollback()
}

func classifyIssueCredentialWriteError(err error) error {
	if errors.Is(err, ErrCredentialVersionConflict) {
		return fmt.Errorf(
			`%w: %v`,
			ErrRegistrationCredentialTransactionConflict,
			err,
		)
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case `23503`, `23505`, `23514`, `40001`, `40P01`:
			return fmt.Errorf(
				`%w: %v`,
				ErrRegistrationCredentialTransactionConflict,
				err,
			)
		}
	}
	return err
}
