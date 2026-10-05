package xiangwanapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

type orderListCatalog struct {
	xiangwanadmin.Catalog
	read func(context.Context, xiangwanadmin.Principal, xiangwanadmin.OrderListFilter) (xiangwanadmin.OrderListPage, error)
}

func (catalog orderListCatalog) ListOrders(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	filter xiangwanadmin.OrderListFilter,
) (xiangwanadmin.OrderListPage, error) {
	return catalog.read(ctx, principal, filter)
}

func TestListAdminOrdersRejectsInvalidFilterBeforeCatalogRead(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := orderListCatalog{read: func(
		context.Context, xiangwanadmin.Principal, xiangwanadmin.OrderListFilter,
	) (xiangwanadmin.OrderListPage, error) {
		called = true
		return xiangwanadmin.OrderListPage{}, nil
	}}
	for _, suffix := range []string{"?status=refunded", "?limit=101", "?limit=-1"} {
		result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
			"/api/v1/xiangwan/admin/orders"+suffix, nil))
		if result.Code != http.StatusBadRequest || called {
			t.Fatalf("admin Orders %q status/called = %d/%t, want 400/false", suffix, result.Code, called)
		}
	}
}

func TestListAdminOrdersProjectsAmountAndIndependentStates(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	actualPaid := int64(9007199254740993)
	refundCaseID := apiUUID(111)
	catalog := orderListCatalog{read: func(
		_ context.Context, actor xiangwanadmin.Principal, filter xiangwanadmin.OrderListFilter,
	) (xiangwanadmin.OrderListPage, error) {
		if actor.PrincipalID != principal.PrincipalID || filter.Status != "paid_confirmed" ||
			filter.Limit != 10 || filter.Cursor != "opaque" {
			t.Fatalf("unexpected Order filter: %+v", filter)
		}
		return xiangwanadmin.OrderListPage{
			Items: []xiangwanadmin.OrderListItem{{
				OrderID: apiUUID(112), RegistrationID: apiUUID(113),
				InstanceID: apiUUID(114), SessionID: apiUUID(115),
				SeriesTitle: "周末活动", InstanceTitle: "第一期", SessionTitle: "上午场",
				PaymentStatus:      payment.OrderStatusPaidConfirmed,
				OriginalPriceCents: actualPaid, PayableCents: actualPaid,
				ActualPaidCents: &actualPaid, RefundCaseID: &refundCaseID,
				RefundStatus:         refund.StatusPendingManual,
				RequestedRefundCents: actualPaid,
				CreatedAt:            time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC),
				UpdatedAt:            time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC),
			}},
			Status: "paid_confirmed", NextCursor: "next-page",
		}, nil
	}}
	result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/orders?status=paid_confirmed&limit=10&cursor=opaque", nil))
	if result.Code != http.StatusOK || result.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("admin Orders status/cache = %d/%q", result.Code, result.Header().Get("Cache-Control"))
	}
	body := result.Body.String()
	if !strings.Contains(body, `"actual_paid_cents":"9007199254740993"`) ||
		!strings.Contains(body, `"refund_status":"pending_manual"`) ||
		!strings.Contains(body, `"next_cursor":"next-page"`) ||
		strings.Contains(body, "principal_id") || strings.Contains(body, "merchant_order_no") ||
		strings.Contains(body, "wechat_transaction_id") ||
		strings.Contains(body, "contact_phone") {
		t.Fatalf("admin Orders leaked sensitive fields or omitted payment/refund facts: %s", body)
	}
}

type orderDetailCatalog struct {
	xiangwanadmin.Catalog
	read func(context.Context, xiangwanadmin.Principal, uuid.UUID) (xiangwanadmin.OrderListItem, error)
}

func (catalog orderDetailCatalog) GetOrder(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	orderID uuid.UUID,
) (xiangwanadmin.OrderListItem, error) {
	return catalog.read(ctx, principal, orderID)
}

func TestGetAdminOrderRequiresExactIDAndProjectsSafeFacts(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	orderID := apiUUID(121)
	called := false
	catalog := orderDetailCatalog{read: func(
		_ context.Context, actor xiangwanadmin.Principal, target uuid.UUID,
	) (xiangwanadmin.OrderListItem, error) {
		called = true
		if actor.PrincipalID != principal.PrincipalID || target != orderID {
			t.Fatalf("unexpected Order detail target: %s", target)
		}
		return xiangwanadmin.OrderListItem{
			OrderID: orderID, RegistrationID: apiUUID(122),
			InstanceID: apiUUID(123), SessionID: apiUUID(124),
			PaymentStatus:      payment.OrderStatusUnknown,
			OriginalPriceCents: 4900, PayableCents: 4900,
			CreatedAt: time.Date(2026, 9, 30, 1, 0, 0, 0, time.UTC),
			UpdatedAt: time.Date(2026, 9, 30, 2, 0, 0, 0, time.UTC),
		}, nil
	}}
	bad := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/orders/not-a-uuid", nil))
	if bad.Code != http.StatusBadRequest || called {
		t.Fatalf("invalid Order ID status/called = %d/%t, want 400/false", bad.Code, called)
	}
	result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/orders/"+orderID.String(), nil))
	if result.Code != http.StatusOK || !called ||
		result.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Order detail status/called/cache = %d/%t/%q", result.Code,
			called, result.Header().Get("Cache-Control"))
	}
	body := result.Body.String()
	if !strings.Contains(body, `"payment_status":"unknown"`) ||
		!strings.Contains(body, `"actual_paid_cents":null`) ||
		strings.Contains(body, "merchant_order_no") ||
		strings.Contains(body, "wechat_transaction_id") ||
		strings.Contains(body, "contact_phone") {
		t.Fatalf("Order detail leaked sensitive fields or claimed payment success: %s", body)
	}
}
