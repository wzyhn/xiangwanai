package peoplepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

const bindingProjection = `
    id, tenant_id, people_profile_id, principal_id, evidence_digest,
    binding_status, bound_by, bound_at,
    revoked_by, revoked_at, revocation_reason,
    version, created_at, updated_at
`

func (repository *Repository) CreateBinding(
	ctx context.Context,
	value people.Binding,
) (people.Binding, error) {
	if err := people.ValidateBinding(value); err != nil {
		return people.Binding{}, err
	}
	if value.BindingStatus != people.BindingStatusActive || value.Version != 1 {
		return people.Binding{}, people.ErrInvalidBinding
	}
	created, err := scanBinding(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_people_bindings (
    id, tenant_id, people_profile_id, principal_id, evidence_digest,
    binding_status, bound_by, bound_at,
    revoked_by, revoked_at, revocation_reason,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11,
    $12, $13, $14
)
RETURNING`+bindingProjection,
		value.ID,
		value.TenantID,
		value.PeopleProfileID,
		value.PrincipalID,
		value.EvidenceDigest[:],
		value.BindingStatus,
		value.BoundBy,
		value.BoundAt,
		value.RevokedBy,
		value.RevokedAt,
		value.RevocationReason,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return people.Binding{}, fmt.Errorf(
			`create xiangwan People binding: %w`,
			err,
		)
	}
	return created, nil
}

func (repository *Repository) GetActiveBindingByProfile(
	ctx context.Context,
	tenantID uuid.UUID,
	profileID uuid.UUID,
) (people.Binding, error) {
	return repository.getBinding(ctx, `
SELECT`+bindingProjection+`
FROM xiangwan_people_bindings
WHERE tenant_id = $1
  AND people_profile_id = $2
  AND binding_status = 'active'
`, tenantID, profileID)
}

func (repository *Repository) GetActiveBindingByPrincipal(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) (people.Binding, error) {
	return repository.getBinding(ctx, `
SELECT`+bindingProjection+`
FROM xiangwan_people_bindings
WHERE tenant_id = $1
  AND principal_id = $2
  AND binding_status = 'active'
`, tenantID, principalID)
}

func (repository *Repository) GetActiveBindingByProfileForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	profileID uuid.UUID,
) (people.Binding, error) {
	return repository.getBinding(ctx, `
SELECT`+bindingProjection+`
FROM xiangwan_people_bindings
WHERE tenant_id = $1
  AND people_profile_id = $2
  AND binding_status = 'active'
FOR UPDATE
`, tenantID, profileID)
}

func (repository *Repository) RevokeBinding(
	ctx context.Context,
	value people.Binding,
	expectedVersion int64,
) (people.Binding, error) {
	if err := people.ValidateBinding(value); err != nil {
		return people.Binding{}, err
	}
	if value.BindingStatus != people.BindingStatusRevoked ||
		expectedVersion < 1 ||
		value.Version != expectedVersion+1 {
		return people.Binding{}, people.ErrInvalidBinding
	}
	updated, err := scanBinding(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_people_bindings
SET binding_status = $3,
    revoked_by = $4,
    revoked_at = $5,
    revocation_reason = $6,
    version = version + 1,
    updated_at = $7
WHERE tenant_id = $1
  AND id = $2
  AND version = $8
RETURNING`+bindingProjection,
		value.TenantID,
		value.ID,
		value.BindingStatus,
		value.RevokedBy,
		value.RevokedAt,
		value.RevocationReason,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return people.Binding{}, ErrBindingVersionConflict
	}
	if err != nil {
		return people.Binding{}, fmt.Errorf(
			`revoke xiangwan People binding: %w`,
			err,
		)
	}
	return updated, nil
}

func (repository *Repository) getBinding(
	ctx context.Context,
	query string,
	args ...any,
) (people.Binding, error) {
	value, err := scanBinding(
		repository.db.queryRowContext(ctx, query, args...),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return people.Binding{}, ErrBindingNotFound
	}
	if err != nil {
		return people.Binding{}, fmt.Errorf(
			`get xiangwan People binding: %w`,
			err,
		)
	}
	return value, nil
}

func scanBinding(row rowScanner) (people.Binding, error) {
	var value people.Binding
	var evidenceDigest []byte
	var revokedBy uuid.NullUUID
	var revokedAt sql.NullTime
	var revocationReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.PeopleProfileID,
		&value.PrincipalID,
		&evidenceDigest,
		&value.BindingStatus,
		&value.BoundBy,
		&value.BoundAt,
		&revokedBy,
		&revokedAt,
		&revocationReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return people.Binding{}, err
	}
	if len(evidenceDigest) != len(value.EvidenceDigest) {
		return people.Binding{}, people.ErrInvalidBinding
	}
	copy(value.EvidenceDigest[:], evidenceDigest)
	value.RevokedBy = nullUUIDPointer(revokedBy)
	value.RevokedAt = nullTimePointer(revokedAt)
	value.RevocationReason = nullStringPointer(revocationReason)
	if err := people.ValidateBinding(value); err != nil {
		return people.Binding{}, err
	}
	return value, nil
}
