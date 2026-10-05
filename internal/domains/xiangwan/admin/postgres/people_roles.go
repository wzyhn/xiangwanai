package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// ListPeople returns tenant-scoped PeopleProfile facts for the administrator
// UI. It deliberately omits Principal and contact fields; the profile ID is
// the only selector the role writer accepts.
func (catalog *Catalog) ListPeople(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	page int,
	pageSize int,
) (result xiangwanadmin.PeoplePage, resultErr error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil {
		return xiangwanadmin.PeoplePage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	page, pageSize, err := normalizePage(page, pageSize)
	if err != nil {
		return xiangwanadmin.PeoplePage{}, err
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.PeoplePage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var total int64
	if err := tx.QueryRowContext(ctx, `
SELECT count(*)
FROM xiangwan_people_profiles
WHERE tenant_id = $1
`, catalog.tenantID).Scan(&total); err != nil {
		return xiangwanadmin.PeoplePage{}, fmt.Errorf("count xiangwan PeopleProfiles: %w", err)
	}
	offset := (page - 1) * pageSize
	rows, err := tx.QueryContext(ctx, `
SELECT profile.id, profile.display_name, profile.headline,
       profile.introduction, profile.profile_status,
       profile.moderation_status, profile.version, profile.updated_at,
       binding.id
FROM xiangwan_people_profiles AS profile
LEFT JOIN xiangwan_people_bindings AS binding
  ON binding.tenant_id = profile.tenant_id
 AND binding.people_profile_id = profile.id
 AND binding.binding_status = 'active'
WHERE profile.tenant_id = $1
ORDER BY profile.updated_at DESC, profile.id DESC
LIMIT $2 OFFSET $3
`, catalog.tenantID, pageSize, offset)
	if err != nil {
		return xiangwanadmin.PeoplePage{}, fmt.Errorf("list xiangwan PeopleProfiles: %w", err)
	}
	defer rows.Close()
	items := make([]xiangwanadmin.Person, 0, pageSize)
	for rows.Next() {
		var value xiangwanadmin.Person
		var headline sql.NullString
		var bindingID uuid.NullUUID
		if err := rows.Scan(
			&value.ID, &value.DisplayName, &headline, &value.Introduction,
			&value.ProfileStatus, &value.ModerationStatus, &value.Version,
			&value.UpdatedAt, &bindingID,
		); err != nil {
			return xiangwanadmin.PeoplePage{}, fmt.Errorf("scan xiangwan PeopleProfile: %w", err)
		}
		if headline.Valid {
			value.Headline = &headline.String
		}
		if bindingID.Valid {
			value.ActiveBindingID = &bindingID.UUID
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return xiangwanadmin.PeoplePage{}, fmt.Errorf("iterate xiangwan PeopleProfiles: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.PeoplePage{}, fmt.Errorf("commit xiangwan PeopleProfile read: %w", err)
	}
	return xiangwanadmin.PeoplePage{Items: items, Page: page, PageSize: pageSize, Total: total}, nil
}

// ListInstanceRoles returns active role bindings whose trusted PeopleProfile
// is still linked. A revoked/removed People binding is not projected as a
// current leader and therefore cannot be accidentally reused by the UI.
func (catalog *Catalog) ListInstanceRoles(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	instanceID uuid.UUID,
) (result []xiangwanadmin.InstanceRole, resultErr error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || instanceID == uuid.Nil {
		return nil, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM xiangwan_activity_instances
    WHERE tenant_id = $1 AND id = $2
)
`, catalog.tenantID, instanceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check xiangwan Instance for roles: %w", err)
	}
	if !exists {
		return nil, xiangwanadmin.ErrTargetNotFound
	}
	rows, err := tx.QueryContext(ctx, `
SELECT role_binding.id, role_binding.instance_id,
       profile.id, profile.display_name, profile.headline,
       role_binding.role_code, role_binding.role_status,
       role_binding.grant_reason, role_binding.version,
       role_binding.granted_at, role_binding.revoked_at
FROM xiangwan_instance_role_bindings AS role_binding
JOIN xiangwan_people_bindings AS people_binding
  ON people_binding.tenant_id = role_binding.tenant_id
 AND people_binding.principal_id = role_binding.principal_id
 AND people_binding.binding_status = 'active'
JOIN xiangwan_people_profiles AS profile
  ON profile.tenant_id = people_binding.tenant_id
 AND profile.id = people_binding.people_profile_id
 AND profile.profile_status = 'published'
 AND profile.moderation_status = 'approved'
WHERE role_binding.tenant_id = $1
  AND role_binding.instance_id = $2
  AND role_binding.role_status = 'active'
ORDER BY role_binding.granted_at ASC, role_binding.id ASC
`, catalog.tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan Instance roles: %w", err)
	}
	defer rows.Close()
	items := make([]xiangwanadmin.InstanceRole, 0)
	for rows.Next() {
		var value xiangwanadmin.InstanceRole
		var headline sql.NullString
		var revokedAt sql.NullTime
		if err := rows.Scan(
			&value.ID, &value.InstanceID, &value.PeopleProfileID,
			&value.DisplayName, &headline, &value.RoleCode, &value.RoleStatus,
			&value.GrantReason, &value.Version, &value.GrantedAt, &revokedAt,
		); err != nil {
			return nil, fmt.Errorf("scan xiangwan Instance role: %w", err)
		}
		if headline.Valid {
			value.Headline = &headline.String
		}
		if revokedAt.Valid {
			value.RevokedAt = &revokedAt.Time
		}
		items = append(items, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan Instance roles: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit xiangwan Instance role read: %w", err)
	}
	return items, nil
}

func (catalog *Catalog) AssignInstanceRole(
	ctx context.Context,
	command xiangwanadmin.AssignInstanceRoleCommand,
) (result xiangwanadmin.InstanceRole, resultErr error) {
	command.RoleCode = strings.TrimSpace(command.RoleCode)
	command.GrantReason = strings.TrimSpace(command.GrantReason)
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"instance_role.assign", "instance", command.InstanceID,
			command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.PeopleProfileID == uuid.Nil ||
		!validAdminPeopleRoleCode(command.RoleCode) || command.GrantReason == "" ||
		len([]rune(command.GrantReason)) > 500 {
		return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		InstanceID      uuid.UUID `json:"instance_id"`
		PeopleProfileID uuid.UUID `json:"people_profile_id"`
		RoleCode        string    `json:"role_code"`
		GrantReason     string    `json:"grant_reason"`
	}{command.InstanceID, command.PeopleProfileID, command.RoleCode, command.GrantReason})
	if err != nil {
		return xiangwanadmin.InstanceRole{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return xiangwanadmin.InstanceRole{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.InstanceRole]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"instance_role.assign", digest,
	); err != nil {
		return xiangwanadmin.InstanceRole{}, err
	} else if replay {
		if err := tx.Commit(); err != nil {
			return xiangwanadmin.InstanceRole{}, fmt.Errorf("commit Instance role replay: %w", err)
		}
		return receipt.Value, nil
	}
	var seriesID, principalID uuid.UUID
	if err := tx.QueryRowContext(ctx, `
SELECT instance.series_id, people_binding.principal_id
FROM xiangwan_activity_instances AS instance
JOIN xiangwan_people_bindings AS people_binding
  ON people_binding.tenant_id = instance.tenant_id
 AND people_binding.people_profile_id = $3
 AND people_binding.binding_status = 'active'
JOIN xiangwan_people_profiles AS profile
  ON profile.tenant_id = people_binding.tenant_id
 AND profile.id = people_binding.people_profile_id
 AND profile.profile_status = 'published'
 AND profile.moderation_status = 'approved'
WHERE instance.tenant_id = $1 AND instance.id = $2
`, catalog.tenantID, command.InstanceID, command.PeopleProfileID).Scan(&seriesID, &principalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			var instanceExists bool
			if existsErr := tx.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM xiangwan_activity_instances WHERE tenant_id = $1 AND id = $2)
`, catalog.tenantID, command.InstanceID).Scan(&instanceExists); existsErr != nil {
				return xiangwanadmin.InstanceRole{}, fmt.Errorf("check Instance role target: %w", existsErr)
			}
			if !instanceExists {
				return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrTargetNotFound
			}
			return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrPeopleProfileUnavailable
		}
		return xiangwanadmin.InstanceRole{}, fmt.Errorf("resolve trusted People binding: %w", err)
	}
	now := catalog.now().UTC()
	binding, err := people.NewInstanceRoleBinding(people.NewInstanceRoleBindingCommand{
		TenantID: catalog.tenantID, SeriesID: seriesID, InstanceID: command.InstanceID,
		PrincipalID: principalID, RoleCode: people.InstanceRoleCode(command.RoleCode),
		GrantReason: command.GrantReason, ActorID: command.ActorID, At: now,
	})
	if err != nil {
		return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	stored, err := peoplepostgres.NewRepository(tx).CreateInstanceRoleBinding(ctx, binding)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "23505" {
			return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrInstanceRoleConflict
		}
		return xiangwanadmin.InstanceRole{}, err
	}
	result = xiangwanadmin.InstanceRole{
		ID: stored.ID, InstanceID: stored.InstanceID, PeopleProfileID: command.PeopleProfileID,
		RoleCode: string(stored.RoleCode), RoleStatus: string(stored.RoleStatus),
		GrantReason: stored.GrantReason, Version: stored.Version,
		GrantedAt: stored.GrantedAt,
	}
	var headline sql.NullString
	if err := tx.QueryRowContext(ctx, `
SELECT display_name, headline
FROM xiangwan_people_profiles
WHERE tenant_id = $1 AND id = $2
	`, catalog.tenantID, command.PeopleProfileID).Scan(&result.DisplayName, &headline); err != nil {
		return xiangwanadmin.InstanceRole{}, fmt.Errorf("read assigned PeopleProfile: %w", err)
	}
	if headline.Valid {
		result.Headline = &headline.String
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "instance_role.assign", digest,
		operationResult[xiangwanadmin.InstanceRole]{Value: result}, result.ID, result.Version,
		command.RequestID, now,
	); err != nil {
		return xiangwanadmin.InstanceRole{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.InstanceRole{}, fmt.Errorf("commit Instance role assignment: %w", err)
	}
	return result, nil
}

func (catalog *Catalog) RevokeInstanceRole(
	ctx context.Context,
	command xiangwanadmin.RevokeInstanceRoleCommand,
) (result xiangwanadmin.InstanceRole, resultErr error) {
	command.Reason = strings.TrimSpace(command.Reason)
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"instance_role.revoke", "instance_role_binding", command.RoleBindingID,
			command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.RoleBindingID == uuid.Nil ||
		command.ExpectedVersion < 1 || command.Reason == "" || len([]rune(command.Reason)) > 500 {
		return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		InstanceID      uuid.UUID `json:"instance_id"`
		RoleBindingID   uuid.UUID `json:"role_binding_id"`
		ExpectedVersion int64     `json:"expected_version"`
		Reason          string    `json:"reason"`
	}{command.InstanceID, command.RoleBindingID, command.ExpectedVersion, command.Reason})
	if err != nil {
		return xiangwanadmin.InstanceRole{}, err
	}
	tx, err := catalog.beginActivityWrite(ctx, command.ActorID, command.IdentityLinkID, command.OperationID)
	if err != nil {
		return xiangwanadmin.InstanceRole{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.InstanceRole]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"instance_role.revoke", digest,
	); err != nil {
		return xiangwanadmin.InstanceRole{}, err
	} else if replay {
		if err := tx.Commit(); err != nil {
			return xiangwanadmin.InstanceRole{}, fmt.Errorf("commit Instance role revoke replay: %w", err)
		}
		return receipt.Value, nil
	}
	repository := peoplepostgres.NewRepository(tx)
	current, err := repository.GetInstanceRoleBindingByIDForUpdate(ctx, catalog.tenantID, command.RoleBindingID)
	if errors.Is(err, peoplepostgres.ErrRoleBindingNotFound) {
		return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.InstanceRole{}, err
	}
	if current.InstanceID != command.InstanceID {
		return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrTargetNotFound
	}
	now := catalog.now().UTC()
	next, err := people.RevokeInstanceRoleBinding(current, people.RevokeInstanceRoleBindingCommand{
		ActorID: command.ActorID, Reason: command.Reason, At: now,
	})
	if err != nil {
		return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if next.Version != command.ExpectedVersion+1 {
		return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrVersionConflict
	}
	stored, err := repository.RevokeInstanceRoleBinding(ctx, next, command.ExpectedVersion)
	if errors.Is(err, peoplepostgres.ErrRoleBindingVersionConflict) {
		return xiangwanadmin.InstanceRole{}, xiangwanadmin.ErrVersionConflict
	}
	if err != nil {
		return xiangwanadmin.InstanceRole{}, err
	}
	result = xiangwanadmin.InstanceRole{
		ID: stored.ID, InstanceID: stored.InstanceID, RoleCode: string(stored.RoleCode),
		RoleStatus: string(stored.RoleStatus), GrantReason: stored.GrantReason,
		Version: stored.Version, GrantedAt: stored.GrantedAt, RevokedAt: stored.RevokedAt,
	}
	var headline sql.NullString
	if err := tx.QueryRowContext(ctx, `
SELECT profile.id, profile.display_name, profile.headline
FROM xiangwan_people_bindings AS binding
JOIN xiangwan_people_profiles AS profile
  ON profile.tenant_id = binding.tenant_id AND profile.id = binding.people_profile_id
WHERE binding.tenant_id = $1 AND binding.principal_id = $2
ORDER BY binding.bound_at DESC, binding.id DESC
LIMIT 1
`, catalog.tenantID, stored.PrincipalID).Scan(&result.PeopleProfileID, &result.DisplayName, &headline); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.InstanceRole{}, fmt.Errorf("read revoked PeopleProfile: %w", err)
	}
	if headline.Valid {
		result.Headline = &headline.String
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "instance_role.revoke", digest,
		operationResult[xiangwanadmin.InstanceRole]{Value: result}, result.ID, result.Version,
		command.RequestID, now,
	); err != nil {
		return xiangwanadmin.InstanceRole{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.InstanceRole{}, fmt.Errorf("commit Instance role revoke: %w", err)
	}
	return result, nil
}

func validAdminPeopleRoleCode(value string) bool {
	switch people.InstanceRoleCode(value) {
	case people.InstanceRoleHost,
		people.InstanceRoleInvitedGuest,
		people.InstanceRoleCourseInstructor,
		people.InstanceRoleEventSpeaker:
		return true
	default:
		return false
	}
}
