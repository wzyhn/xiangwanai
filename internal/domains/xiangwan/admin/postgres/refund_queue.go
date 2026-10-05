package postgres

import (
	"context"
	"errors"
	"fmt"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	refundpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/google/uuid"
)

func (catalog *Catalog) ListRefundQueue(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	filter xiangwanadmin.RefundQueueFilter,
) (xiangwanadmin.RefundQueuePage, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || filter.Limit < 0 ||
		filter.Limit > refund.MaxQueueLimit ||
		(filter.Status != "" && filter.Status != refund.StatusPendingManual &&
			filter.Status != refund.StatusProcessing && filter.Status != refund.StatusFailed) {
		return xiangwanadmin.RefundQueuePage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if filter.Status == "" {
		filter.Status = refund.StatusPendingManual
	}
	if filter.Limit == 0 {
		filter.Limit = refund.DefaultQueueLimit
	}
	tx, err := catalog.beginAuthorizedFinanceRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.RefundQueuePage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	page, err := refundpostgres.NewRepository(tx).ListQueue(ctx, refund.QueueFilter{
		TenantID: catalog.tenantID, Status: filter.Status,
		Limit: filter.Limit, Cursor: filter.Cursor,
	})
	if errors.Is(err, refundpostgres.ErrInvalidRefundQueueFilter) ||
		errors.Is(err, refundpostgres.ErrInvalidRefundQueueCursor) ||
		errors.Is(err, refundpostgres.ErrStaleRefundQueueCursor) {
		return xiangwanadmin.RefundQueuePage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if err != nil {
		return xiangwanadmin.RefundQueuePage{}, fmt.Errorf("read administrator refund queue: %w", err)
	}
	result := xiangwanadmin.RefundQueuePage{
		Items:  make([]xiangwanadmin.RefundQueueItem, 0, len(page.Items)),
		Status: page.ActiveStatus, NextCursor: page.NextCursor,
	}
	for _, item := range page.Items {
		result.Items = append(result.Items, projectRefundQueueItem(item))
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
) VALUES ($1, $2, $3, 'refund.queue_read', 'tenant', $2,
    $4, jsonb_build_object('identity_link_id', $5::TEXT, 'status', $6::TEXT,
    'limit', $7::INTEGER, 'returned', $8::INTEGER), $9, $9)
`, uuid.New(), catalog.tenantID, principal.PrincipalID, requestID,
		principal.IdentityLinkID.String(), string(result.Status), filter.Limit,
		len(result.Items), now); err != nil {
		return xiangwanadmin.RefundQueuePage{}, fmt.Errorf("audit administrator refund queue read: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.RefundQueuePage{}, fmt.Errorf("commit administrator refund queue read: %w", err)
	}
	return result, nil
}

func (catalog *Catalog) GetRefundCase(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	caseID uuid.UUID,
) (xiangwanadmin.RefundCaseDetail, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || caseID == uuid.Nil {
		return xiangwanadmin.RefundCaseDetail{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedFinanceRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.RefundCaseDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()
	detail, err := refundpostgres.NewRepository(tx).GetCaseDetail(ctx, catalog.tenantID, caseID)
	if errors.Is(err, refundpostgres.ErrRefundCaseNotFound) {
		return xiangwanadmin.RefundCaseDetail{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.RefundCaseDetail{}, fmt.Errorf("read administrator refund case: %w", err)
	}
	result := xiangwanadmin.RefundCaseDetail{
		Case:   projectRefundQueueItem(detail.Item),
		Events: make([]xiangwanadmin.RefundCaseEvent, 0, len(detail.Events)),
	}
	for _, event := range detail.Events {
		result.Events = append(result.Events, xiangwanadmin.RefundCaseEvent{
			Sequence: event.EventSequence, Type: event.EventType,
			FromStatus: event.FromStatus, ToStatus: event.ToStatus,
			SuccessfulRefundCents:  event.SuccessfulRefundCents,
			ResultingRefundVersion: event.ResultingRefundVersion,
			OccurredAt:             event.OccurredAt,
		})
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
) VALUES ($1, $2, $3, 'refund.case_read', 'refund_case', $4,
    $5, jsonb_build_object('identity_link_id', $6::TEXT, 'event_count', $7::INTEGER), $8, $8)
`, uuid.New(), catalog.tenantID, principal.PrincipalID, caseID,
		requestID, principal.IdentityLinkID.String(), len(result.Events), now); err != nil {
		return xiangwanadmin.RefundCaseDetail{}, fmt.Errorf("audit administrator refund case read: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.RefundCaseDetail{}, fmt.Errorf("commit administrator refund case read: %w", err)
	}
	return result, nil
}

func projectRefundQueueItem(item refund.QueueItem) xiangwanadmin.RefundQueueItem {
	return xiangwanadmin.RefundQueueItem{
		CaseID: item.Case.ID, OrderID: item.Case.OrderID,
		RegistrationID: item.Case.RegistrationID,
		InstanceID:     item.Case.InstanceID, SessionID: item.Case.SessionID,
		SeriesTitle: item.SeriesTitle, InstanceTitle: item.InstanceTitle,
		SessionTitle: item.SessionTitle, Status: item.Case.RefundStatus,
		ReasonCode:            item.Case.ReasonCode,
		RequestedRefundCents:  item.Case.RequestedRefundCents,
		SuccessfulRefundCents: item.Case.SuccessfulRefundCents,
		Version:               item.Case.Version,
		CreatedAt:             item.Case.CreatedAt, UpdatedAt: item.Case.UpdatedAt,
	}
}
