package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestRegistrationCancellationHandlerReturnsSafeRefundSummary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 3, 4, 5, 0, time.UTC)
	principalID := uuid.New()
	registrationID := uuid.New()
	sessionID := uuid.New()
	orderID := uuid.New()
	actualPaid := int64(8800)
	cancelledAt := now
	application := &fakeRegistrationCancellationApplication{
		result: registrationpostgres.RegistrationCancellationResult{
			Registration: registration.Registration{
				ID:                  registrationID,
				SessionID:           sessionID,
				ParticipationStatus: registration.ParticipationStatusCancelled,
				CancellationReason:  stringPointer("user_cancelled@cancel-v3"),
				CancelledAt:         &cancelledAt,
				Version:             4,
			},
			Order: &payment.Order{
				ID:                  orderID,
				PaymentStatus:       payment.OrderStatusPaidConfirmed,
				ActualPaidCents:     &actualPaid,
				PaymentAppID:        "private-app-id",
				PaymentMerchantID:   "private-merchant-id",
				MerchantOrderNo:     "private-provider-order",
				WeChatTransactionID: stringPointer("private-transaction-id"),
				Version:             3,
			},
			Refund: &refund.Case{
				ID:                    uuid.New(),
				RefundStatus:          refund.StatusPendingManual,
				RequestedRefundCents:  actualPaid,
				SuccessfulRefundCents: 0,
				OperatorNote:          stringPointer("private finance note"),
				UpdatedAt:             now,
			},
			PolicyVersion: "cancel-v3",
		},
	}
	principalCalls := 0
	engine := gin.New()
	NewRegistrationCancellationHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) {
			principalCalls++
			return principalID, nil
		},
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		newValidRegistrationCancellationRequest(registrationID),
	)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || application.calls != 1 ||
		principalCalls != 1 || application.principalID != principalID ||
		application.registrationID != registrationID ||
		!strings.Contains(body, `"participation_status":"cancelled"`) ||
		!strings.Contains(body, `"payment_status":"paid_confirmed"`) ||
		!strings.Contains(body, `"status":"pending_manual"`) ||
		!strings.Contains(body, `"requested_refund_cents":8800`) ||
		!strings.Contains(body, `"policy_version":"cancel-v3"`) ||
		!strings.Contains(body, `"next_action":"refund_processing"`) {
		t.Fatalf("status=%d application=%+v body=%s", recorder.Code, application, body)
	}
	for _, forbidden := range []string{
		"private-app-id",
		"private-merchant-id",
		"private-provider-order",
		"private-transaction-id",
		"private finance note",
		"refund_case_id",
		"cancellation_reason",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("private fact %q crossed HTTP boundary: %s", forbidden, body)
		}
	}
}

func TestRegistrationCancellationHandlerProjectsCouponRestoration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 4, 5, 6, 0, time.UTC)
	cancelledAt := now
	policyVersion := "coupon-refund-v2"
	application := &fakeRegistrationCancellationApplication{
		result: registrationpostgres.RegistrationCancellationResult{
			Registration: registration.Registration{
				ID:                  uuid.New(),
				SessionID:           uuid.New(),
				ParticipationStatus: registration.ParticipationStatusCancelled,
				CancelledAt:         &cancelledAt,
				Version:             2,
			},
			Order: &payment.Order{
				ID:            uuid.New(),
				PaymentStatus: payment.OrderStatusSettledZero,
				Version:       1,
			},
			CouponAdjustment: &coupon.Entry{
				EntryType:           coupon.EntryTypeRestored,
				RefundPolicyVersion: &policyVersion,
				OccurredAt:          now,
			},
		},
	}
	engine := gin.New()
	NewRegistrationCancellationHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		newValidRegistrationCancellationRequest(uuid.New()),
	)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"disposition":"restore"`) ||
		!strings.Contains(recorder.Body.String(), `"policy_version":"coupon-refund-v2"`) ||
		!strings.Contains(recorder.Body.String(), `"next_action":"cancellation_completed"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRegistrationCancellationHandlerMakesFailedRefundActionable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 5, 6, 7, 0, time.UTC)
	cancelledAt := now
	application := &fakeRegistrationCancellationApplication{
		result: registrationpostgres.RegistrationCancellationResult{
			Registration: registration.Registration{
				ID:                  uuid.New(),
				SessionID:           uuid.New(),
				ParticipationStatus: registration.ParticipationStatusCancelled,
				CancelledAt:         &cancelledAt,
				Version:             2,
			},
			Order: &payment.Order{
				ID:            uuid.New(),
				PaymentStatus: payment.OrderStatusPaidConfirmed,
				Version:       1,
			},
			Refund: &refund.Case{
				RefundStatus:          refund.StatusFailed,
				RequestedRefundCents:  8800,
				SuccessfulRefundCents: 0,
				UpdatedAt:             now,
			},
		},
	}
	engine := gin.New()
	NewRegistrationCancellationHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		newValidRegistrationCancellationRequest(uuid.New()),
	)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"status":"failed"`) ||
		!strings.Contains(recorder.Body.String(), `"next_action":"contact_support"`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestRegistrationCancellationHandlerRejectsClientFactsBeforePrincipal(
	t *testing.T,
) {
	t.Parallel()

	registrationID := uuid.New()
	validPath := "/api/v1/xiangwan/registrations/" + registrationID.String() +
		"/cancellations"
	tests := []struct {
		name    string
		path    string
		body    string
		headers map[string]string
	}{
		{name: "query", path: validPath + "?reason=anything"},
		{name: "body", path: validPath, body: `{"reason":"anything"}`},
		{name: "content type", path: validPath, headers: map[string]string{"Content-Type": "text/plain"}},
		{name: "idempotency key", path: validPath, headers: map[string]string{"Idempotency-Key": uuid.New().String()}},
		{name: "invalid id", path: "/api/v1/xiangwan/registrations/not-a-uuid/cancellations"},
		{name: "uppercase id", path: "/api/v1/xiangwan/registrations/" + strings.ToUpper(registrationID.String()) + "/cancellations"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeRegistrationCancellationApplication{}
			principalCalls := 0
			engine := gin.New()
			NewRegistrationCancellationHandler(
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

func TestRegistrationCancellationHandlerMapsSafeFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "missing", err: registrationpostgres.ErrRegistrationCancellationNotFound, wantStatus: http.StatusNotFound},
		{name: "wrong owner", err: registrationpostgres.ErrRegistrationCancellationForbidden, wantStatus: http.StatusNotFound},
		{name: "policy missing", err: registrationpostgres.ErrRegistrationCancellationPolicyMissing, wantStatus: http.StatusConflict},
		{name: "coupon policy missing", err: registrationpostgres.ErrRegistrationCouponPolicyMissing, wantStatus: http.StatusConflict},
		{name: "not allowed", err: registrationpostgres.ErrRegistrationCancellationNotAllowed, wantStatus: http.StatusConflict},
		{name: "conflict", err: registrationpostgres.ErrRegistrationCancellationTransaction, wantStatus: http.StatusConflict},
		{name: "backend", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := gin.New()
			NewRegistrationCancellationHandler(
				&fakeRegistrationCancellationApplication{err: test.err},
				func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				newValidRegistrationCancellationRequest(uuid.New()),
			)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func newValidRegistrationCancellationRequest(registrationID uuid.UUID) *http.Request {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/registrations/"+registrationID.String()+"/cancellations",
		nil,
	)
	request.Header.Set("Content-Type", "application/json")
	return request
}

type fakeRegistrationCancellationApplication struct {
	result registrationpostgres.RegistrationCancellationResult
	err    error

	calls          int
	principalID    uuid.UUID
	registrationID uuid.UUID
}

func (application *fakeRegistrationCancellationApplication) Cancel(
	_ context.Context,
	principalID uuid.UUID,
	registrationID uuid.UUID,
) (registrationpostgres.RegistrationCancellationResult, error) {
	application.calls++
	application.principalID = principalID
	application.registrationID = registrationID
	return application.result, application.err
}
