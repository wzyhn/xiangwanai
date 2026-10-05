package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/google/uuid"
)

// ListAuditEvents exposes only a tenant's event identity and routing metadata
// to a live super administrator. The details JSONB is intentionally neither
// selected nor logged. This read itself commits a content-free audit event.
func (catalog *Catalog) ListAuditEvents(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	filter xiangwanadmin.AuditEventFilter,
) (xiangwanadmin.AuditEventPage, error) {
	page, pageSize, pageErr := normalizePage(filter.Page, filter.PageSize)
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || pageErr != nil ||
		(filter.AsOf != nil && filter.AsOf.IsZero()) ||
		(page > 1 && filter.AsOf == nil) ||
		page-1 > maxPostgresInteger/pageSize {
		return xiangwanadmin.AuditEventPage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	now := catalog.now().UTC().Truncate(time.Microsecond)
	asOf := now
	if filter.AsOf != nil {
		asOf = filter.AsOf.UTC()
		if asOf.After(now) {
			return xiangwanadmin.AuditEventPage{}, xiangwanadmin.ErrInvalidCatalogRequest
		}
	}
	tx, err := catalog.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return xiangwanadmin.AuditEventPage{}, fmt.Errorf("begin administrator audit read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.require(
		ctx, tx, principal.PrincipalID, &principal.IdentityLinkID, "super_admin", nil,
	); err != nil {
		return xiangwanadmin.AuditEventPage{}, err
	}
	result := xiangwanadmin.AuditEventPage{
		Items: make([]xiangwanadmin.AuditEvent, 0),
		Page:  page, PageSize: pageSize, AsOf: asOf,
	}
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM xiangwan_admin_audit_events
WHERE tenant_id = $1 AND occurred_at < $2`, catalog.tenantID, asOf).Scan(&result.Total); err != nil {
		return xiangwanadmin.AuditEventPage{}, fmt.Errorf("count administrator audit events: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT id, actor_id, action_code, target_type, target_id, occurred_at
FROM xiangwan_admin_audit_events
WHERE tenant_id = $1 AND occurred_at < $2
ORDER BY occurred_at DESC, id DESC
OFFSET $3 LIMIT $4`, catalog.tenantID, asOf, (page-1)*pageSize, pageSize)
	if err != nil {
		return xiangwanadmin.AuditEventPage{}, fmt.Errorf("list administrator audit events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item xiangwanadmin.AuditEvent
		if err := rows.Scan(
			&item.ID, &item.ActorID, &item.ActionCode,
			&item.TargetType, &item.TargetID, &item.OccurredAt,
		); err != nil {
			return xiangwanadmin.AuditEventPage{}, fmt.Errorf("scan administrator audit event: %w", err)
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return xiangwanadmin.AuditEventPage{}, fmt.Errorf("iterate administrator audit events: %w", err)
	}
	if err := rows.Close(); err != nil {
		return xiangwanadmin.AuditEventPage{}, fmt.Errorf("close administrator audit events: %w", err)
	}
	requestID := requestctx.RequestID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'admin.audit_list_read', 'tenant', $2,
    $4, jsonb_build_object('identity_link_id', $5::TEXT, 'page', $6::INTEGER,
    'page_size', $7::INTEGER, 'returned', $8::INTEGER, 'as_of', $9::TEXT), $10, $10)
`, uuid.New(), catalog.tenantID, principal.PrincipalID, requestID,
		principal.IdentityLinkID.String(), page, pageSize, len(result.Items),
		asOf.Format(time.RFC3339Nano), now); err != nil {
		return xiangwanadmin.AuditEventPage{}, fmt.Errorf("audit administrator audit read: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.AuditEventPage{}, fmt.Errorf("commit administrator audit read: %w", err)
	}
	return result, nil
}
