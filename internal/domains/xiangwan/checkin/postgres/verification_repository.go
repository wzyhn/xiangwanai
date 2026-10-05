package checkinpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/google/uuid"
)

var ErrVerificationAttemptNotFound = errors.New(
	`xiangwan Checkin verification attempt not found`,
)

const verificationAttemptProjection = `
    id, tenant_id,
    requested_series_id, requested_instance_id, requested_session_id,
    actor_id, presented_kind, decision_code, idempotency_key,
    request_fingerprint,
    credential_id, credential_jti, registration_id, principal_id, checkin_id,
    occurred_at, created_at
`

func (repository *Repository) CreateVerificationAttempt(
	ctx context.Context,
	value checkin.VerificationAttempt,
) (checkin.VerificationAttempt, error) {
	created, err := scanVerificationAttempt(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_checkin_verification_attempts (
    id, tenant_id,
    requested_series_id, requested_instance_id, requested_session_id,
    actor_id, presented_kind, decision_code, idempotency_key,
    request_fingerprint,
    credential_id, credential_jti, registration_id, principal_id, checkin_id,
    occurred_at, created_at
) VALUES (
    $1, $2,
    $3, $4, $5,
    $6, $7, $8, $9,
    $10,
    $11, $12, $13, $14, $15,
    $16, $17
)
RETURNING`+verificationAttemptProjection,
		value.ID,
		value.TenantID,
		value.RequestedSeriesID,
		value.RequestedInstanceID,
		value.RequestedSessionID,
		value.ActorID,
		value.PresentedKind,
		value.Decision,
		value.IdempotencyKey,
		value.RequestFingerprint[:],
		value.CredentialID,
		value.CredentialJTI,
		value.RegistrationID,
		value.PrincipalID,
		value.CheckinID,
		value.OccurredAt,
		value.CreatedAt,
	))
	if err != nil {
		return checkin.VerificationAttempt{}, fmt.Errorf(
			`create xiangwan Checkin verification attempt: %w`,
			err,
		)
	}
	return created, nil
}

func (repository *Repository) GetVerificationAttemptByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	actorID uuid.UUID,
	idempotencyKey string,
) (checkin.VerificationAttempt, error) {
	return repository.getVerificationAttempt(ctx, `
SELECT`+verificationAttemptProjection+`
FROM xiangwan_checkin_verification_attempts
WHERE tenant_id = $1
  AND actor_id = $2
  AND idempotency_key = $3
`, tenantID, actorID, idempotencyKey)
}

func (repository *Repository) GetVerificationAttempt(
	ctx context.Context,
	tenantID uuid.UUID,
	attemptID uuid.UUID,
) (checkin.VerificationAttempt, error) {
	return repository.getVerificationAttempt(ctx, `
SELECT`+verificationAttemptProjection+`
FROM xiangwan_checkin_verification_attempts
WHERE tenant_id = $1 AND id = $2
`, tenantID, attemptID)
}

func (repository *Repository) getVerificationAttempt(
	ctx context.Context,
	query string,
	args ...any,
) (checkin.VerificationAttempt, error) {
	value, err := scanVerificationAttempt(repository.db.queryRowContext(
		ctx,
		query,
		args...,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return checkin.VerificationAttempt{}, ErrVerificationAttemptNotFound
	}
	if err != nil {
		return checkin.VerificationAttempt{}, fmt.Errorf(
			`get xiangwan Checkin verification attempt: %w`,
			err,
		)
	}
	return value, nil
}

func scanVerificationAttempt(row rowScanner) (checkin.VerificationAttempt, error) {
	var value checkin.VerificationAttempt
	var credentialID uuid.NullUUID
	var credentialJTI uuid.NullUUID
	var registrationID uuid.NullUUID
	var principalID uuid.NullUUID
	var checkinID uuid.NullUUID
	var requestFingerprint []byte
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.RequestedSeriesID,
		&value.RequestedInstanceID,
		&value.RequestedSessionID,
		&value.ActorID,
		&value.PresentedKind,
		&value.Decision,
		&value.IdempotencyKey,
		&requestFingerprint,
		&credentialID,
		&credentialJTI,
		&registrationID,
		&principalID,
		&checkinID,
		&value.OccurredAt,
		&value.CreatedAt,
	)
	if err != nil {
		return checkin.VerificationAttempt{}, err
	}
	if len(requestFingerprint) != 0 &&
		len(requestFingerprint) != len(value.RequestFingerprint) {
		return checkin.VerificationAttempt{}, checkin.ErrInvalidVerificationAttempt
	}
	copy(value.RequestFingerprint[:], requestFingerprint)
	value.CredentialID = nullUUIDPointer(credentialID)
	value.CredentialJTI = nullUUIDPointer(credentialJTI)
	value.RegistrationID = nullUUIDPointer(registrationID)
	value.PrincipalID = nullUUIDPointer(principalID)
	value.CheckinID = nullUUIDPointer(checkinID)
	return value, nil
}
