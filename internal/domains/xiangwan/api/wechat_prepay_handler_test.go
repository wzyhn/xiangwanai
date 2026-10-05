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

func TestWeChatPrepayHandlerReturnsOnlyMiniProgramPaymentParameters(t *testing.T) {
	t.Parallel()

	principalID := uuid.New()
	orderID := uuid.New()
	idempotencyKey := uuid.New()
	attemptID := uuid.New()
	holdExpiresAt := time.Date(2026, time.September, 18, 3, 14, 5, 6, time.UTC)
	application := &fakeWeChatPrepayApplication{
		result: payment.PrepayAttemptResult{
			Attempt: payment.PaymentAttempt{
				ID:                attemptID,
				OrderID:           orderID,
				AttemptStatus:     payment.PrepayAttemptStatusReady,
				PaymentAppID:      "private-app-id",
				PaymentMerchantID: "private-merchant-id",
				OutTradeNo:        "private-provider-order",
				ProviderRequestID: stringPointer("private-provider-request"),
				PrepayID:          stringPointer("private-prepay-id"),
			},
			HoldExpiresAt: holdExpiresAt,
			Parameters: &payment.MiniProgramPaymentParameters{
				AppID:     "private-app-id",
				TimeStamp: "1789701245",
				NonceStr:  "safe-client-nonce",
				Package:   "prepay_id=client-package-value",
				SignType:  "RSA",
				PaySign:   "safe-client-signature",
			},
		},
	}
	principalCalls := 0
	engine := gin.New()
	NewWeChatPrepayHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) {
			principalCalls++
			return principalID, nil
		},
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/orders/"+orderID.String()+
			"/wechat-prepay-attempts",
		strings.NewReader(`{"order_version":3,"payable_cents":9900}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey.String())
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || application.calls != 1 ||
		principalCalls != 1 || application.principalID != principalID ||
		application.orderID != orderID ||
		application.request.IdempotencyKey != idempotencyKey ||
		application.request.ExpectedOrderVersion != 3 ||
		application.request.ExpectedPayableCents != 9900 {
		t.Fatalf(
			"prepay status=%d application=%+v principal_calls=%d body=%s",
			recorder.Code,
			application,
			principalCalls,
			recorder.Body.String(),
		)
	}
	body := recorder.Body.String()
	for _, required := range []string{
		`"attempt_id":"` + attemptID.String() + `"`,
		`"order_id":"` + orderID.String() + `"`,
		`"attempt_status":"ready"`,
		`"hold_expires_at":"` + holdExpiresAt.Format(time.RFC3339Nano) + `"`,
		`"next_action":"invoke_wechat_payment"`,
		`"timeStamp":"1789701245"`,
		`"nonceStr":"safe-client-nonce"`,
		`"package":"prepay_id=client-package-value"`,
		`"signType":"RSA"`,
		`"paySign":"safe-client-signature"`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("prepay response missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{
		"private-app-id",
		"private-merchant-id",
		"private-provider-order",
		"private-provider-request",
		"private-prepay-id",
		"payment_app_id",
		"payment_merchant_id",
		"out_trade_no",
		"provider_request_id",
		"openid",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("private provider fact %q crossed HTTP boundary: %s", forbidden, body)
		}
	}
}

func TestWeChatPrepayHandlerReturnsConfirmationPendingWithoutParameters(t *testing.T) {
	t.Parallel()

	orderID := uuid.New()
	application := &fakeWeChatPrepayApplication{
		result: payment.PrepayAttemptResult{
			Attempt: payment.PaymentAttempt{
				ID:            uuid.New(),
				OrderID:       orderID,
				AttemptStatus: payment.PrepayAttemptStatusUnknown,
			},
			HoldExpiresAt: time.Date(2026, time.September, 18, 3, 14, 5, 0, time.UTC),
		},
	}
	engine := gin.New()
	NewWeChatPrepayHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	request := newValidWeChatPrepayRequest(orderID)
	engine.ServeHTTP(recorder, request)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK ||
		!strings.Contains(body, `"attempt_status":"unknown"`) ||
		!strings.Contains(body, `"next_action":"payment_confirmation_pending"`) ||
		strings.Contains(body, "payment_parameters") {
		t.Fatalf("unknown prepay status=%d body=%s", recorder.Code, body)
	}
}

func TestWeChatPrepayHandlerRejectsMalformedCommandsBeforePrincipal(t *testing.T) {
	t.Parallel()

	orderID := uuid.New()
	validKey := uuid.New().String()
	validPath := "/api/v1/xiangwan/orders/" + orderID.String() +
		"/wechat-prepay-attempts"
	validBody := `{"order_version":1,"payable_cents":9900}`
	tests := []struct {
		name        string
		path        string
		contentType string
		body        string
		headers     []string
		wantStatus  int
	}{
		{name: "query", path: validPath + "?unexpected=true", contentType: "application/json", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "content type", path: validPath, contentType: "text/plain", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "Order id", path: "/api/v1/xiangwan/orders/not-a-uuid/wechat-prepay-attempts", contentType: "application/json", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "uppercase Order id", path: "/api/v1/xiangwan/orders/" + strings.ToUpper(orderID.String()) + "/wechat-prepay-attempts", contentType: "application/json", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "missing key", path: validPath, contentType: "application/json", body: validBody, wantStatus: http.StatusBadRequest},
		{name: "multiple keys", path: validPath, contentType: "application/json", body: validBody, headers: []string{validKey, uuid.New().String()}, wantStatus: http.StatusBadRequest},
		{name: "non v4 key", path: validPath, contentType: "application/json", body: validBody, headers: []string{"00000000-0000-1000-8000-000000000001"}, wantStatus: http.StatusBadRequest},
		{name: "uppercase key", path: validPath, contentType: "application/json", body: validBody, headers: []string{strings.ToUpper(validKey)}, wantStatus: http.StatusBadRequest},
		{name: "missing version", path: validPath, contentType: "application/json", body: `{"payable_cents":9900}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "zero version", path: validPath, contentType: "application/json", body: `{"order_version":0,"payable_cents":9900}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "missing amount", path: validPath, contentType: "application/json", body: `{"order_version":1}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "zero amount", path: validPath, contentType: "application/json", body: `{"order_version":1,"payable_cents":0}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "unknown field", path: validPath, contentType: "application/json", body: `{"order_version":1,"payable_cents":9900,"merchant_id":"attacker"}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "trailing JSON", path: validPath, contentType: "application/json", body: validBody + `{}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "oversize", path: validPath, contentType: "application/json", body: `{"order_version":1,"payable_cents":9900,"padding":"` + strings.Repeat("x", maxWeChatPrepayBodyBytes) + `"}`, headers: []string{validKey}, wantStatus: http.StatusRequestEntityTooLarge},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeWeChatPrepayApplication{}
			principalCalls := 0
			engine := gin.New()
			NewWeChatPrepayHandler(
				application,
				func(*gin.Context) (uuid.UUID, error) {
					principalCalls++
					return uuid.New(), nil
				},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				test.path,
				strings.NewReader(test.body),
			)
			request.Header.Set("Content-Type", test.contentType)
			for _, value := range test.headers {
				request.Header.Add("Idempotency-Key", value)
			}
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || application.calls != 0 ||
				principalCalls != 0 {
				t.Fatalf(
					"malformed prepay status=%d calls=%d principal=%d body=%s",
					recorder.Code,
					application.calls,
					principalCalls,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestWeChatPrepayHandlerMapsSafeFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantText   string
	}{
		{name: "invalid", err: payment.ErrInvalidPrepayAttempt, wantStatus: http.StatusBadRequest, wantText: "提交内容有误，请检查后重试"},
		{name: "missing Order", err: paymentpostgres.ErrPrepayOrderNotFound, wantStatus: http.StatusNotFound, wantText: "相关内容不存在或已下线"},
		{name: "rejected prepay", err: payment.ErrPrepayRejected, wantStatus: http.StatusConflict, wantText: prepayRejectedCustomerMessage},
		{name: "disabled", err: ErrWeChatPrepayDisabled, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试"},
		{name: "missing identity", err: paymentpostgres.ErrPrepayOpenIDUnavailable, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试"},
		{name: "facts changed", err: paymentpostgres.ErrPrepayConfirmationConflict, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试"},
		{name: "idempotency", err: paymentpostgres.ErrPrepayIdempotencyConflict, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试"},
		{name: "unavailable", err: paymentpostgres.ErrPrepayUnavailable, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试"},
		{name: "transaction conflict", err: paymentpostgres.ErrPrepayTransactionConflict, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试"},
		{name: "inactive generation", err: paymentpostgres.ErrPrepayGenerationInactive, wantStatus: http.StatusServiceUnavailable, wantText: "service unavailable"},
		{name: "backend", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError, wantText: "internal server error"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeWeChatPrepayApplication{err: test.err}
			engine := gin.New()
			NewWeChatPrepayHandler(
				application,
				func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, newValidWeChatPrepayRequest(uuid.New()))
			body := recorder.Body.String()
			if recorder.Code != test.wantStatus ||
				!strings.Contains(body, test.wantText) ||
				strings.Contains(body, "database address") {
				t.Fatalf("error status=%d body=%s", recorder.Code, body)
			}
		})
	}
}

func newValidWeChatPrepayRequest(orderID uuid.UUID) *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/orders/"+orderID.String()+
			"/wechat-prepay-attempts",
		strings.NewReader(`{"order_version":1,"payable_cents":9900}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.New().String())
	return request
}

func stringPointer(value string) *string {
	return &value
}

type fakeWeChatPrepayApplication struct {
	result payment.PrepayAttemptResult
	err    error

	calls       int
	principalID uuid.UUID
	orderID     uuid.UUID
	request     WeChatPrepayRequest
}

func (application *fakeWeChatPrepayApplication) Create(
	_ context.Context,
	principalID uuid.UUID,
	orderID uuid.UUID,
	request WeChatPrepayRequest,
) (payment.PrepayAttemptResult, error) {
	application.calls++
	application.principalID = principalID
	application.orderID = orderID
	application.request = request
	return application.result, application.err
}
