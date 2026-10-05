package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestCreateRegistrationHandlerConfirmsExactSubmission(t *testing.T) {
	t.Parallel()

	principalID := uuid.New()
	sessionID := uuid.New()
	idempotencyKey := uuid.New()
	questionnaireID := uuid.New()
	fieldID := uuid.New()
	confirmedAt := time.Date(2026, time.September, 18, 3, 4, 5, 6, time.UTC)
	application := &fakeCreateRegistrationApplication{
		created: CreateRegistrationResult{Registration: registration.Registration{
			ID:                  uuid.New(),
			SeriesID:            uuid.New(),
			InstanceID:          uuid.New(),
			SessionID:           sessionID,
			ParticipationStatus: registration.ParticipationStatusConfirmed,
			Version:             1,
			ConfirmedAt:         &confirmedAt,
		},
		},
	}
	principalCalls := 0
	engine := gin.New()
	NewCreateRegistrationHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) {
			principalCalls++
			return principalID, nil
		},
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	body := `{
		"instance_publication_version":1,
		"price_cents":0,
		"contact":{
			"name":"Wang Wei",
			"phone_e164":"+8613812345678",
			"policy_version":"contact-v1"
		},
		"questionnaire_version_id":"` + questionnaireID.String() + `",
		"answers":[{"field_id":"` + fieldID.String() + `","values":["agents"]}]
	}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/sessions/"+sessionID.String()+"/registrations",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", idempotencyKey.String())
	request.Header.Set(registrationPrivacyPolicyVersionHeader, "privacy-v1")
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("POST Registration status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if application.calls != 1 || principalCalls != 1 ||
		application.principalID != principalID ||
		application.sessionID != sessionID ||
		application.request.IdempotencyKey != idempotencyKey ||
		application.request.InstancePublicationVersion != 1 ||
		application.request.PriceCents != 0 ||
		application.request.PrivacyPolicyVersion != "privacy-v1" ||
		application.request.QuestionnaireVersionID == nil ||
		*application.request.QuestionnaireVersionID != questionnaireID ||
		len(application.request.Answers) != 1 ||
		application.request.Answers[0].FieldID != fieldID ||
		len(application.request.Answers[0].Values) != 1 ||
		application.request.Answers[0].Values[0] != "agents" {
		t.Fatalf("application = %+v", application)
	}
	responseBody := recorder.Body.String()
	for _, required := range []string{
		`"registration_id":"` + application.created.Registration.ID.String() + `"`,
		`"session_id":"` + sessionID.String() + `"`,
		`"participation_status":"confirmed"`,
		`"next_action":"registration_confirmed"`,
	} {
		if !strings.Contains(responseBody, required) {
			t.Fatalf("response missing %q: %s", required, responseBody)
		}
	}
	for _, forbidden := range []string{
		"Wang Wei",
		"+8613812345678",
		"contact-v1",
		"agents",
		"principal_id",
		"tenant_id",
		"idempotency_key",
		"questionnaire_version_id",
	} {
		if strings.Contains(responseBody, forbidden) {
			t.Fatalf("sensitive/internal value %q crossed HTTP boundary: %s", forbidden, responseBody)
		}
	}
}

func TestCreateRegistrationHandlerReturnsPendingPaidOrderWithoutMerchantFacts(
	t *testing.T,
) {
	t.Parallel()

	sessionID := uuid.New()
	createdAt := time.Date(2026, time.September, 18, 3, 4, 5, 0, time.UTC)
	registrationValue := registration.Registration{
		ID:                  uuid.New(),
		SeriesID:            uuid.New(),
		InstanceID:          uuid.New(),
		SessionID:           sessionID,
		ParticipationStatus: registration.ParticipationStatusPendingPayment,
		Version:             1,
	}
	orderID := uuid.New()
	application := &fakeCreateRegistrationApplication{
		created: CreateRegistrationResult{
			Registration: registrationValue,
			Payment: &payment.PaymentContext{
				Order: payment.Order{
					ID:                 orderID,
					PaymentStatus:      payment.OrderStatusPending,
					OriginalPriceCents: 9_900,
					PayableCents:       9_900,
					PaymentAppID:       "private-app-id",
					PaymentMerchantID:  "private-merchant-id",
					MerchantOrderNo:    "private-provider-order",
				},
				Hold: payment.CapacityHold{
					HoldStatus: payment.CapacityHoldStatusActive,
					ExpiresAt:  createdAt.Add(payment.CapacityHoldDuration),
				},
			},
		},
	}
	engine := gin.New()
	NewCreateRegistrationHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/sessions/"+sessionID.String()+"/registrations",
		strings.NewReader(`{"instance_publication_version":1,"price_cents":9900,"privacy_policy_version":"privacy-v1","contact":{"name":"Wang Wei","phone_e164":"+8613812345678","policy_version":"contact-v1"},"answers":[]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.New().String())
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST paid Registration status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, required := range []string{
		`"order_id":"` + orderID.String() + `"`,
		`"payment_status":"pending"`,
		`"original_price_cents":9900`,
		`"payable_cents":9900`,
		`"currency":"CNY"`,
		`"hold_status":"active"`,
		`"next_action":"wechat_payment_required"`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("paid response missing %q: %s", required, body)
		}
	}
	for _, forbidden := range []string{
		"private-app-id",
		"private-merchant-id",
		"private-provider-order",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("provider fact %q crossed HTTP boundary: %s", forbidden, body)
		}
	}
}

func TestCreateRegistrationHandlerRejectsMalformedCommandsBeforePrincipal(t *testing.T) {
	t.Parallel()

	sessionID := uuid.New()
	validKey := uuid.New().String()
	validBody := `{"instance_publication_version":1,"price_cents":0,"privacy_policy_version":"privacy-v1","contact":{"name":"Wang Wei","phone_e164":"+8613812345678","policy_version":"contact-v1"},"questionnaire_version_id":null,"answers":[]}`
	tests := []struct {
		name           string
		path           string
		contentType    string
		body           string
		headers        []string
		privacyHeaders []string
		wantStatus     int
	}{
		{name: "query", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations?unexpected=true", contentType: "application/json", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "content type", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "text/plain", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "session id", path: "/api/v1/xiangwan/sessions/not-a-uuid/registrations", contentType: "application/json", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "uppercase session id", path: "/api/v1/xiangwan/sessions/" + strings.ToUpper(sessionID.String()) + "/registrations", contentType: "application/json", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "missing key", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: validBody, wantStatus: http.StatusBadRequest},
		{name: "multiple keys", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: validBody, headers: []string{validKey, uuid.New().String()}, wantStatus: http.StatusBadRequest},
		{name: "non v4 key", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: validBody, headers: []string{"00000000-0000-1000-8000-000000000001"}, wantStatus: http.StatusBadRequest},
		{name: "uppercase key", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: validBody, headers: []string{strings.ToUpper(validKey)}, wantStatus: http.StatusBadRequest},
		{name: "padded key", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: validBody, headers: []string{" " + validKey}, wantStatus: http.StatusBadRequest},
		{name: "missing publication version", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: `{"price_cents":0,"contact":{},"answers":[]}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "missing price", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: `{"instance_publication_version":1,"contact":{},"answers":[]}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "padded privacy policy", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: `{"instance_publication_version":1,"price_cents":0,"privacy_policy_version":" privacy-v1","contact":{},"answers":[]}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "invalid privacy policy", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: `{"instance_publication_version":1,"price_cents":0,"privacy_policy_version":"privacy/v1","contact":{},"answers":[]}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "padded privacy header", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: validBody, headers: []string{validKey}, privacyHeaders: []string{" privacy-v1"}, wantStatus: http.StatusBadRequest},
		{name: "conflicting privacy transports", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: validBody, headers: []string{validKey}, privacyHeaders: []string{"privacy-v2"}, wantStatus: http.StatusBadRequest},
		{name: "multiple privacy headers", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: validBody, headers: []string{validKey}, privacyHeaders: []string{"privacy-v1", "privacy-v1"}, wantStatus: http.StatusBadRequest},
		{name: "negative price", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: `{"instance_publication_version":1,"price_cents":-1,"contact":{},"answers":[]}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "unknown field", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: `{"contact":{},"answers":[],"extra":true}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "trailing json", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: validBody + `{}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "invalid questionnaire id", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: `{"contact":{},"questionnaire_version_id":"not-a-uuid","answers":[]}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "missing answer values", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: `{"contact":{},"answers":[{"field_id":"` + uuid.New().String() + `"}]}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "oversize", path: "/api/v1/xiangwan/sessions/" + sessionID.String() + "/registrations", contentType: "application/json", body: `{"contact":{"name":"` + strings.Repeat("x", maxCreateRegistrationBodyBytes) + `"}}`, headers: []string{validKey}, wantStatus: http.StatusRequestEntityTooLarge},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeCreateRegistrationApplication{}
			principalCalls := 0
			engine := gin.New()
			NewCreateRegistrationHandler(
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
			for _, value := range test.privacyHeaders {
				request.Header.Add(registrationPrivacyPolicyVersionHeader, value)
			}
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || application.calls != 0 ||
				principalCalls != 0 {
				t.Fatalf(
					"malformed command status=%d calls=%d principal=%d body=%s",
					recorder.Code,
					application.calls,
					principalCalls,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestCreateRegistrationHandlerMapsSafeFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantText   string
		wantReason string
	}{
		{name: "invalid submission", err: registration.ErrInvalidRegistrationSubmission, wantStatus: http.StatusBadRequest, wantText: "提交内容有误，请检查后重试"},
		{name: "missing Session", err: activity.ErrSessionDetailUnavailable, wantStatus: http.StatusNotFound, wantText: "相关内容不存在或已下线"},
		{name: "privacy policy", err: ErrRegistrationPrivacyPolicyUnavailable, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "contact policy", err: ErrManualRegistrationContactUnavailable, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "closed Session", err: registrationpostgres.ErrRegistrationUnavailable, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "paid Session", err: registrationpostgres.ErrRegistrationPaymentRequired, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "paid feature disabled", err: ErrPaidRegistrationUnavailable, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "paid Session unavailable", err: paymentpostgres.ErrPaidRegistrationUnavailable, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "merchant config unavailable", err: paymentpostgres.ErrPaymentMerchantConfigUnavailable, wantStatus: http.StatusServiceUnavailable, wantText: "service unavailable"},
		{name: "paid questionnaire changed", err: paymentpostgres.ErrPaidRegistrationQuestionnaireConflict, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "paid idempotency conflict", err: paymentpostgres.ErrPaidRegistrationIdempotencyConflict, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "idempotency_key_conflict"},
		{name: "paid capacity conflict", err: paymentpostgres.ErrPaidRegistrationCapacityConflict, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "stale questionnaire", err: registrationpostgres.ErrRegistrationQuestionnaireConflict, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "existing Registration", err: registrationpostgres.ErrRegistrationAlreadyOpen, wantStatus: http.StatusConflict, wantText: "当前操作已完成，请刷新查看", wantReason: "registration_already_open"},
		{name: "idempotency conflict", err: registrationpostgres.ErrRegistrationIdempotencyConflict, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "idempotency_key_conflict"},
		{name: "capacity conflict", err: registrationpostgres.ErrRegistrationCapacityConflict, wantStatus: http.StatusConflict, wantText: "内容已更新，请刷新后重试", wantReason: "registration_facts_changed"},
		{name: "backend", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError, wantText: "internal server error"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeCreateRegistrationApplication{err: test.err}
			engine := gin.New()
			NewCreateRegistrationHandler(
				application,
				func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/xiangwan/sessions/"+uuid.New().String()+"/registrations",
				strings.NewReader(`{"instance_publication_version":1,"price_cents":0,"privacy_policy_version":"privacy-v1","contact":{"name":"Wang Wei","phone_e164":"+8613812345678","policy_version":"contact-v1"},"answers":[]}`),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", uuid.New().String())
			engine.ServeHTTP(recorder, request)
			body := recorder.Body.String()
			if recorder.Code != test.wantStatus ||
				!strings.Contains(body, test.wantText) ||
				(test.wantReason != "" && !strings.Contains(body, test.wantReason)) ||
				strings.Contains(body, "database address") {
				t.Fatalf("error status=%d body=%s", recorder.Code, body)
			}
		})
	}
}

type fakeCreateRegistrationApplication struct {
	created CreateRegistrationResult
	err     error

	calls       int
	principalID uuid.UUID
	sessionID   uuid.UUID
	request     CreateRegistrationRequest
}

func (application *fakeCreateRegistrationApplication) Create(
	_ context.Context,
	principalID uuid.UUID,
	sessionID uuid.UUID,
	request CreateRegistrationRequest,
) (CreateRegistrationResult, error) {
	application.calls++
	application.principalID = principalID
	application.sessionID = sessionID
	application.request = request
	return application.created, application.err
}
