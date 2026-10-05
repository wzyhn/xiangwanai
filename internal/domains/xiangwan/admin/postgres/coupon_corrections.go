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

// ListCouponCorrections exposes immutable markers, exact routing IDs and manual
// handling metadata. It omits private evidence and never changes financial facts.
func (catalog *Catalog) ListCouponCorrections(
	ctx context.Context, principal xiangwanadmin.Principal,
	filter xiangwanadmin.CouponCorrectionFilter,
) (xiangwanadmin.CouponCorrectionPage, error) {
	page, pageSize, pageErr := normalizePage(filter.Page, filter.PageSize)
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || pageErr != nil ||
		(filter.AsOf != nil && filter.AsOf.IsZero()) ||
		(page > 1 && filter.AsOf == nil) ||
		page-1 > maxPostgresInteger/pageSize {
		return xiangwanadmin.CouponCorrectionPage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	now := catalog.now().UTC().Truncate(time.Microsecond)
	asOf := now
	if filter.AsOf != nil {
		asOf = filter.AsOf.UTC()
		if asOf.After(now) {
			return xiangwanadmin.CouponCorrectionPage{}, xiangwanadmin.ErrInvalidCatalogRequest
		}
	}
	tx, err := catalog.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return xiangwanadmin.CouponCorrectionPage{}, fmt.Errorf("begin administrator Coupon correction read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.require(
		ctx, tx, principal.PrincipalID, &principal.IdentityLinkID, "super_admin", nil,
	); err != nil {
		return xiangwanadmin.CouponCorrectionPage{}, err
	}
	result := xiangwanadmin.CouponCorrectionPage{
		Items: make([]xiangwanadmin.CouponCorrectionItem, 0),
		Page:  page, PageSize: pageSize, AsOf: asOf,
	}
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM xiangwan_coupon_entries
WHERE tenant_id = $1 AND entry_type = 'correction_required' AND recorded_at < $2
`, catalog.tenantID, asOf).Scan(&result.Total); err != nil {
		return xiangwanadmin.CouponCorrectionPage{}, fmt.Errorf("count Coupon corrections: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT correction.id, correction.coupon_id, instrument.face_value_cents,
       correction.source_checkin_event_id,
       COALESCE(transaction_entry.entry_type, related.entry_type),
       transaction_entry.order_id, transaction_entry.registration_id,
       correction.recorded_at,
       COALESCE(CASE handling.event_type WHEN 'started' THEN 'processing'
                    ELSE handling.event_type END, 'pending'),
       COALESCE(handling.event_sequence, 0),
       COALESCE(handling.evidence_kind, ''),
       COALESCE(handling.adjustment_cents, 0)
FROM xiangwan_coupon_entries AS correction
JOIN xiangwan_coupons AS instrument
  ON instrument.tenant_id = correction.tenant_id
 AND instrument.id = correction.coupon_id
JOIN xiangwan_coupon_entries AS related
  ON related.tenant_id = correction.tenant_id
 AND related.coupon_id = correction.coupon_id
 AND related.id = correction.related_entry_id
LEFT JOIN LATERAL (
    SELECT entry_type, order_id, registration_id
    FROM xiangwan_coupon_entries AS used
    WHERE used.tenant_id = correction.tenant_id
      AND used.coupon_id = correction.coupon_id
      AND used.entry_type IN ('held', 'redeemed')
      AND used.recorded_at < $2
    ORDER BY entry_sequence DESC LIMIT 1
) AS transaction_entry ON TRUE
LEFT JOIN LATERAL (
    SELECT event_type, event_sequence, evidence_kind, adjustment_cents
    FROM xiangwan_coupon_correction_events AS action
    WHERE action.tenant_id = correction.tenant_id
      AND action.correction_entry_id = correction.id
      AND action.recorded_at < $2
    ORDER BY event_sequence DESC LIMIT 1
) AS handling ON TRUE
WHERE correction.tenant_id = $1
  AND correction.entry_type = 'correction_required'
  AND correction.recorded_at < $2
ORDER BY correction.recorded_at DESC, correction.id DESC
OFFSET $3 LIMIT $4
`, catalog.tenantID, asOf, (page-1)*pageSize, pageSize)
	if err != nil {
		return xiangwanadmin.CouponCorrectionPage{}, fmt.Errorf("list Coupon corrections: %w", err)
	}
	for rows.Next() {
		var item xiangwanadmin.CouponCorrectionItem
		var orderID, registrationID uuid.NullUUID
		if err := rows.Scan(&item.EntryID, &item.CouponID, &item.FaceValueCents,
			&item.SourceCheckinEventID,
			&item.RelatedEntryType, &orderID, &registrationID, &item.RecordedAt,
			&item.HandlingStatus, &item.HandlingVersion, &item.EvidenceKind,
			&item.AdjustmentCents); err != nil {
			_ = rows.Close()
			return xiangwanadmin.CouponCorrectionPage{}, fmt.Errorf("scan Coupon correction: %w", err)
		}
		if orderID.Valid {
			item.OrderID = &orderID.UUID
		}
		if registrationID.Valid {
			item.RegistrationID = &registrationID.UUID
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return xiangwanadmin.CouponCorrectionPage{}, fmt.Errorf("iterate Coupon corrections: %w", err)
	}
	if err := rows.Close(); err != nil {
		return xiangwanadmin.CouponCorrectionPage{}, fmt.Errorf("close Coupon corrections: %w", err)
	}
	requestID := requestctx.RequestID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'coupon.correction_list_read', 'tenant', $2,
    $4, jsonb_build_object('identity_link_id', $5::TEXT, 'page', $6::INTEGER,
    'page_size', $7::INTEGER, 'returned', $8::INTEGER, 'as_of', $9::TEXT), $10, $10)
`, uuid.New(), catalog.tenantID, principal.PrincipalID, requestID,
		principal.IdentityLinkID.String(), page, pageSize, len(result.Items),
		asOf.Format(time.RFC3339Nano), now)
	if err != nil {
		return xiangwanadmin.CouponCorrectionPage{}, fmt.Errorf("audit Coupon correction read: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.CouponCorrectionPage{}, fmt.Errorf("commit Coupon correction read: %w", err)
	}
	return result, nil
}
