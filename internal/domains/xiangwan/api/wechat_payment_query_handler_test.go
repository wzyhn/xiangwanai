package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestWeChatPaymentQueryHandlerReturnsOnlyLocalConvergence(t *testing.T) {
	t.Parallel()

	principalID := uuid.New()
	orderID := uuid.New()
	nextQueryAt := time.Date(2026, time.September, 19, 2, 3, 6, 0, time.UTC)
	application := &fakeWeChatPaymentQueryApplication{result: payment.PaymentQueryResult{
		Order: payment.Order{
			ID:                  orderID,
			PaymentStatus:       payment.OrderStatusUnknown,
			PaymentAppID:        "private-app-id",
			PaymentMerchantID:   "private-merchant-id",
			MerchantOrderNo:     "private-provider-order",
			WeChatTransactionID: stringPointer("private-transaction-id"),
		},
		QueryStatus: payment.PaymentQueryStatusUnknown,
		NextQueryAt: &nextQueryAt,
	}}
	principalCalls := 0
	engine := gin.New()
	NewWeChatPaymentQueryHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) {
			principalCalls++
			return principalID, nil
		},
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, newValidWeChatPaymentQueryRequest(orderID))

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || application.calls != 1 ||
		principalCalls != 1 || application.principalID != principalID ||
		application.orderID != orderID ||
		!strings.Contains(body, `"payment_status":"unknown"`) ||
		!strings.Contains(body, `"query_status":"unknown"`) ||
		!strings.Contains(body, `"confirmation_pending":true`) ||
		!strings.Contains(body, `"retry_payment_allowed":false`) ||
		!strings.Contains(body, `"next_action":"retry_payment_query"`) ||
		!strings.Contains(body, `"next_query_at":"`+nextQueryAt.Format(time.RFC3339Nano)+`"`) {
		t.Fatalf("query status=%d application=%+v body=%s", recorder.Code, application, body)
	}
	for _, forbidden := range []string{
		"private-app-id",
		"private-merchant-id",
		"private-provider-order",
		"private-transaction-id",
		"payment_app_id",
		"payment_merchant_id",
		"out_trade_no",
		"transaction_id",
		"provider_request_id",
		"trade_state",
		"openid",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("private provider fact %q crossed HTTP boundary: %s", forbidden, body)
		}
	}
}

func TestWeChatPaymentQueryHandlerAcceptsJSONContentTypeWithoutBody(t *testing.T) {
	t.Parallel()

	orderID := uuid.New()
	application := &fakeWeChatPaymentQueryApplication{result: payment.PaymentQueryResult{
		Order:       payment.Order{ID: orderID, PaymentStatus: payment.OrderStatusPending},
		QueryStatus: payment.PaymentQueryStatusPending,
	}}
	engine := gin.New()
	NewWeChatPaymentQueryHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	request := newValidWeChatPaymentQueryRequest(orderID)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || application.calls != 1 {
		t.Fatalf("JSON empty query status=%d application=%+v", recorder.Code, application)
	}
}

func TestWeChatPaymentQueryResponseProjectsOnlyRetryDecision(t *testing.T) {
	t.Parallel()
	projected := projectWeChatPaymentQueryResponse(payment.PaymentQueryResult{
		Order:               payment.Order{ID: uuid.New(), PaymentStatus: payment.OrderStatusPending},
		QueryStatus:         payment.PaymentQueryStatusPending,
		RetryPaymentAllowed: true,
	})
	if !projected.RetryPaymentAllowed || projected.NextAction != "retry_payment_query" ||
		projected.PaymentStatus != payment.OrderStatusPending {
		t.Fatalf("unexpected payment retry projection: %+v", projected)
	}
}

func TestWeChatPaymentQueryHandlerProjectsConfirmedRefundDisposition(t *testing.T) {
	t.Parallel()

	orderID := uuid.New()
	application := &fakeWeChatPaymentQueryApplication{result: payment.PaymentQueryResult{
		Order: payment.Order{
			ID:            orderID,
			PaymentStatus: payment.OrderStatusPaidConfirmed,
		},
		QueryStatus: payment.PaymentQueryStatusConverged,
		Disposition: payment.PaymentConfirmationDispositionRefundRequired,
	}}
	engine := gin.New()
	NewWeChatPaymentQueryHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, newValidWeChatPaymentQueryRequest(orderID))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK ||
		!strings.Contains(body, `"payment_status":"paid_confirmed"`) ||
		!strings.Contains(body, `"confirmation_pending":false`) ||
		!strings.Contains(body, `"disposition":"refund_required"`) ||
		!strings.Contains(body, `"next_action":"refund_processing"`) {
		t.Fatalf("confirmed query status=%d body=%s", recorder.Code, body)
	}
}

func TestWeChatPaymentQueryHandlerProjectsTerminalUnpaidClosure(t *testing.T) {
	t.Parallel()

	orderID := uuid.New()
	application := &fakeWeChatPaymentQueryApplication{result: payment.PaymentQueryResult{
		Order: payment.Order{
			ID:            orderID,
			PaymentStatus: payment.OrderStatusClosedUnpaid,
		},
		QueryStatus: payment.PaymentQueryStatusClosed,
	}}
	engine := gin.New()
	NewWeChatPaymentQueryHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, newValidWeChatPaymentQueryRequest(orderID))
	body := recorder.Body.String()
	if recorder.Code != http.StatusOK ||
		!strings.Contains(body, `"payment_status":"closed_unpaid"`) ||
		!strings.Contains(body, `"query_status":"closed"`) ||
		!strings.Contains(body, `"confirmation_pending":false`) ||
		!strings.Contains(body, `"next_action":"payment_closed"`) ||
		strings.Contains(body, `"next_query_at"`) {
		t.Fatalf("closed query status=%d body=%s", recorder.Code, body)
	}
}

func TestWeChatPaymentQueryHandlerRejectsClientPaymentFactsBeforePrincipal(t *testing.T) {
	t.Parallel()

	orderID := uuid.New()
	validPath := "/api/v1/xiangwan/orders/" + orderID.String() +
		"/payment-queries"
	tests := []struct {
		name    string
		path    string
		body    string
		headers map[string]string
	}{
		{name: "query parameters", path: validPath + "?trade_state=SUCCESS"},
		{name: "body", path: validPath, body: `{"paid":true}`},
		{name: "unsupported content type", path: validPath, headers: map[string]string{"Content-Type": "text/plain"}},
		{name: "client idempotency", path: validPath, headers: map[string]string{"Idempotency-Key": uuid.New().String()}},
		{name: "invalid order", path: "/api/v1/xiangwan/orders/not-a-uuid/payment-queries"},
		{name: "uppercase order", path: "/api/v1/xiangwan/orders/" + strings.ToUpper(orderID.String()) + "/payment-queries"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeWeChatPaymentQueryApplication{}
			principalCalls := 0
			engine := gin.New()
			NewWeChatPaymentQueryHandler(
				application,
				func(*gin.Context) (uuid.UUID, error) {
					principalCalls++
					return uuid.New(), nil
				},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			var body *strings.Reader
			if test.body == "" {
				body = strings.NewReader("")
			} else {
				body = strings.NewReader(test.body)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, body)
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || application.calls != 0 ||
				principalCalls != 0 {
				t.Fatalf("status=%d app=%d principal=%d body=%s", recorder.Code, application.calls, principalCalls, recorder.Body.String())
			}
		})
	}
}

func TestWeChatPaymentQueryHandlerMapsSafeFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "missing", err: paymentpostgres.ErrPaymentQueryOrderNotFound, wantStatus: http.StatusNotFound},
		{name: "disabled", err: ErrWeChatPaymentQueryDisabled, wantStatus: http.StatusConflict},
		{name: "unavailable", err: paymentpostgres.ErrPaymentQueryUnavailable, wantStatus: http.StatusConflict},
		{name: "generation", err: paymentpostgres.ErrPaymentQueryGenerationInactive, wantStatus: http.StatusServiceUnavailable},
		{name: "conflict", err: paymentpostgres.ErrPaymentConfirmationConflict, wantStatus: http.StatusConflict},
		{name: "backend", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := gin.New()
			NewWeChatPaymentQueryHandler(
				&fakeWeChatPaymentQueryApplication{err: test.err},
				func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, newValidWeChatPaymentQueryRequest(uuid.New()))
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func newValidWeChatPaymentQueryRequest(orderID uuid.UUID) *http.Request {
	return httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/orders/"+orderID.String()+"/payment-queries",
		nil,
	)
}

type fakeWeChatPaymentQueryApplication struct {
	result payment.PaymentQueryResult
	err    error

	calls       int
	principalID uuid.UUID
	orderID     uuid.UUID
}

func (application *fakeWeChatPaymentQueryApplication) Query(
	_ context.Context,
	principalID uuid.UUID,
	orderID uuid.UUID,
) (payment.PaymentQueryResult, error) {
	application.calls++
	application.principalID = principalID
	application.orderID = orderID
	return application.result, application.err
}
