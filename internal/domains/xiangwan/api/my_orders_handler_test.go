package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestMyOrdersHandlerReturnsIndependentPaymentAndRefundFacts(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 15, 9, 0, 0, 0, time.UTC)
	principalID := apiUUID(161)
	paid := int64(8800)
	paidAt := asOf.Add(-24 * time.Hour)
	service := &fakeMyOrdersApplication{page: booking.MyOrdersPage{
		ActiveState: booking.MyOrderStateRefundProcessing,
		AsOf:        asOf,
		NextCursor:  "next-order-cursor",
		Items: []booking.MyOrderItem{{
			OrderID:                    apiUUID(162),
			OrderVersion:               3,
			RegistrationID:             apiUUID(163),
			RegistrationVersion:        4,
			SeriesID:                   apiUUID(164),
			SeriesTitle:                "AI Roundtable",
			InstanceID:                 apiUUID(165),
			InstanceTitle:              "AI Roundtable 05",
			SessionID:                  apiUUID(166),
			SessionTitle:               "Sunday Session",
			SessionStartAt:             asOf.Add(24 * time.Hour),
			SessionEndAt:               asOf.Add(26 * time.Hour),
			ParticipationStatus:        registration.ParticipationStatusCancelled,
			ReservationState:           booking.MyRegistrationStateRefundProcessing,
			ReservationHasActiveAccess: false,
			PaymentStatus:              payment.OrderStatusPaidConfirmed,
			OriginalPriceCents:         9900,
			DiscountCents:              1100,
			PayableCents:               8800,
			ActualPaidCents:            &paid,
			Currency:                   booking.MyOrdersCurrency,
			PaidAt:                     &paidAt,
			HoldStatus:                 payment.CapacityHoldStatusConverted,
			HoldExpiresAt:              paidAt.Add(time.Hour),
			HoldVersion:                2,
			CanContinuePayment:         false,
			Refund: &booking.MyRegistrationRefundSummary{
				RefundCaseID:         apiUUID(167),
				RefundStatus:         refund.StatusPendingManual,
				ReasonCode:           refund.ReasonUserCancelled,
				RequestedRefundCents: 8800,
				Version:              1,
				UpdatedAt:            asOf.Add(-time.Hour),
			},
			State:          booking.MyOrderStateRefundProcessing,
			Outcome:        booking.MyOrderOutcomeRefundPendingManual,
			LastBusinessAt: asOf.Add(-time.Hour),
		}},
	}}
	engine := gin.New()
	NewMyOrdersHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/orders?state=refund_processing&limit=10",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET My Orders status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int              `json:"code"`
		Data MyOrdersResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode My Orders response: %v", err)
	}
	if envelope.Code != 0 ||
		envelope.Data.ActiveState != booking.MyOrderStateRefundProcessing ||
		len(envelope.Data.Items) != 1 ||
		envelope.Data.Items[0].OrderID != apiUUID(162).String() ||
		envelope.Data.Items[0].RegistrationID != apiUUID(163).String() ||
		envelope.Data.Items[0].SessionID != apiUUID(166).String() ||
		envelope.Data.Items[0].PaymentStatus != payment.OrderStatusPaidConfirmed ||
		envelope.Data.Items[0].Outcome != booking.MyOrderOutcomeRefundPendingManual ||
		envelope.Data.Items[0].Refund == nil ||
		envelope.Data.Items[0].Refund.RefundCaseID != apiUUID(167).String() ||
		envelope.Data.NextCursor != "next-order-cursor" {
		t.Fatalf("GET My Orders response = %+v", envelope.Data)
	}
	if service.calls != 1 || service.principalID != principalID ||
		service.request.State != booking.MyOrderStateRefundProcessing ||
		service.request.Limit != 10 {
		t.Fatalf("service = %+v", service)
	}
	for _, forbidden := range []string{
		"principal_id",
		"tenant_id",
		"sort_rank",
		"sort_at",
		"payment_app_id",
		"merchant_order_no",
		"external_refund_id",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private/internal field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestMyOrdersHandlerRejectsAmbiguousQuery(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{
		"?unknown=value",
		"?state=all&state=paid",
		"?state=unknown",
		"?state=",
		"?cursor=%20opaque",
		"?limit=0",
		"?limit=01",
		"?limit=101",
	} {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyOrdersApplication{}
			engine := gin.New()
			NewMyOrdersHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(168), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/me/orders"+suffix,
					nil,
				),
			)
			if recorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("invalid query status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
		})
	}
}

func TestMyOrdersHandlerUsesStableOpaqueErrorsAndEmptyState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		page       booking.MyOrdersPage
		err        error
		wantStatus int
		wantBody   string
	}{
		{name: "invalid", err: booking.ErrInvalidMyOrdersCursor, wantStatus: http.StatusBadRequest, wantBody: `"code":10001`},
		{name: "stale", err: booking.ErrStaleMyOrdersCursor, wantStatus: http.StatusConflict, wantBody: `"code":10005`},
		{name: "backend", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError, wantBody: `"code":10006`},
		{name: "empty", page: booking.MyOrdersPage{ActiveState: booking.MyOrderStateAll, Items: []booking.MyOrderItem{}, AsOf: time.Now().UTC()}, wantStatus: http.StatusOK, wantBody: `"empty_state":"no_orders"`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := gin.New()
			NewMyOrdersHandler(
				&fakeMyOrdersApplication{page: test.page, err: test.err},
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(169), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/orders", nil),
			)
			body := recorder.Body.String()
			if recorder.Code != test.wantStatus || !strings.Contains(body, test.wantBody) ||
				strings.Contains(body, "database address") {
				t.Fatalf("response status=%d body=%s", recorder.Code, body)
			}
		})
	}
}

type fakeMyOrdersApplication struct {
	page booking.MyOrdersPage
	err  error

	calls       int
	principalID uuid.UUID
	request     MyOrdersRequest
}

func (service *fakeMyOrdersApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	request MyOrdersRequest,
) (booking.MyOrdersPage, error) {
	service.calls++
	service.principalID = principalID
	service.request = request
	return service.page, service.err
}
