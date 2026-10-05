// Package checkinpostgres persists Xiangwan Checkin facts and immutable
// lifecycle events in the customer PostgreSQL database.
package checkinpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/google/uuid"
)

var (
	ErrCheckinNotFound        = errors.New(`xiangwan Checkin not found`)
	ErrCheckinVersionConflict = errors.New(`xiangwan Checkin version conflict`)
	ErrCheckinEventNotFound   = errors.New(`xiangwan Checkin event not found`)
)

type DBTX interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
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

type rowsScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

type queryExecutor interface {
	queryContext(context.Context, string, ...any) (rowsScanner, error)
	queryRowContext(context.Context, string, ...any) rowScanner
}

type sqlQueryExecutor struct {
	db DBTX
}

func (executor sqlQueryExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return executor.db.QueryContext(ctx, query, args...)
}

func (executor sqlQueryExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return executor.db.QueryRowContext(ctx, query, args...)
}

const checkinProjection = `
    id, tenant_id, registration_id, series_id, instance_id, session_id,
    principal_id, checkin_status, checked_in_by, checked_in_at,
    revoked_by, revoked_at, revocation_reason,
    version, created_at, updated_at
`

const checkinEventProjection = `
    id, tenant_id, checkin_id, registration_id, session_id,
    event_sequence, event_type, idempotency_key,
    from_status, to_status, actor_id, reason,
    occurred_at, resulting_checkin_version, created_at
`

func (repository *Repository) Create(
	ctx context.Context,
	value checkin.Checkin,
) (checkin.Checkin, error) {
	created, err := scanCheckin(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_checkins (
    id, tenant_id, registration_id, series_id, instance_id, session_id,
    principal_id, checkin_status, checked_in_by, checked_in_at,
    revoked_by, revoked_at, revocation_reason,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13,
    $14, $15, $16
)
RETURNING`+checkinProjection,
		value.ID,
		value.TenantID,
		value.RegistrationID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.CheckinStatus,
		value.CheckedInBy,
		value.CheckedInAt,
		value.RevokedBy,
		value.RevokedAt,
		value.RevocationReason,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return checkin.Checkin{}, fmt.Errorf(`create xiangwan Checkin: %w`, err)
	}
	return created, nil
}

func (repository *Repository) Get(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
) (checkin.Checkin, error) {
	return repository.get(ctx, `
SELECT`+checkinProjection+`
FROM xiangwan_checkins
WHERE tenant_id = $1 AND id = $2
`, tenantID, checkinID)
}

func (repository *Repository) GetForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
) (checkin.Checkin, error) {
	return repository.get(ctx, `
SELECT`+checkinProjection+`
FROM xiangwan_checkins
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, checkinID)
}

func (repository *Repository) GetByRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.Checkin, error) {
	return repository.getByRegistration(ctx, tenantID, registrationID, false)
}

func (repository *Repository) GetByRegistrationForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.Checkin, error) {
	return repository.getByRegistration(ctx, tenantID, registrationID, true)
}

func (repository *Repository) Update(
	ctx context.Context,
	value checkin.Checkin,
	expectedVersion int64,
) (checkin.Checkin, error) {
	updated, err := scanCheckin(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_checkins
SET checkin_status = $3,
    revoked_by = $4,
    revoked_at = $5,
    revocation_reason = $6,
    version = version + 1,
    updated_at = $7
WHERE tenant_id = $1
  AND id = $2
  AND version = $8
RETURNING`+checkinProjection,
		value.TenantID,
		value.ID,
		value.CheckinStatus,
		value.RevokedBy,
		value.RevokedAt,
		value.RevocationReason,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return checkin.Checkin{}, ErrCheckinVersionConflict
	}
	if err != nil {
		return checkin.Checkin{}, fmt.Errorf(`update xiangwan Checkin: %w`, err)
	}
	return updated, nil
}

func (repository *Repository) CreateEvent(
	ctx context.Context,
	value checkin.Event,
) (checkin.Event, error) {
	created, err := scanCheckinEvent(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_checkin_events (
    id, tenant_id, checkin_id, registration_id, session_id,
    event_sequence, event_type, idempotency_key,
    from_status, to_status, actor_id, reason,
    occurred_at, resulting_checkin_version, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11, $12,
    $13, $14, $15
)
RETURNING`+checkinEventProjection,
		value.ID,
		value.TenantID,
		value.CheckinID,
		value.RegistrationID,
		value.SessionID,
		value.EventSequence,
		value.EventType,
		value.IdempotencyKey,
		value.FromStatus,
		value.ToStatus,
		value.ActorID,
		value.Reason,
		value.OccurredAt,
		value.ResultingCheckinVersion,
		value.CreatedAt,
	))
	if err != nil {
		return checkin.Event{}, fmt.Errorf(`create xiangwan Checkin event: %w`, err)
	}
	return created, nil
}

func (repository *Repository) GetEventByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	idempotencyKey string,
) (checkin.Event, error) {
	return repository.getEvent(ctx, `
SELECT`+checkinEventProjection+`
FROM xiangwan_checkin_events
WHERE tenant_id = $1
  AND actor_id = $2
  AND idempotency_key = $3
`, tenantID, actorID, idempotencyKey)
}

// ListEvents caps the immutable timeline at the version of the current fact
// already read by the caller, so a concurrent revocation cannot appear ahead
// of that projection.
func (repository *Repository) ListEvents(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
	maxVersion int64,
) ([]checkin.Event, error) {
	rows, err := repository.db.queryContext(ctx, `
SELECT`+checkinEventProjection+`
FROM xiangwan_checkin_events
WHERE tenant_id = $1
  AND checkin_id = $2
  AND resulting_checkin_version <= $3
ORDER BY event_sequence ASC
`, tenantID, checkinID, maxVersion)
	if err != nil {
		return nil, fmt.Errorf(`list xiangwan Checkin events: %w`, err)
	}
	defer func() {
		_ = rows.Close()
	}()

	values := make([]checkin.Event, 0, 2)
	for rows.Next() {
		value, scanErr := scanCheckinEvent(rows)
		if scanErr != nil {
			return nil, fmt.Errorf(`scan xiangwan Checkin event: %w`, scanErr)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(`iterate xiangwan Checkin events: %w`, err)
	}
	return values, nil
}

func (repository *Repository) getByRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
	forUpdate bool,
) (checkin.Checkin, error) {
	query := `
SELECT` + checkinProjection + `
FROM xiangwan_checkins
WHERE tenant_id = $1 AND registration_id = $2
`
	if forUpdate {
		query += `FOR UPDATE
`
	}
	return repository.get(ctx, query, tenantID, registrationID)
}

func (repository *Repository) get(
	ctx context.Context,
	query string,
	args ...any,
) (checkin.Checkin, error) {
	value, err := scanCheckin(repository.db.queryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return checkin.Checkin{}, ErrCheckinNotFound
	}
	if err != nil {
		return checkin.Checkin{}, fmt.Errorf(`get xiangwan Checkin: %w`, err)
	}
	return value, nil
}

func (repository *Repository) getEvent(
	ctx context.Context,
	query string,
	args ...any,
) (checkin.Event, error) {
	value, err := scanCheckinEvent(repository.db.queryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return checkin.Event{}, ErrCheckinEventNotFound
	}
	if err != nil {
		return checkin.Event{}, fmt.Errorf(`get xiangwan Checkin event: %w`, err)
	}
	return value, nil
}

func scanCheckin(row rowScanner) (checkin.Checkin, error) {
	var value checkin.Checkin
	var revokedBy uuid.NullUUID
	var revokedAt sql.NullTime
	var revocationReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.RegistrationID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PrincipalID,
		&value.CheckinStatus,
		&value.CheckedInBy,
		&value.CheckedInAt,
		&revokedBy,
		&revokedAt,
		&revocationReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return checkin.Checkin{}, err
	}
	value.RevokedBy = nullUUIDPointer(revokedBy)
	value.RevokedAt = nullTimePointer(revokedAt)
	value.RevocationReason = nullStringPointer(revocationReason)
	return value, nil
}

func scanCheckinEvent(row rowScanner) (checkin.Event, error) {
	var value checkin.Event
	var fromStatus sql.NullString
	var reason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.CheckinID,
		&value.RegistrationID,
		&value.SessionID,
		&value.EventSequence,
		&value.EventType,
		&value.IdempotencyKey,
		&fromStatus,
		&value.ToStatus,
		&value.ActorID,
		&reason,
		&value.OccurredAt,
		&value.ResultingCheckinVersion,
		&value.CreatedAt,
	)
	if err != nil {
		return checkin.Event{}, err
	}
	if fromStatus.Valid {
		status := checkin.Status(fromStatus.String)
		value.FromStatus = &status
	}
	value.Reason = nullStringPointer(reason)
	return value, nil
}

func nullUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	return &value.UUID
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
