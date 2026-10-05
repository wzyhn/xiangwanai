package peoplepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

const roleBindingProjection = `
    id, tenant_id, series_id, instance_id, principal_id,
    role_code, role_status, grant_reason, granted_by, granted_at,
    revoked_by, revoked_at, revocation_reason,
    version, created_at, updated_at
`

func (repository *Repository) CreateInstanceRoleBinding(
	ctx context.Context,
	value people.InstanceRoleBinding,
) (people.InstanceRoleBinding, error) {
	if err := people.ValidateInstanceRoleBinding(value); err != nil {
		return people.InstanceRoleBinding{}, err
	}
	if value.RoleStatus != people.RoleStatusActive || value.Version != 1 {
		return people.InstanceRoleBinding{},
			people.ErrInvalidInstanceRoleBinding
	}
	created, err := scanRoleBinding(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_instance_role_bindings (
    id, tenant_id, series_id, instance_id, principal_id,
    role_code, role_status, grant_reason, granted_by, granted_at,
    revoked_by, revoked_at, revocation_reason,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13,
    $14, $15, $16
)
RETURNING`+roleBindingProjection,
		value.ID,
		value.TenantID,
		value.SeriesID,
		value.InstanceID,
		value.PrincipalID,
		value.RoleCode,
		value.RoleStatus,
		value.GrantReason,
		value.GrantedBy,
		value.GrantedAt,
		value.RevokedBy,
		value.RevokedAt,
		value.RevocationReason,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return people.InstanceRoleBinding{}, fmt.Errorf(
			`create xiangwan Instance role binding: %w`,
			err,
		)
	}
	return created, nil
}

func (repository *Repository) GetActiveInstanceRoleBinding(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	principalID uuid.UUID,
	roleCode people.InstanceRoleCode,
) (people.InstanceRoleBinding, error) {
	return repository.getRoleBinding(ctx, `
SELECT`+roleBindingProjection+`
FROM xiangwan_instance_role_bindings
WHERE tenant_id = $1
  AND instance_id = $2
  AND principal_id = $3
  AND role_code = $4
  AND role_status = 'active'
`, tenantID, instanceID, principalID, roleCode)
}

func (repository *Repository) GetActiveInstanceRoleBindingForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	principalID uuid.UUID,
	roleCode people.InstanceRoleCode,
) (people.InstanceRoleBinding, error) {
	return repository.getRoleBinding(ctx, `
SELECT`+roleBindingProjection+`
FROM xiangwan_instance_role_bindings
WHERE tenant_id = $1
  AND instance_id = $2
  AND principal_id = $3
  AND role_code = $4
  AND role_status = 'active'
FOR UPDATE
`, tenantID, instanceID, principalID, roleCode)
}

// GetInstanceRoleBindingByIDForUpdate reads one exact historical role row
// under the caller's transaction. Administrator revocation uses this narrow
// lookup so it can recheck the instance scope and expected version before the
// append-only transition is written.
func (repository *Repository) GetInstanceRoleBindingByIDForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	bindingID uuid.UUID,
) (people.InstanceRoleBinding, error) {
	if tenantID == uuid.Nil || bindingID == uuid.Nil {
		return people.InstanceRoleBinding{}, people.ErrInvalidInstanceRoleBinding
	}
	return repository.getRoleBinding(ctx, `
SELECT`+roleBindingProjection+`
FROM xiangwan_instance_role_bindings
WHERE tenant_id = $1
  AND id = $2
FOR UPDATE
`, tenantID, bindingID)
}

func (repository *Repository) ListActiveInstanceRoleBindings(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) ([]people.InstanceRoleBinding, error) {
	rows, err := repository.db.queryContext(ctx, `
SELECT`+roleBindingProjection+`
FROM xiangwan_instance_role_bindings
WHERE tenant_id = $1
  AND instance_id = $2
  AND role_status = 'active'
ORDER BY granted_at ASC, id ASC
`, tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf(
			`list xiangwan Instance role bindings: %w`,
			err,
		)
	}
	defer rows.Close()
	result := make([]people.InstanceRoleBinding, 0)
	for rows.Next() {
		value, scanErr := scanRoleBinding(rows)
		if scanErr != nil {
			return nil, fmt.Errorf(
				`scan xiangwan Instance role binding: %w`,
				scanErr,
			)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			`iterate xiangwan Instance role bindings: %w`,
			err,
		)
	}
	return result, nil
}

func (repository *Repository) RevokeInstanceRoleBinding(
	ctx context.Context,
	value people.InstanceRoleBinding,
	expectedVersion int64,
) (people.InstanceRoleBinding, error) {
	if err := people.ValidateInstanceRoleBinding(value); err != nil {
		return people.InstanceRoleBinding{}, err
	}
	if value.RoleStatus != people.RoleStatusRevoked ||
		expectedVersion < 1 ||
		value.Version != expectedVersion+1 {
		return people.InstanceRoleBinding{},
			people.ErrInvalidInstanceRoleBinding
	}
	updated, err := scanRoleBinding(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_instance_role_bindings
SET role_status = $3,
    revoked_by = $4,
    revoked_at = $5,
    revocation_reason = $6,
    version = version + 1,
    updated_at = $7
WHERE tenant_id = $1
  AND id = $2
  AND version = $8
RETURNING`+roleBindingProjection,
		value.TenantID,
		value.ID,
		value.RoleStatus,
		value.RevokedBy,
		value.RevokedAt,
		value.RevocationReason,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return people.InstanceRoleBinding{},
			ErrRoleBindingVersionConflict
	}
	if err != nil {
		return people.InstanceRoleBinding{}, fmt.Errorf(
			`revoke xiangwan Instance role binding: %w`,
			err,
		)
	}
	return updated, nil
}

func (repository *Repository) getRoleBinding(
	ctx context.Context,
	query string,
	args ...any,
) (people.InstanceRoleBinding, error) {
	value, err := scanRoleBinding(
		repository.db.queryRowContext(ctx, query, args...),
	)
	if errors.Is(err, sql.ErrNoRows) {
		return people.InstanceRoleBinding{}, ErrRoleBindingNotFound
	}
	if err != nil {
		return people.InstanceRoleBinding{}, fmt.Errorf(
			`get xiangwan Instance role binding: %w`,
			err,
		)
	}
	return value, nil
}

func scanRoleBinding(row rowScanner) (people.InstanceRoleBinding, error) {
	var value people.InstanceRoleBinding
	var revokedBy uuid.NullUUID
	var revokedAt sql.NullTime
	var revocationReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.SeriesID,
		&value.InstanceID,
		&value.PrincipalID,
		&value.RoleCode,
		&value.RoleStatus,
		&value.GrantReason,
		&value.GrantedBy,
		&value.GrantedAt,
		&revokedBy,
		&revokedAt,
		&revocationReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return people.InstanceRoleBinding{}, err
	}
	value.RevokedBy = nullUUIDPointer(revokedBy)
	value.RevokedAt = nullTimePointer(revokedAt)
	value.RevocationReason = nullStringPointer(revocationReason)
	if err := people.ValidateInstanceRoleBinding(value); err != nil {
		return people.InstanceRoleBinding{}, err
	}
	return value, nil
}
