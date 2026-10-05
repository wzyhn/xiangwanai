package checkinpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/google/uuid"
)

var (
	ErrCredentialNotFound        = errors.New(`xiangwan Checkin credential not found`)
	ErrCredentialVersionConflict = errors.New(`xiangwan Checkin credential version conflict`)
)

const credentialProjection = `
    id, tenant_id, registration_id, series_id, instance_id, session_id,
    principal_id, credential_jti, qr_token_hash, backup_code_hash,
    credential_epoch, credential_status, issued_at, expires_at,
    revoked_at, revocation_reason, version, created_at, updated_at
`

func (repository *Repository) CreateCredential(
	ctx context.Context,
	value checkin.Credential,
) (checkin.Credential, error) {
	created, err := scanCredential(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_checkin_credentials (
    id, tenant_id, registration_id, series_id, instance_id, session_id,
    principal_id, credential_jti, qr_token_hash, backup_code_hash,
    credential_epoch, credential_status, issued_at, expires_at,
    revoked_at, revocation_reason, version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16, $17, $18, $19
)
RETURNING`+credentialProjection,
		value.ID,
		value.TenantID,
		value.RegistrationID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.CredentialJTI,
		value.QRTokenHash[:],
		value.BackupCodeHash[:],
		value.CredentialEpoch,
		value.CredentialStatus,
		value.IssuedAt,
		value.ExpiresAt,
		value.RevokedAt,
		value.RevocationReason,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return checkin.Credential{}, fmt.Errorf(`create xiangwan Checkin credential: %w`, err)
	}
	return created, nil
}

func (repository *Repository) GetCredential(
	ctx context.Context,
	tenantID uuid.UUID,
	credentialID uuid.UUID,
) (checkin.Credential, error) {
	return repository.getCredential(ctx, `
SELECT`+credentialProjection+`
FROM xiangwan_checkin_credentials
WHERE tenant_id = $1 AND id = $2
`, tenantID, credentialID)
}

func (repository *Repository) GetCredentialForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	credentialID uuid.UUID,
) (checkin.Credential, error) {
	return repository.getCredential(ctx, `
SELECT`+credentialProjection+`
FROM xiangwan_checkin_credentials
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, credentialID)
}

func (repository *Repository) GetCredentialByJTI(
	ctx context.Context,
	tenantID uuid.UUID,
	jti uuid.UUID,
) (checkin.Credential, error) {
	return repository.getCredential(ctx, `
SELECT`+credentialProjection+`
FROM xiangwan_checkin_credentials
WHERE tenant_id = $1 AND credential_jti = $2
`, tenantID, jti)
}

func (repository *Repository) GetCredentialByBackupHash(
	ctx context.Context,
	tenantID uuid.UUID,
	digest checkin.CredentialDigest,
) (checkin.Credential, error) {
	return repository.getCredential(ctx, `
SELECT`+credentialProjection+`
FROM xiangwan_checkin_credentials
WHERE tenant_id = $1 AND backup_code_hash = $2
`, tenantID, digest[:])
}

func (repository *Repository) GetActiveCredentialByRegistrationForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.Credential, error) {
	return repository.getCredential(ctx, `
SELECT`+credentialProjection+`
FROM xiangwan_checkin_credentials
WHERE tenant_id = $1
  AND registration_id = $2
  AND credential_status = 'active'
FOR UPDATE
`, tenantID, registrationID)
}

func (repository *Repository) GetLatestCredentialByRegistrationForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.Credential, error) {
	return repository.getCredential(ctx, `
SELECT`+credentialProjection+`
FROM xiangwan_checkin_credentials
WHERE tenant_id = $1 AND registration_id = $2
ORDER BY credential_epoch DESC
LIMIT 1
FOR UPDATE
`, tenantID, registrationID)
}

func (repository *Repository) RevokeCredential(
	ctx context.Context,
	value checkin.Credential,
	expectedVersion int64,
) (checkin.Credential, error) {
	updated, err := scanCredential(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_checkin_credentials
SET credential_status = $3,
    revoked_at = $4,
    revocation_reason = $5,
    version = version + 1,
    updated_at = $6
WHERE tenant_id = $1
  AND id = $2
  AND version = $7
RETURNING`+credentialProjection,
		value.TenantID,
		value.ID,
		value.CredentialStatus,
		value.RevokedAt,
		value.RevocationReason,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return checkin.Credential{}, ErrCredentialVersionConflict
	}
	if err != nil {
		return checkin.Credential{}, fmt.Errorf(`revoke xiangwan Checkin credential: %w`, err)
	}
	return updated, nil
}

func (repository *Repository) getCredential(
	ctx context.Context,
	query string,
	args ...any,
) (checkin.Credential, error) {
	value, err := scanCredential(repository.db.queryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return checkin.Credential{}, ErrCredentialNotFound
	}
	if err != nil {
		return checkin.Credential{}, fmt.Errorf(`get xiangwan Checkin credential: %w`, err)
	}
	return value, nil
}

func scanCredential(row rowScanner) (checkin.Credential, error) {
	var value checkin.Credential
	var qrTokenHash []byte
	var backupCodeHash []byte
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
		&value.CredentialJTI,
		&qrTokenHash,
		&backupCodeHash,
		&value.CredentialEpoch,
		&value.CredentialStatus,
		&value.IssuedAt,
		&value.ExpiresAt,
		&revokedAt,
		&revocationReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return checkin.Credential{}, err
	}
	if len(qrTokenHash) != len(value.QRTokenHash) ||
		len(backupCodeHash) != len(value.BackupCodeHash) {
		return checkin.Credential{}, checkin.ErrInvalidCredential
	}
	copy(value.QRTokenHash[:], qrTokenHash)
	copy(value.BackupCodeHash[:], backupCodeHash)
	value.RevokedAt = nullTimePointer(revokedAt)
	value.RevocationReason = nullStringPointer(revocationReason)
	return value, nil
}
