package peoplepostgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/google/uuid"
)

func (repository *Repository) ListInstanceRoleIdentityHistory(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) ([]people.IdentityRole, error) {
	rows, err := repository.db.queryContext(ctx, `
SELECT
    role_binding.id,
    role_binding.tenant_id,
    role_binding.series_id,
    role_binding.instance_id,
    role_binding.principal_id,
    role_binding.role_code,
    role_binding.role_status,
    role_binding.grant_reason,
    role_binding.granted_by,
    role_binding.granted_at,
    role_binding.revoked_by,
    role_binding.revoked_at,
    role_binding.revocation_reason,
    role_binding.version,
    role_binding.created_at,
    role_binding.updated_at,
    activity_series.title,
    activity_instance.title,
    activity_instance.status
FROM xiangwan_instance_role_bindings AS role_binding
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = role_binding.tenant_id
 AND activity_instance.series_id = role_binding.series_id
 AND activity_instance.id = role_binding.instance_id
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_instance.tenant_id
 AND activity_series.id = activity_instance.series_id
WHERE role_binding.tenant_id = $1
  AND role_binding.principal_id = $2
ORDER BY role_binding.granted_at DESC, role_binding.id DESC
`, tenantID, principalID)
	if err != nil {
		return nil, fmt.Errorf(
			`list xiangwan identity role history: %w`,
			err,
		)
	}
	defer rows.Close()

	result := make([]people.IdentityRole, 0)
	for rows.Next() {
		value, scanErr := scanIdentityRole(rows)
		if scanErr != nil {
			return nil, fmt.Errorf(
				`scan xiangwan identity role history: %w`,
				scanErr,
			)
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			`iterate xiangwan identity role history: %w`,
			err,
		)
	}
	return result, nil
}

func scanIdentityRole(row rowScanner) (people.IdentityRole, error) {
	var value people.IdentityRole
	var revokedBy uuid.NullUUID
	var revokedAt sql.NullTime
	var revocationReason sql.NullString
	err := row.Scan(
		&value.Binding.ID,
		&value.Binding.TenantID,
		&value.Binding.SeriesID,
		&value.Binding.InstanceID,
		&value.Binding.PrincipalID,
		&value.Binding.RoleCode,
		&value.Binding.RoleStatus,
		&value.Binding.GrantReason,
		&value.Binding.GrantedBy,
		&value.Binding.GrantedAt,
		&revokedBy,
		&revokedAt,
		&revocationReason,
		&value.Binding.Version,
		&value.Binding.CreatedAt,
		&value.Binding.UpdatedAt,
		&value.SeriesTitle,
		&value.InstanceTitle,
		&value.InstanceStatus,
	)
	if err != nil {
		return people.IdentityRole{}, err
	}
	value.Binding.RevokedBy = nullUUIDPointer(revokedBy)
	value.Binding.RevokedAt = nullTimePointer(revokedAt)
	value.Binding.RevocationReason = nullStringPointer(revocationReason)
	if err := people.ValidateInstanceRoleBinding(value.Binding); err != nil {
		return people.IdentityRole{}, err
	}
	state, err := people.ClassifyIdentityRole(
		value.Binding,
		activity.InstanceStatus(value.InstanceStatus),
	)
	if err != nil {
		return people.IdentityRole{}, err
	}
	value.State = state
	return value, nil
}
