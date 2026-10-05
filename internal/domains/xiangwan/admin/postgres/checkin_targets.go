package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

const checkinTargetScopeSQL = `
FROM xiangwan_activity_sessions AS session
JOIN xiangwan_activity_instances AS instance
  ON instance.tenant_id = session.tenant_id
 AND instance.id = session.instance_id
JOIN xiangwan_activity_series AS series
  ON series.tenant_id = instance.tenant_id
 AND series.id = instance.series_id
WHERE session.tenant_id = $1
  AND session.status = 'published'
  AND instance.status = 'published'
  AND EXISTS (
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
                        admin_grant.scope_type = 'session'
                        AND admin_grant.scope_id = session.id
                    )
                )
            )
        )
  )`

// ListCheckinTargets returns only operational Session labels and counters.
// The authorization predicate is part of the same SQL statement so a
// Session-scoped operator never receives a tenant-wide target inventory.
func (catalog *Catalog) ListCheckinTargets(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	page int,
	pageSize int,
) (xiangwanadmin.CheckinTargetPage, error) {
	page, pageSize, pageErr := normalizePage(page, pageSize)
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || pageErr != nil {
		return xiangwanadmin.CheckinTargetPage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return xiangwanadmin.CheckinTargetPage{}, fmt.Errorf(
			"begin administrator Checkin target read: %w", err,
		)
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.lockCheckinTargetReadIdentity(
		ctx,
		tx,
		principal.PrincipalID,
		principal.IdentityLinkID,
	); err != nil {
		return xiangwanadmin.CheckinTargetPage{}, fmt.Errorf(
			"lock administrator Checkin target authorization: %w", err,
		)
	}
	var total int64
	if err := tx.QueryRowContext(
		ctx,
		"SELECT COUNT(*) "+checkinTargetScopeSQL,
		catalog.tenantID,
		principal.PrincipalID,
		principal.IdentityLinkID,
	).Scan(&total); err != nil {
		return xiangwanadmin.CheckinTargetPage{}, fmt.Errorf(
			"count administrator Checkin targets: %w", err,
		)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT
    series.id, instance.id, session.id,
    series.title, instance.title, session.title,
    session.session_start_at, COALESCE(session.venue_name, ''),
    session.confirmed_registration_count,
    session.checked_in_registration_count,
    session.capacity`+checkinTargetScopeSQL+`
ORDER BY session.session_start_at DESC, session.id DESC
OFFSET $4 LIMIT $5
`, catalog.tenantID, principal.PrincipalID, principal.IdentityLinkID,
		(page-1)*pageSize, pageSize)
	if err != nil {
		return xiangwanadmin.CheckinTargetPage{}, fmt.Errorf(
			"list administrator Checkin targets: %w", err,
		)
	}
	defer func() { _ = rows.Close() }()
	targets := make([]xiangwanadmin.CheckinTarget, 0)
	for rows.Next() {
		var target xiangwanadmin.CheckinTarget
		if err := rows.Scan(
			&target.SeriesID, &target.InstanceID, &target.SessionID,
			&target.SeriesTitle, &target.InstanceTitle, &target.SessionTitle,
			&target.SessionStartAt, &target.VenueName,
			&target.ConfirmedRegistrationCount,
			&target.CheckedInRegistrationCount, &target.Capacity,
		); err != nil {
			return xiangwanadmin.CheckinTargetPage{}, fmt.Errorf(
				"scan administrator Checkin target: %w", err,
			)
		}
		targets = append(targets, target)
	}
	if err := rows.Err(); err != nil {
		return xiangwanadmin.CheckinTargetPage{}, fmt.Errorf(
			"iterate administrator Checkin targets: %w", err,
		)
	}
	if err := rows.Close(); err != nil {
		return xiangwanadmin.CheckinTargetPage{}, fmt.Errorf(
			"close administrator Checkin targets: %w", err,
		)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.CheckinTargetPage{}, fmt.Errorf(
			"commit administrator Checkin target read: %w", err,
		)
	}
	return xiangwanadmin.CheckinTargetPage{
		Items: targets, Page: page, PageSize: pageSize, Total: total,
	}, nil
}
