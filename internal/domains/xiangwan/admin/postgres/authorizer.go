package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/google/uuid"
)

type RowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type GrantAuthorizer struct {
	tenantID uuid.UUID
}

const checkinTargetAuthorizationLockSQL = `
WITH authorized_grants AS MATERIALIZED (
    SELECT principal.id AS principal_id,
           identity_link.id AS identity_link_id,
           admin_grant.id AS grant_id
    FROM principals AS principal
    JOIN xiangwan_admin_identity_links AS identity_link
      ON identity_link.tenant_id = $1
     AND identity_link.principal_id = principal.id
     AND identity_link.link_status = 'active'
     AND identity_link.id = $3
    JOIN xiangwan_admin_grants AS admin_grant
      ON admin_grant.tenant_id = $1
     AND admin_grant.principal_id = principal.id
     AND admin_grant.domain_code = 'xiangwan'
     AND admin_grant.grant_status = 'active'
    WHERE principal.id = $2
      AND principal.status = 'active'
      AND principal.deleted_at IS NULL
      AND principal.primary_tenant_id = $1
      AND (
          (
              admin_grant.capability IN ('super_admin', 'activity_operator')
              AND admin_grant.scope_type = 'tenant'
              AND admin_grant.scope_id IS NULL
          )
          OR (
              admin_grant.capability = 'onsite_checkin'
              AND (
                  (
                      admin_grant.scope_type = 'tenant'
                      AND admin_grant.scope_id IS NULL
                  )
                  OR (
                      admin_grant.scope_type = 'session'
                      AND admin_grant.scope_id IS NOT NULL
                  )
              )
          )
      )
    ORDER BY admin_grant.id, identity_link.id
    FOR SHARE OF principal, identity_link, admin_grant
)
SELECT COUNT(*) FROM authorized_grants
`

func NewGrantAuthorizer(tenantID uuid.UUID) (*GrantAuthorizer, error) {
	if tenantID == uuid.Nil {
		return nil, xiangwanadmin.ErrInvalidAdminConfiguration
	}
	return &GrantAuthorizer{tenantID: tenantID}, nil
}

func (authorizer *GrantAuthorizer) RequireActivityOperator(
	ctx context.Context,
	queryer RowQueryer,
	principalID uuid.UUID,
) error {
	return authorizer.require(
		ctx,
		queryer,
		principalID,
		nil,
		"activity_operator",
		nil,
	)
}

func (authorizer *GrantAuthorizer) RequireActivityOperatorIdentity(
	ctx context.Context,
	queryer RowQueryer,
	principalID uuid.UUID,
	identityLinkID uuid.UUID,
) error {
	if identityLinkID == uuid.Nil {
		return xiangwanadmin.ErrScopeForbidden
	}
	return authorizer.require(
		ctx,
		queryer,
		principalID,
		&identityLinkID,
		"activity_operator",
		nil,
	)
}

func (authorizer *GrantAuthorizer) RequireSuperAdminIdentity(
	ctx context.Context,
	queryer RowQueryer,
	principalID uuid.UUID,
	identityLinkID uuid.UUID,
) error {
	if identityLinkID == uuid.Nil {
		return xiangwanadmin.ErrScopeForbidden
	}
	return authorizer.require(ctx, queryer, principalID, &identityLinkID,
		"super_admin", nil)
}

// lockActivityOperatorIdentity orders protected catalog reads before any
// concurrent revocation of the Principal, exact identity link, or active Grant.
func (authorizer *GrantAuthorizer) lockActivityOperatorIdentity(
	ctx context.Context,
	queryer RowQueryer,
	principalID uuid.UUID,
	identityLinkID uuid.UUID,
) error {
	return authorizer.RequireActivityOperatorIdentity(
		ctx, queryer, principalID, identityLinkID,
	)
}

func (authorizer *GrantAuthorizer) lockCheckinTargetReadIdentity(
	ctx context.Context,
	queryer RowQueryer,
	principalID uuid.UUID,
	identityLinkID uuid.UUID,
) error {
	if authorizer == nil || authorizer.tenantID == uuid.Nil || ctx == nil ||
		queryer == nil || principalID == uuid.Nil || identityLinkID == uuid.Nil {
		return xiangwanadmin.ErrScopeForbidden
	}
	var lockedGrantCount int64
	if err := queryer.QueryRowContext(
		ctx,
		checkinTargetAuthorizationLockSQL,
		authorizer.tenantID,
		principalID,
		identityLinkID,
	).Scan(&lockedGrantCount); err != nil {
		return err
	}
	if lockedGrantCount == 0 {
		return xiangwanadmin.ErrScopeForbidden
	}
	return nil
}

// registrationReadAuthorizationLockSQL is RequireRegistrationRead's predicate
// in lock form: the qualifying Principal, identity link, and Grant rows are
// held FOR SHARE for the duration of the read transaction, so a revocation
// must commit after the read releases instead of slipping between the
// authorization check and the data query (codex review 2026-09-19).
const registrationReadAuthorizationLockSQL = `
WITH authorized_grants AS MATERIALIZED (
    SELECT admin_grant.id AS grant_id
    FROM principals AS principal
    JOIN xiangwan_admin_identity_links AS identity_link
      ON identity_link.tenant_id = $1
     AND identity_link.principal_id = principal.id
     AND identity_link.link_status = 'active'
     AND identity_link.id = $3
    JOIN xiangwan_admin_grants AS admin_grant
      ON admin_grant.tenant_id = $1
     AND admin_grant.principal_id = principal.id
     AND admin_grant.domain_code = 'xiangwan'
     AND admin_grant.grant_status = 'active'
    WHERE principal.id = $2
      AND principal.status = 'active'
      AND principal.deleted_at IS NULL
      AND principal.primary_tenant_id = $1
      AND (
          (
              admin_grant.capability IN ('super_admin', 'activity_operator')
              AND admin_grant.scope_type = 'tenant'
              AND admin_grant.scope_id IS NULL
          )
          OR (
              admin_grant.capability = 'onsite_checkin'
              AND (
                  (
                      admin_grant.scope_type = 'tenant'
                      AND admin_grant.scope_id IS NULL
                  )
                  OR (
                      $4::UUID IS NOT NULL
                      AND admin_grant.scope_type = 'session'
                      AND admin_grant.scope_id = $4
                  )
              )
          )
      )
      ORDER BY admin_grant.id
      FOR SHARE OF principal, identity_link, admin_grant
)
SELECT COUNT(*) FROM authorized_grants
`

// lockRegistrationReadIdentity is the transactional counterpart of
// RequireRegistrationRead: identical acceptance, but the qualifying rows are
// locked so the subsequent registration query cannot race a revocation.
func (authorizer *GrantAuthorizer) lockRegistrationReadIdentity(
	ctx context.Context,
	queryer RowQueryer,
	principalID uuid.UUID,
	identityLinkID uuid.UUID,
	sessionID *uuid.UUID,
) error {
	if authorizer == nil || authorizer.tenantID == uuid.Nil || ctx == nil ||
		queryer == nil || principalID == uuid.Nil || identityLinkID == uuid.Nil {
		return xiangwanadmin.ErrScopeForbidden
	}
	var lockedGrantCount int64
	if err := queryer.QueryRowContext(
		ctx,
		registrationReadAuthorizationLockSQL,
		authorizer.tenantID,
		principalID,
		identityLinkID,
		nullableUUID(sessionID),
	).Scan(&lockedGrantCount); err != nil {
		return err
	}
	if lockedGrantCount == 0 {
		return xiangwanadmin.ErrScopeForbidden
	}
	return nil
}

// lockRegistrationDetailReadIdentity locks the Principal, the exact identity
// link, and every active Xiangwan Grant of the operator without a capability
// filter: the detail query carries its own row-level Session-scope predicate
// inside the same snapshot, so locking any qualifying Grant row is enough to
// force a concurrent revocation to commit after the read.
func (authorizer *GrantAuthorizer) lockRegistrationDetailReadIdentity(
	ctx context.Context,
	queryer RowQueryer,
	principalID uuid.UUID,
	identityLinkID uuid.UUID,
) error {
	if authorizer == nil || authorizer.tenantID == uuid.Nil || ctx == nil ||
		queryer == nil || principalID == uuid.Nil || identityLinkID == uuid.Nil {
		return xiangwanadmin.ErrScopeForbidden
	}
	var lockedGrantCount int64
	if err := queryer.QueryRowContext(
		ctx, `
WITH authorized_grants AS MATERIALIZED (
    SELECT admin_grant.id AS grant_id
    FROM principals AS principal
    JOIN xiangwan_admin_identity_links AS identity_link
      ON identity_link.tenant_id = $1
     AND identity_link.principal_id = principal.id
     AND identity_link.link_status = 'active'
     AND identity_link.id = $3
    JOIN xiangwan_admin_grants AS admin_grant
      ON admin_grant.tenant_id = $1
     AND admin_grant.principal_id = principal.id
     AND admin_grant.domain_code = 'xiangwan'
     AND admin_grant.grant_status = 'active'
    WHERE principal.id = $2
      AND principal.status = 'active'
      AND principal.deleted_at IS NULL
      AND principal.primary_tenant_id = $1
      ORDER BY admin_grant.id
      FOR SHARE OF principal, identity_link, admin_grant
)
SELECT COUNT(*) FROM authorized_grants
`,
		authorizer.tenantID,
		principalID,
		identityLinkID,
	).Scan(&lockedGrantCount); err != nil {
		return err
	}
	if lockedGrantCount == 0 {
		return xiangwanadmin.ErrScopeForbidden
	}
	return nil
}

// RequireRegistrationRead accepts tenant activity operators and on-site
// operators. A Session-scoped on-site grant can read only that exact Session.
func (authorizer *GrantAuthorizer) RequireRegistrationRead(
	ctx context.Context,
	queryer RowQueryer,
	principalID uuid.UUID,
	identityLinkID uuid.UUID,
	sessionID *uuid.UUID,
) error {
	if authorizer == nil || authorizer.tenantID == uuid.Nil || ctx == nil ||
		queryer == nil || principalID == uuid.Nil || identityLinkID == uuid.Nil {
		return xiangwanadmin.ErrScopeForbidden
	}
	var allowed bool
	err := queryer.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM principals AS principal
    JOIN xiangwan_admin_identity_links AS identity_link
     ON identity_link.tenant_id = $1
     AND identity_link.principal_id = principal.id
     AND identity_link.link_status = 'active'
     AND identity_link.id = $3
    JOIN xiangwan_admin_grants AS admin_grant
      ON admin_grant.tenant_id = $1
     AND admin_grant.principal_id = principal.id
     AND admin_grant.domain_code = 'xiangwan'
     AND admin_grant.grant_status = 'active'
    WHERE principal.id = $2
      AND principal.status = 'active'
      AND principal.deleted_at IS NULL
      AND principal.primary_tenant_id = $1
      AND (
          (
              admin_grant.capability IN ('super_admin', 'activity_operator')
              AND admin_grant.scope_type = 'tenant'
              AND admin_grant.scope_id IS NULL
          )
          OR (
              admin_grant.capability = 'onsite_checkin'
              AND (
                  (
                      admin_grant.scope_type = 'tenant'
                      AND admin_grant.scope_id IS NULL
                  )
                  OR (
                      $4::UUID IS NOT NULL
                      AND admin_grant.scope_type = 'session'
                      AND admin_grant.scope_id = $4
                  )
              )
          )
      )
)
`, authorizer.tenantID, principalID, identityLinkID, nullableUUID(sessionID)).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed {
		return xiangwanadmin.ErrScopeForbidden
	}
	return nil
}

func (authorizer *GrantAuthorizer) AuthorizeRecordCheckin(
	ctx context.Context,
	queryer checkinpostgres.OperatorAuthorizationQuery,
	request checkinpostgres.RecordCheckinAuthorization,
) error {
	if authorizer == nil || request.TenantID != authorizer.tenantID {
		return checkinpostgres.ErrCheckinOperatorForbidden
	}
	if err := authorizer.require(
		ctx,
		queryer,
		request.ActorID,
		&request.IdentityLinkID,
		"onsite_checkin",
		&request.SessionID,
	); err != nil {
		if errors.Is(err, xiangwanadmin.ErrScopeForbidden) {
			return checkinpostgres.ErrCheckinOperatorForbidden
		}
		return err
	}
	return nil
}

func (authorizer *GrantAuthorizer) require(
	ctx context.Context,
	queryer interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	},
	principalID uuid.UUID,
	identityLinkID *uuid.UUID,
	capability string,
	sessionID *uuid.UUID,
) error {
	if authorizer == nil || authorizer.tenantID == uuid.Nil || ctx == nil ||
		queryer == nil || principalID == uuid.Nil {
		return xiangwanadmin.ErrScopeForbidden
	}
	var selectedPrincipalID uuid.UUID
	var selectedIdentityLinkID uuid.UUID
	var selectedGrantID uuid.UUID
	err := queryer.QueryRowContext(ctx, `
SELECT principal.id, identity_link.id, admin_grant.id
FROM principals AS principal
JOIN xiangwan_admin_identity_links AS identity_link
  ON identity_link.tenant_id = $1
 AND identity_link.principal_id = principal.id
 AND identity_link.link_status = 'active'
 AND ($3::UUID IS NULL OR identity_link.id = $3)
JOIN xiangwan_admin_grants AS admin_grant
  ON admin_grant.tenant_id = $1
 AND admin_grant.principal_id = principal.id
 AND admin_grant.domain_code = 'xiangwan'
 AND admin_grant.grant_status = 'active'
WHERE principal.id = $2
  AND principal.status = 'active'
  AND principal.deleted_at IS NULL
  AND principal.primary_tenant_id = $1
  AND (
      (
          admin_grant.capability = 'super_admin'
          AND admin_grant.scope_type = 'tenant'
          AND admin_grant.scope_id IS NULL
      )
      OR (
          $4 = 'onsite_checkin'
          AND admin_grant.capability = 'activity_operator'
          AND admin_grant.scope_type = 'tenant'
          AND admin_grant.scope_id IS NULL
      )
      OR (
          admin_grant.capability = $4
          AND (
              (
                  admin_grant.scope_type = 'tenant'
                  AND admin_grant.scope_id IS NULL
              )
              OR (
                  $5::UUID IS NOT NULL
                  AND admin_grant.scope_type = 'session'
                  AND admin_grant.scope_id = $5
              )
          )
      )
  )
ORDER BY admin_grant.id, identity_link.id
LIMIT 1
FOR SHARE OF principal, identity_link, admin_grant
`, authorizer.tenantID, principalID, nullableUUID(identityLinkID), capability,
		nullableUUID(sessionID)).Scan(
		&selectedPrincipalID,
		&selectedIdentityLinkID,
		&selectedGrantID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.ErrScopeForbidden
	}
	if err != nil {
		return err
	}
	return nil
}
