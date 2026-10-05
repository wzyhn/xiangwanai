package xiangwanapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/gin-gonic/gin"
)

func TestWeChatPaymentNotificationHandlerAcknowledgesOnlyAfterProcessing(t *testing.T) {
	t.Parallel()

	decoder := &fakePaymentNotificationDecoder{
		notification: validAPIPaymentNotification(fixedNotificationHandlerTime()),
	}
	application := &fakePaymentNotificationApplication{}
	router := paymentNotificationTestRouter(decoder, application)
	request := validPaymentNotificationHTTPRequest(`{"signed":"ciphertext"}`)
	responseRecorder := httptest.NewRecorder()
	router.ServeHTTP(responseRecorder, request)
	if responseRecorder.Code != http.StatusNoContent ||
		responseRecorder.Body.Len() != 0 || decoder.calls != 1 ||
		decoder.rawBody != `{"signed":"ciphertext"}` || application.calls != 1 ||
		application.notification.NotificationID != decoder.notification.NotificationID {
		t.Fatalf(
			"Receive status=%d body=%s decoder=%+v application=%+v",
			responseRecorder.Code,
			responseRecorder.Body.String(),
			decoder,
			application,
		)
	}
}

func TestWeChatPaymentNotificationHandlerRejectsUntrustedRequestShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mutate     func(*http.Request)
		wantStatus int
	}{
		{name: "query", mutate: func(request *http.Request) { request.URL.RawQuery = "tenant=other" }, wantStatus: http.StatusBadRequest},
		{name: "wrong content type", mutate: func(request *http.Request) { request.Header.Set("Content-Type", "text/plain") }, wantStatus: http.StatusBadRequest},
		{name: "idempotency key", mutate: func(request *http.Request) { request.Header.Set("Idempotency-Key", "client-key") }, wantStatus: http.StatusBadRequest},
		{name: "missing signature", mutate: func(request *http.Request) { request.Header.Del("Wechatpay-Signature") }, wantStatus: http.StatusBadRequest},
		{name: "duplicate serial", mutate: func(request *http.Request) { request.Header.Add("Wechatpay-Serial", "PUB_KEY_ID_SECOND") }, wantStatus: http.StatusBadRequest},
		{name: "unsupported signature type", mutate: func(request *http.Request) { request.Header.Set("Wechatpay-Signature-Type", "OTHER") }, wantStatus: http.StatusBadRequest},
		{name: "oversized body", mutate: func(request *http.Request) {
			request.Body = io.NopCloser(strings.NewReader(strings.Repeat("x", maxWeChatPaymentNotificationBodyBytes+1)))
			request.ContentLength = int64(maxWeChatPaymentNotificationBodyBytes + 1)
		}, wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			decoder := &fakePaymentNotificationDecoder{}
			application := &fakePaymentNotificationApplication{}
			router := paymentNotificationTestRouter(decoder, application)
			request := validPaymentNotificationHTTPRequest(`{"signed":"ciphertext"}`)
			test.mutate(request)
			responseRecorder := httptest.NewRecorder()
			router.ServeHTTP(responseRecorder, request)
			if responseRecorder.Code != test.wantStatus || decoder.calls != 0 ||
				application.calls != 0 ||
				!strings.Contains(responseRecorder.Body.String(), `"code":"FAIL"`) {
				t.Fatalf(
					"%s status=%d body=%s decoder=%d application=%d",
					test.name,
					responseRecorder.Code,
					responseRecorder.Body.String(),
					decoder.calls,
					application.calls,
				)
			}
		})
	}
}

func TestWeChatPaymentNotificationHandlerUsesProviderRetrySemantics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		decoderErr error
		serviceErr error
		wantStatus int
		forbidden  string
	}{
		{name: "verification failure", decoderErr: errors.New("private signature detail"), wantStatus: http.StatusBadRequest, forbidden: "signature"},
		{name: "notification conflict", serviceErr: paymentpostgres.ErrPaymentConfirmationConflict, wantStatus: http.StatusConflict},
		{name: "temporary database failure", serviceErr: errors.New("private database detail"), wantStatus: http.StatusInternalServerError, forbidden: "database"},
		{name: "disabled", serviceErr: ErrWeChatPaymentNotificationDisabled, wantStatus: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			decoder := &fakePaymentNotificationDecoder{
				notification: validAPIPaymentNotification(fixedNotificationHandlerTime()),
				err:          test.decoderErr,
			}
			application := &fakePaymentNotificationApplication{err: test.serviceErr}
			router := paymentNotificationTestRouter(decoder, application)
			responseRecorder := httptest.NewRecorder()
			router.ServeHTTP(
				responseRecorder,
				validPaymentNotificationHTTPRequest(`{"signed":"ciphertext"}`),
			)
			if responseRecorder.Code != test.wantStatus ||
				!strings.Contains(responseRecorder.Body.String(), `"code":"FAIL"`) ||
				(test.forbidden != "" && strings.Contains(responseRecorder.Body.String(), test.forbidden)) {
				t.Fatalf("%s status=%d body=%s", test.name, responseRecorder.Code, responseRecorder.Body.String())
			}
		})
	}
}

func paymentNotificationTestRouter(
	decoder weChatPaymentNotificationDecoder,
	application weChatPaymentNotificationApplication,
) *gin.Engine {
	router := gin.New()
	handler := NewWeChatPaymentNotificationHandler(decoder, application)
	handler.RegisterRoutes(router.Group("/api/v1/xiangwan"))
	return router
}

func validPaymentNotificationHTTPRequest(body string) *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/integrations/wechat-pay/notifications",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Wechatpay-Serial", "PUB_KEY_ID_SYNTHETIC_TEST")
	request.Header.Set("Wechatpay-Signature", "synthetic-signature")
	request.Header.Set("Wechatpay-Timestamp", "1789344000")
	request.Header.Set("Wechatpay-Nonce", "synthetic-nonce")
	return request
}

type fakePaymentNotificationDecoder struct {
	notification payment.VerifiedPaymentNotification
	err          error
	calls        int
	rawBody      string
}

func (fake *fakePaymentNotificationDecoder) Decode(
	_ context.Context,
	request *http.Request,
) (payment.VerifiedPaymentNotification, error) {
	fake.calls++
	body, _ := io.ReadAll(request.Body)
	fake.rawBody = string(body)
	return fake.notification, fake.err
}

type fakePaymentNotificationApplication struct {
	err          error
	calls        int
	notification payment.VerifiedPaymentNotification
}

func (fake *fakePaymentNotificationApplication) Process(
	_ context.Context,
	notification payment.VerifiedPaymentNotification,
) (payment.PaymentConvergence, error) {
	fake.calls++
	fake.notification = notification
	return payment.PaymentConvergence{}, fake.err
}

func fixedNotificationHandlerTime() time.Time {
	return time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
}
