package xiangwanapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

type refundQueueCatalog struct {
	xiangwanadmin.Catalog
	read func(context.Context, xiangwanadmin.Principal, xiangwanadmin.RefundQueueFilter) (xiangwanadmin.RefundQueuePage, error)
}

type refundCaseCatalog struct {
	xiangwanadmin.Catalog
	read func(context.Context, xiangwanadmin.Principal, uuid.UUID) (xiangwanadmin.RefundCaseDetail, error)
}

func (catalog refundCaseCatalog) GetRefundCase(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	caseID uuid.UUID,
) (xiangwanadmin.RefundCaseDetail, error) {
	return catalog.read(ctx, principal, caseID)
}

func TestGetRefundCaseRejectsInvalidIDBeforeCatalogRead(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := refundCaseCatalog{read: func(
		context.Context, xiangwanadmin.Principal, uuid.UUID,
	) (xiangwanadmin.RefundCaseDetail, error) {
		called = true
		return xiangwanadmin.RefundCaseDetail{}, nil
	}}
	result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/refund-cases/not-a-uuid", nil))
	if result.Code != http.StatusBadRequest || called {
		t.Fatalf("refund detail status/called = %d/%t, want 400/false", result.Code, called)
	}
}

func TestGetRefundCaseProjectsOnlyStatusTimeline(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	caseID := apiUUID(251)
	catalog := refundCaseCatalog{read: func(
		_ context.Context, actor xiangwanadmin.Principal, target uuid.UUID,
	) (xiangwanadmin.RefundCaseDetail, error) {
		if actor.PrincipalID != principal.PrincipalID || target != caseID {
			t.Fatalf("unexpected refund detail target: %s", target)
		}
		return xiangwanadmin.RefundCaseDetail{
			Case: xiangwanadmin.RefundQueueItem{
				CaseID: caseID, OrderID: apiUUID(252), RegistrationID: apiUUID(253),
				InstanceID: apiUUID(254), SessionID: apiUUID(255),
				Status: refund.StatusRefunded, ReasonCode: refund.ReasonUserCancelled,
				RequestedRefundCents:  9007199254740993,
				SuccessfulRefundCents: 9007199254740993,
				CreatedAt:             time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC),
				UpdatedAt:             time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC),
			},
			Events: []xiangwanadmin.RefundCaseEvent{{
				Sequence: 1, Type: refund.EventTypeRefundCompleted,
				FromStatus: refund.StatusPendingManual, ToStatus: refund.StatusRefunded,
				SuccessfulRefundCents: 9007199254740993,
				OccurredAt:            time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC),
			}},
		}, nil
	}}
	result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/refund-cases/"+caseID.String(), nil))
	if result.Code != http.StatusOK || result.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("refund detail status/cache = %d/%q", result.Code, result.Header().Get("Cache-Control"))
	}
	body := result.Body.String()
	if !strings.Contains(body, `"successful_refund_cents":"9007199254740993"`) ||
		!strings.Contains(body, `"type":"refund_completed"`) ||
		strings.Contains(body, "actor_id") || strings.Contains(body, "operator_note") ||
		strings.Contains(body, "external_refund_id") ||
		strings.Contains(body, "evidence_reference") ||
		strings.Contains(body, "idempotency_key") {
		t.Fatalf("refund detail leaked private fields or omitted status timeline: %s", body)
	}
}

func (catalog refundQueueCatalog) ListRefundQueue(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	filter xiangwanadmin.RefundQueueFilter,
) (xiangwanadmin.RefundQueuePage, error) {
	return catalog.read(ctx, principal, filter)
}

func TestListRefundQueueRejectsInvalidStatusAndPagination(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := refundQueueCatalog{read: func(
		context.Context, xiangwanadmin.Principal, xiangwanadmin.RefundQueueFilter,
	) (xiangwanadmin.RefundQueuePage, error) {
		called = true
		return xiangwanadmin.RefundQueuePage{}, nil
	}}
	for _, suffix := range []string{"?status=refunded", "?limit=101", "?limit=-1"} {
		result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(
			http.MethodGet, "/api/v1/xiangwan/admin/refund-cases"+suffix, nil,
		))
		if result.Code != http.StatusBadRequest || called {
			t.Fatalf("refund queue %q status/called = %d/%t, want 400/false", suffix, result.Code, called)
		}
	}
}

func TestListRefundQueueProjectsOnlyRoutingAndStatusMetadata(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	catalog := refundQueueCatalog{read: func(
		_ context.Context, actor xiangwanadmin.Principal, filter xiangwanadmin.RefundQueueFilter,
	) (xiangwanadmin.RefundQueuePage, error) {
		if actor.PrincipalID != principal.PrincipalID || filter.Status != refund.StatusFailed ||
			filter.Limit != 10 || filter.Cursor != "opaque-cursor" {
			t.Fatalf("unexpected refund queue filter: %+v", filter)
		}
		return xiangwanadmin.RefundQueuePage{
			Items: []xiangwanadmin.RefundQueueItem{{
				CaseID: apiUUID(241), OrderID: apiUUID(242),
				RegistrationID: apiUUID(243), InstanceID: apiUUID(244),
				SessionID: apiUUID(245), SeriesTitle: "周末活动",
				InstanceTitle: "第一期", SessionTitle: "上午场",
				Status: refund.StatusFailed, ReasonCode: refund.ReasonUserCancelled,
				RequestedRefundCents: 1200, Version: 2,
				CreatedAt: time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC),
				UpdatedAt: time.Date(2026, 9, 30, 4, 5, 6, 0, time.UTC),
			}},
			Status: refund.StatusFailed, NextCursor: "next-token",
		}, nil
	}}
	result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/refund-cases?status=failed&limit=10&cursor=opaque-cursor", nil))
	if result.Code != http.StatusOK || result.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("refund queue status/cache = %d/%q", result.Code, result.Header().Get("Cache-Control"))
	}
	body := result.Body.String()
	if !strings.Contains(body, `"requested_refund_cents":"1200"`) ||
		!strings.Contains(body, `"next_cursor":"next-token"`) ||
		strings.Contains(body, "principal_id") || strings.Contains(body, "operator_note") ||
		strings.Contains(body, "evidence_reference") || strings.Contains(body, "idempotency_key") {
		t.Fatalf("refund queue response leaked private fields or omitted status metadata: %s", body)
	}
}
