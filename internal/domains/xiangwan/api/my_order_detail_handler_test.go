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
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestMyOrderDetailHandlerReturnsExactConfirmingOutcome(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(181)
	orderID := apiUUID(182)
	asOf := time.Date(2026, time.September, 15, 11, 0, 0, 0, time.UTC)
	service := &fakeMyOrderDetailApplication{detail: booking.MyOrderDetail{
		Item: booking.MyOrderItem{
			OrderID:                    orderID,
			OrderVersion:               2,
			RegistrationID:             apiUUID(183),
			RegistrationVersion:        1,
			SeriesID:                   apiUUID(184),
			SeriesTitle:                "AI Roundtable",
			InstanceID:                 apiUUID(185),
			InstanceTitle:              "AI Roundtable 06",
			SessionID:                  apiUUID(186),
			SessionTitle:               "Sunday Session",
			SessionStartAt:             asOf.Add(24 * time.Hour),
			SessionEndAt:               asOf.Add(26 * time.Hour),
			ParticipationStatus:        registration.ParticipationStatusPendingPayment,
			ReservationState:           booking.MyRegistrationStatePendingPayment,
			ReservationHasActiveAccess: false,
			PaymentStatus:              payment.OrderStatusUnknown,
			PaymentConfirmationPending: true,
			OriginalPriceCents:         9900,
			DiscountCents:              1100,
			PayableCents:               8800,
			Currency:                   booking.MyOrdersCurrency,
			HoldStatus:                 payment.CapacityHoldStatusActive,
			HoldExpiresAt:              asOf.Add(5 * time.Minute),
			HoldVersion:                1,
			CanContinuePayment:         false,
			State:                      booking.MyOrderStatePendingPayment,
			Outcome:                    booking.MyOrderOutcomePaymentConfirming,
			LastBusinessAt:             asOf.Add(-time.Minute),
		},
		AsOf: asOf,
	}}
	engine := gin.New()
	NewMyOrderDetailHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/orders/"+orderID.String(),
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET My Order status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                   `json:"code"`
		Data MyOrderDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode My Order response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.Order.OrderID != orderID.String() ||
		envelope.Data.Order.RegistrationID != apiUUID(183).String() ||
		envelope.Data.Order.SessionID != apiUUID(186).String() ||
		envelope.Data.Order.PaymentStatus != payment.OrderStatusUnknown ||
		!envelope.Data.Order.PaymentConfirmationPending ||
		envelope.Data.Order.Outcome != booking.MyOrderOutcomePaymentConfirming ||
		envelope.Data.Order.CanContinuePayment {
		t.Fatalf("GET My Order response = %+v", envelope.Data)
	}
	if service.calls != 1 || service.principalID != principalID ||
		service.orderID != orderID {
		t.Fatalf("service = %+v", service)
	}
	for _, forbidden := range []string{
		"principal_id",
		"tenant_id",
		"sort_rank",
		"sort_at",
		"payment_app_id",
		"merchant_order_no",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private/internal field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestMyOrderDetailHandlerRejectsInvalidIdentityBeforeRead(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{
		"not-a-uuid",
		strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"),
		apiUUID(187).String() + "?unexpected=true",
	} {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyOrderDetailApplication{}
			engine := gin.New()
			NewMyOrderDetailHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(188), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/me/orders/"+suffix,
					nil,
				),
			)
			if recorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("invalid identity status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
		})
	}
}

func TestMyOrderDetailHandlerUsesOpaqueOwnedNotFound(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "missing or other owner", err: booking.ErrMyOrderNotFound, wantStatus: http.StatusNotFound, wantCode: `"code":10004`},
		{name: "backend", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError, wantCode: `"code":10006`},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := gin.New()
			NewMyOrderDetailHandler(
				&fakeMyOrderDetailApplication{err: test.err},
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(189), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/me/orders/"+apiUUID(190).String(),
					nil,
				),
			)
			body := recorder.Body.String()
			if recorder.Code != test.wantStatus || !strings.Contains(body, test.wantCode) ||
				strings.Contains(body, "database address") {
				t.Fatalf("error status=%d body=%s", recorder.Code, body)
			}
		})
	}
}

type fakeMyOrderDetailApplication struct {
	detail booking.MyOrderDetail
	err    error

	calls       int
	principalID uuid.UUID
	orderID     uuid.UUID
}

func (service *fakeMyOrderDetailApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	orderID uuid.UUID,
) (booking.MyOrderDetail, error) {
	service.calls++
	service.principalID = principalID
	service.orderID = orderID
	return service.detail, service.err
}
