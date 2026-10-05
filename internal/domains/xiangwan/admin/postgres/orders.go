package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/google/uuid"
)

type adminOrderCursor struct {
	Version   int       `json:"v"`
	TenantID  uuid.UUID `json:"tenant_id"`
	Status    string    `json:"status"`
	AsOf      time.Time `json:"as_of"`
	CreatedAt time.Time `json:"created_at"`
	OrderID   uuid.UUID `json:"order_id"`
}

func validAdminOrderStatus(status string) bool {
	switch status {
	case "all", string(payment.OrderStatusPending), string(payment.OrderStatusUnknown),
		string(payment.OrderStatusPaidConfirmed), string(payment.OrderStatusSettledZero),
		string(payment.OrderStatusClosedUnpaid):
		return true
	default:
		return false
	}
}

func decodeAdminOrderCursor(value string) (adminOrderCursor, error) {
	if len(value) > 1024 {
		return adminOrderCursor{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return adminOrderCursor{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var cursor adminOrderCursor
	if err := decoder.Decode(&cursor); err != nil {
		return adminOrderCursor{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return adminOrderCursor{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if cursor.Version != 1 || cursor.TenantID == uuid.Nil ||
		!validAdminOrderStatus(cursor.Status) || cursor.AsOf.IsZero() ||
		cursor.CreatedAt.IsZero() || !cursor.CreatedAt.Before(cursor.AsOf) ||
		cursor.OrderID == uuid.Nil {
		return adminOrderCursor{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	return cursor, nil
}

func encodeAdminOrderCursor(value adminOrderCursor) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode administrator Order cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func normalizeAdminOrderFilter(
	tenantID uuid.UUID,
	filter xiangwanadmin.OrderListFilter,
	now time.Time,
) (xiangwanadmin.OrderListFilter, time.Time, *adminOrderCursor, error) {
	if tenantID == uuid.Nil || now.IsZero() || filter.Limit < 0 ||
		filter.Limit > xiangwanadmin.MaxOrderListLimit {
		return xiangwanadmin.OrderListFilter{}, time.Time{}, nil,
			xiangwanadmin.ErrInvalidCatalogRequest
	}
	if filter.Status == "" {
		filter.Status = "all"
	}
	if !validAdminOrderStatus(filter.Status) {
		return xiangwanadmin.OrderListFilter{}, time.Time{}, nil,
			xiangwanadmin.ErrInvalidCatalogRequest
	}
	if filter.Limit == 0 {
		filter.Limit = xiangwanadmin.DefaultOrderListLimit
	}
	now = now.UTC().Truncate(time.Microsecond)
	if filter.Cursor == "" {
		return filter, now, nil, nil
	}
	cursor, err := decodeAdminOrderCursor(filter.Cursor)
	if err != nil || cursor.TenantID != tenantID ||
		cursor.Status != filter.Status || cursor.AsOf.After(now.Add(time.Minute)) {
		return xiangwanadmin.OrderListFilter{}, time.Time{}, nil,
			xiangwanadmin.ErrInvalidCatalogRequest
	}
	return filter, cursor.AsOf, &cursor, nil
}

const adminOrderProjectionAndJoins = `
SELECT
    activity_order.id, activity_order.registration_id,
    activity_order.instance_id, activity_order.session_id,
    activity_series.title, activity_instance.title, activity_session.title,
    activity_order.payment_status, activity_order.original_price_cents,
    activity_order.discount_cents, activity_order.payable_cents,
    activity_order.actual_paid_cents,
    refund_case.id, COALESCE(refund_case.refund_status, ''),
    COALESCE(refund_case.requested_refund_cents, 0),
    COALESCE(refund_case.successful_refund_cents, 0),
    activity_order.created_at, activity_order.updated_at,
    activity_order.paid_at, activity_order.closed_at
FROM xiangwan_orders AS activity_order
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_order.tenant_id
 AND activity_series.id = activity_order.series_id
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = activity_order.tenant_id
 AND activity_instance.series_id = activity_order.series_id
 AND activity_instance.id = activity_order.instance_id
JOIN xiangwan_activity_sessions AS activity_session
  ON activity_session.tenant_id = activity_order.tenant_id
 AND activity_session.instance_id = activity_order.instance_id
 AND activity_session.id = activity_order.session_id
LEFT JOIN xiangwan_refund_cases AS refund_case
  ON refund_case.tenant_id = activity_order.tenant_id
 AND refund_case.order_id = activity_order.id
`

const adminOrdersQueryBase = adminOrderProjectionAndJoins + `
WHERE activity_order.tenant_id = $1
  AND ($2::TEXT = 'all' OR activity_order.payment_status = $2)
  AND activity_order.created_at < $3
`

func (catalog *Catalog) ListOrders(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	filter xiangwanadmin.OrderListFilter,
) (xiangwanadmin.OrderListPage, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil {
		return xiangwanadmin.OrderListPage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	now := catalog.now().UTC().Truncate(time.Microsecond)
	filter, asOf, cursor, err := normalizeAdminOrderFilter(
		catalog.tenantID, filter, now,
	)
	if err != nil {
		return xiangwanadmin.OrderListPage{}, err
	}
	tx, err := catalog.beginAuthorizedOrderRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.OrderListPage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var query strings.Builder
	query.WriteString(adminOrdersQueryBase)
	args := []any{catalog.tenantID, filter.Status, asOf}
	if cursor != nil {
		query.WriteString("  AND (activity_order.created_at, activity_order.id) < ($4, $5)\n")
		args = append(args, cursor.CreatedAt, cursor.OrderID)
	}
	query.WriteString(fmt.Sprintf("ORDER BY activity_order.created_at DESC, activity_order.id DESC\nLIMIT $%d", len(args)+1))
	args = append(args, filter.Limit+1)
	rows, err := tx.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return xiangwanadmin.OrderListPage{}, fmt.Errorf("list administrator Orders: %w", err)
	}
	defer func() { _ = rows.Close() }()
	items := make([]xiangwanadmin.OrderListItem, 0, filter.Limit+1)
	for rows.Next() {
		item, err := scanAdminOrder(rows)
		if err != nil {
			return xiangwanadmin.OrderListPage{}, fmt.Errorf("scan administrator Order: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return xiangwanadmin.OrderListPage{}, fmt.Errorf("iterate administrator Orders: %w", err)
	}
	if err := rows.Close(); err != nil {
		return xiangwanadmin.OrderListPage{}, fmt.Errorf("close administrator Orders: %w", err)
	}
	nextCursor := ""
	if len(items) > filter.Limit {
		items = items[:filter.Limit]
		last := items[len(items)-1]
		nextCursor, err = encodeAdminOrderCursor(adminOrderCursor{
			Version: 1, TenantID: catalog.tenantID, Status: filter.Status,
			AsOf: asOf, CreatedAt: last.CreatedAt.UTC(), OrderID: last.OrderID,
		})
		if err != nil {
			return xiangwanadmin.OrderListPage{}, err
		}
	}
	requestID := requestctx.RequestID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'order.list_read', 'tenant', $2,
    $4, jsonb_build_object('identity_link_id', $5::TEXT, 'status', $6::TEXT,
    'limit', $7::INTEGER, 'returned', $8::INTEGER), $9, $9)
`, uuid.New(), catalog.tenantID, principal.PrincipalID, requestID,
		principal.IdentityLinkID.String(), filter.Status, filter.Limit,
		len(items), now); err != nil {
		return xiangwanadmin.OrderListPage{}, fmt.Errorf("audit administrator Order list read: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.OrderListPage{}, fmt.Errorf("commit administrator Order list read: %w", err)
	}
	return xiangwanadmin.OrderListPage{Items: items, Status: filter.Status, NextCursor: nextCursor}, nil
}

type adminOrderRowScanner interface {
	Scan(...any) error
}

func scanAdminOrder(row adminOrderRowScanner) (xiangwanadmin.OrderListItem, error) {
	var item xiangwanadmin.OrderListItem
	var actualPaid sql.NullInt64
	var refundCase uuid.NullUUID
	var refundStatus string
	var paidAt, closedAt sql.NullTime
	if err := row.Scan(
		&item.OrderID, &item.RegistrationID, &item.InstanceID,
		&item.SessionID, &item.SeriesTitle, &item.InstanceTitle,
		&item.SessionTitle, &item.PaymentStatus, &item.OriginalPriceCents,
		&item.DiscountCents, &item.PayableCents, &actualPaid,
		&refundCase, &refundStatus, &item.RequestedRefundCents,
		&item.SuccessfulRefundCents, &item.CreatedAt, &item.UpdatedAt,
		&paidAt, &closedAt,
	); err != nil {
		return xiangwanadmin.OrderListItem{}, err
	}
	if actualPaid.Valid {
		item.ActualPaidCents = &actualPaid.Int64
	}
	if refundCase.Valid {
		item.RefundCaseID = &refundCase.UUID
	}
	item.RefundStatus = refund.Status(refundStatus)
	if paidAt.Valid {
		item.PaidAt = &paidAt.Time
	}
	if closedAt.Valid {
		item.ClosedAt = &closedAt.Time
	}
	return item, nil
}

func (catalog *Catalog) GetOrder(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	orderID uuid.UUID,
) (xiangwanadmin.OrderListItem, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || orderID == uuid.Nil {
		return xiangwanadmin.OrderListItem{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedOrderRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.OrderListItem{}, err
	}
	defer func() { _ = tx.Rollback() }()
	value, err := scanAdminOrder(tx.QueryRowContext(ctx,
		adminOrderProjectionAndJoins+`
WHERE activity_order.tenant_id = $1 AND activity_order.id = $2`,
		catalog.tenantID, orderID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.OrderListItem{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.OrderListItem{}, fmt.Errorf("read administrator Order detail: %w", err)
	}
	requestID := requestctx.RequestID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	now := catalog.now().UTC()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'order.detail_read', 'order', $4,
    $5, jsonb_build_object('identity_link_id', $6::TEXT), $7, $7)
`, uuid.New(), catalog.tenantID, principal.PrincipalID, orderID,
		requestID, principal.IdentityLinkID.String(), now); err != nil {
		return xiangwanadmin.OrderListItem{}, fmt.Errorf("audit administrator Order detail read: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.OrderListItem{}, fmt.Errorf("commit administrator Order detail read: %w", err)
	}
	return value, nil
}
