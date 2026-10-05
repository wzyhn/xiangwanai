// Package registrationpostgres persists Xiangwan Registration participation
// facts in the customer PostgreSQL database.
package registrationpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

var (
	ErrRegistrationNotFound        = errors.New("xiangwan Registration not found")
	ErrRegistrationVersionConflict = errors.New("xiangwan Registration version conflict")
)

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Repository struct {
	db queryExecutor
}

func NewRepository(db DBTX) *Repository {
	return &Repository{db: sqlQueryExecutor{db: db}}
}

type rowScanner interface {
	Scan(...any) error
}

type queryExecutor interface {
	queryRowContext(context.Context, string, ...any) rowScanner
}

type sqlQueryExecutor struct {
	db DBTX
}

func (executor sqlQueryExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return executor.db.QueryRowContext(ctx, query, args...)
}

func (repository *Repository) Create(
	ctx context.Context,
	value registration.Registration,
) (registration.Registration, error) {
	created, err := scanRegistration(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_registrations (
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8,
    $9, $10, $11,
    $12, $13, $14
)
RETURNING
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
`,
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.ParticipationStatus,
		value.IdempotencyKey,
		value.ConfirmedAt,
		value.CancelledAt,
		value.CancellationReason,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return registration.Registration{}, fmt.Errorf("create xiangwan Registration: %w", err)
	}
	return created, nil
}

func (repository *Repository) Get(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (registration.Registration, error) {
	return repository.get(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
FROM xiangwan_registrations
WHERE tenant_id = $1 AND id = $2
`, tenantID, registrationID)
}

func (repository *Repository) GetForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (registration.Registration, error) {
	return repository.get(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
FROM xiangwan_registrations
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, registrationID)
}

func (repository *Repository) GetByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	idempotencyKey string,
) (registration.Registration, error) {
	return repository.get(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
FROM xiangwan_registrations
WHERE tenant_id = $1 AND idempotency_key = $2
`, tenantID, idempotencyKey)
}

func (repository *Repository) GetOpenByPrincipalSession(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	sessionID uuid.UUID,
) (registration.Registration, error) {
	return repository.get(ctx, `
SELECT
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
FROM xiangwan_registrations
WHERE tenant_id = $1
  AND principal_id = $2
  AND session_id = $3
  AND participation_status IN ('pending_payment', 'confirmed')
`, tenantID, principalID, sessionID)
}

func (repository *Repository) UpdateParticipation(
	ctx context.Context,
	value registration.Registration,
	expectedVersion int64,
) (registration.Registration, error) {
	updated, err := scanRegistration(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_registrations
SET participation_status = $3,
    confirmed_at = $4,
    cancelled_at = $5,
    cancellation_reason = $6,
    version = version + 1,
    updated_at = $7
WHERE tenant_id = $1
  AND id = $2
  AND version = $8
RETURNING
    id, tenant_id, series_id, instance_id, session_id, principal_id,
    participation_status, idempotency_key,
    confirmed_at, cancelled_at, cancellation_reason,
    version, created_at, updated_at
`,
		value.TenantID,
		value.ID,
		value.ParticipationStatus,
		value.ConfirmedAt,
		value.CancelledAt,
		value.CancellationReason,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return registration.Registration{}, ErrRegistrationVersionConflict
	}
	if err != nil {
		return registration.Registration{}, fmt.Errorf("update xiangwan Registration participation: %w", err)
	}
	return updated, nil
}

func (repository *Repository) get(
	ctx context.Context,
	query string,
	args ...any,
) (registration.Registration, error) {
	value, err := scanRegistration(repository.db.queryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return registration.Registration{}, ErrRegistrationNotFound
	}
	if err != nil {
		return registration.Registration{}, fmt.Errorf("get xiangwan Registration: %w", err)
	}
	return value, nil
}

func scanRegistration(row rowScanner) (registration.Registration, error) {
	var value registration.Registration
	var confirmedAt sql.NullTime
	var cancelledAt sql.NullTime
	var cancellationReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PrincipalID,
		&value.ParticipationStatus,
		&value.IdempotencyKey,
		&confirmedAt,
		&cancelledAt,
		&cancellationReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return registration.Registration{}, err
	}
	value.ConfirmedAt = nullTimePointer(confirmedAt)
	value.CancelledAt = nullTimePointer(cancelledAt)
	value.CancellationReason = nullStringPointer(cancellationReason)
	return value, nil
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}
