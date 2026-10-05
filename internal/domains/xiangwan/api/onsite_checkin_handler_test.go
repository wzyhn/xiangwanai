package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestOnsiteCheckinHandlerReturnsSafeVerificationReceipt(t *testing.T) {
	t.Parallel()

	operatorID := apiUUID(80)
	request := knownOnsiteVerificationRequest()
	attempt := knownOnsiteVerificationAttempt(
		request,
		apiUUID(81),
		operatorID,
		checkin.VerificationDecisionValid,
	)
	service := &fakeOnsiteCheckinApplication{
		verification: checkinpostgres.VerifyCredentialResult{Attempt: attempt},
	}
	engine := gin.New()
	NewOnsiteCheckinHandler(
		service,
		fakeOnsiteAdminPrincipalResolver(
			func(*gin.Context) (xiangwanadmin.Principal, error) {
				return testAdminPrincipal(operatorID), nil
			},
		),
	).RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	body := fmt.Sprintf(
		`{"series_id":%q,"instance_id":%q,"session_id":%q,"presented_kind":"qr_token","presented_value":%q}`,
		request.SeriesID,
		request.InstanceID,
		request.SessionID,
		request.PresentedValue,
	)
	httpRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/checkin-verifications",
		strings.NewReader(body),
	)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey.String())
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST verification status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                               `json:"code"`
		Data OnsiteCheckinVerificationResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode verification response: %v", err)
	}
	if envelope.Code != 0 ||
		envelope.Data.Decision != checkin.VerificationDecisionValid ||
		!envelope.Data.CanRecord ||
		envelope.Data.VerificationAttemptID != attempt.ID.String() ||
		envelope.Data.RegistrationID != attempt.RegistrationID.String() ||
		envelope.Data.CredentialID != attempt.CredentialID.String() ||
		service.verifyCalls != 1 || service.operator.PrincipalID != operatorID ||
		service.verificationRequest.PresentedValue != request.PresentedValue {
		t.Fatalf("response=%+v service=%+v", envelope.Data, service)
	}
	for _, forbidden := range []string{
		request.PresentedValue,
		"presented_value",
		"principal_id",
		"credential_jti",
		operatorID.String(),
		attempt.PrincipalID.String(),
		attempt.CredentialJTI.String(),
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private value %q crossed verification response: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestOnsiteCheckinHandlerHidesMatchedIdentityForRejectedCredential(t *testing.T) {
	t.Parallel()

	operatorID := apiUUID(82)
	request := knownOnsiteVerificationRequest()
	attempt := knownOnsiteVerificationAttempt(
		request,
		apiUUID(83),
		operatorID,
		checkin.VerificationDecisionWrongContext,
	)
	service := &fakeOnsiteCheckinApplication{
		verification: checkinpostgres.VerifyCredentialResult{Attempt: attempt},
	}
	engine := gin.New()
	NewOnsiteCheckinHandler(
		service,
		fakeOnsiteAdminPrincipalResolver(
			func(*gin.Context) (xiangwanadmin.Principal, error) {
				return testAdminPrincipal(operatorID), nil
			},
		),
	).RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	body := fmt.Sprintf(
		`{"series_id":%q,"instance_id":%q,"session_id":%q,"presented_kind":"qr_token","presented_value":%q}`,
		request.SeriesID,
		request.InstanceID,
		request.SessionID,
		request.PresentedValue,
	)
	httpRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/checkin-verifications",
		strings.NewReader(body),
	)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey.String())
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusOK ||
		strings.Contains(recorder.Body.String(), attempt.RegistrationID.String()) ||
		strings.Contains(recorder.Body.String(), attempt.CredentialID.String()) ||
		strings.Contains(recorder.Body.String(), attempt.PrincipalID.String()) {
		t.Fatalf("rejected verification leaked identity: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestOnsiteCheckinHandlerReturnsRecordedFactWithoutParticipant(t *testing.T) {
	t.Parallel()

	operatorID := apiUUID(84)
	request := knownOnsiteRecordRequest()
	value, err := checkin.New(checkin.NewCommand{
		TenantID:       apiUUID(85),
		RegistrationID: request.RegistrationID,
		SeriesID:       request.SeriesID,
		InstanceID:     request.InstanceID,
		SessionID:      request.SessionID,
		PrincipalID:    apiUUID(86),
		CheckedInBy:    operatorID,
		At:             time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("checkin.New() error = %v", err)
	}
	service := &fakeOnsiteCheckinApplication{
		recorded: checkinpostgres.RecordCheckinResult{Checkin: value},
	}
	engine := gin.New()
	NewOnsiteCheckinHandler(
		service,
		fakeOnsiteAdminPrincipalResolver(
			func(*gin.Context) (xiangwanadmin.Principal, error) {
				return testAdminPrincipal(operatorID), nil
			},
		),
	).RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	body := fmt.Sprintf(
		`{"series_id":%q,"instance_id":%q,"session_id":%q,"credential_id":%q,"verification_attempt_id":%q}`,
		request.SeriesID,
		request.InstanceID,
		request.SessionID,
		request.CredentialID,
		request.VerificationAttemptID,
	)
	httpRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/registrations/"+
			request.RegistrationID.String()+"/checkins",
		strings.NewReader(body),
	)
	httpRequest.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST Checkin status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                         `json:"code"`
		Data OnsiteCheckinRecordResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Checkin response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.CheckinID != value.ID.String() ||
		envelope.Data.RegistrationID != request.RegistrationID.String() ||
		envelope.Data.Status != checkin.StatusCheckedIn ||
		service.recordCalls != 1 || service.operator.PrincipalID != operatorID ||
		service.recordRequest != request {
		t.Fatalf("response=%+v service=%+v", envelope.Data, service)
	}
	for _, forbidden := range []string{
		"principal_id",
		"checked_in_by",
		value.PrincipalID.String(),
		operatorID.String(),
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private value %q crossed Checkin response: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestOnsiteCheckinHandlerRejectsMalformedCommandsBeforePrincipal(t *testing.T) {
	t.Parallel()

	validID := apiUUID(87).String()
	validKey := uuid.New().String()
	tests := []struct {
		name        string
		path        string
		body        string
		contentType string
		operation   string
	}{
		{name: "query", path: "/api/v1/xiangwan/admin/checkin-verifications?mode=scan", body: `{}`, contentType: "application/json", operation: validKey},
		{name: "content type", path: "/api/v1/xiangwan/admin/checkin-verifications", body: `{}`, contentType: "text/plain", operation: validKey},
		{name: "missing operation", path: "/api/v1/xiangwan/admin/checkin-verifications", body: `{}`, contentType: "application/json"},
		{name: "unknown field", path: "/api/v1/xiangwan/admin/checkin-verifications", body: fmt.Sprintf(`{"series_id":%q,"instance_id":%q,"session_id":%q,"presented_kind":"qr_token","presented_value":"token","registration_id":%q}`, validID, validID, validID, validID), contentType: "application/json", operation: validKey},
		{name: "invalid kind", path: "/api/v1/xiangwan/admin/checkin-verifications", body: fmt.Sprintf(`{"series_id":%q,"instance_id":%q,"session_id":%q,"presented_kind":"raw_phone","presented_value":"token"}`, validID, validID, validID), contentType: "application/json", operation: validKey},
		{name: "invalid registration path", path: "/api/v1/xiangwan/admin/registrations/not-a-uuid/checkins", body: `{}`, contentType: "application/json"},
		{name: "record client operation key", path: "/api/v1/xiangwan/admin/registrations/" + validID + "/checkins", body: `{}`, contentType: "application/json", operation: validKey},
		{name: "record unknown field", path: "/api/v1/xiangwan/admin/registrations/" + validID + "/checkins", body: fmt.Sprintf(`{"series_id":%q,"instance_id":%q,"session_id":%q,"credential_id":%q,"verification_attempt_id":%q,"principal_id":%q}`, validID, validID, validID, validID, validID, validID), contentType: "application/json"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeOnsiteCheckinApplication{}
			principalCalls := 0
			engine := gin.New()
			NewOnsiteCheckinHandler(
				service,
				fakeOnsiteAdminPrincipalResolver(
					func(*gin.Context) (xiangwanadmin.Principal, error) {
						principalCalls++
						return testAdminPrincipal(apiUUID(88)), nil
					},
				),
			).RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
			request := httptest.NewRequest(
				http.MethodPost,
				test.path,
				strings.NewReader(test.body),
			)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.operation != "" {
				request.Header.Set("Idempotency-Key", test.operation)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest ||
				service.verifyCalls != 0 || service.recordCalls != 0 ||
				principalCalls != 0 {
				t.Fatalf("status=%d service=%+v principal_calls=%d body=%s", recorder.Code, service, principalCalls, recorder.Body.String())
			}
		})
	}
}

func TestOnsiteCheckinHandlerRejectsOversizedBody(t *testing.T) {
	t.Parallel()

	service := &fakeOnsiteCheckinApplication{}
	principalCalls := 0
	engine := gin.New()
	NewOnsiteCheckinHandler(
		service,
		fakeOnsiteAdminPrincipalResolver(
			func(*gin.Context) (xiangwanadmin.Principal, error) {
				principalCalls++
				return testAdminPrincipal(apiUUID(90)), nil
			},
		),
	).RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	httpRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/checkin-verifications",
		strings.NewReader(`{"series_id":"`+strings.Repeat("x", maxOnsiteCheckinBodyBytes)+`"}`),
	)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Idempotency-Key", uuid.New().String())
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusRequestEntityTooLarge ||
		service.verifyCalls != 0 || principalCalls != 0 {
		t.Fatalf("status=%d service=%+v principal_calls=%d body=%s", recorder.Code, service, principalCalls, recorder.Body.String())
	}
}

func TestOnsiteCheckinHandlerMapsSafeFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serviceErr   error
		principalErr error
		wantStatus   int
	}{
		{name: "unauthenticated", principalErr: errx.NewUnauthorized("admin session required"), wantStatus: http.StatusUnauthorized},
		{name: "forbidden", serviceErr: checkinpostgres.ErrCheckinOperatorForbidden, wantStatus: http.StatusForbidden},
		{name: "target missing", serviceErr: checkinpostgres.ErrCheckinVerificationTargetNotFound, wantStatus: http.StatusNotFound},
		{name: "retry conflict", serviceErr: checkinpostgres.ErrCheckinVerificationTransactionConflict, wantStatus: http.StatusConflict},
		{name: "authorization unavailable", serviceErr: checkinpostgres.ErrCheckinAuthorizationUnavailable, wantStatus: http.StatusServiceUnavailable},
		{name: "generation inactive", serviceErr: checkinpostgres.ErrCheckinVerificationGenerationInactive, wantStatus: http.StatusServiceUnavailable},
		{name: "private database failure", serviceErr: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := knownOnsiteVerificationRequest()
			service := &fakeOnsiteCheckinApplication{err: test.serviceErr}
			engine := gin.New()
			NewOnsiteCheckinHandler(
				service,
				fakeOnsiteAdminPrincipalResolver(
					func(*gin.Context) (xiangwanadmin.Principal, error) {
						return testAdminPrincipal(apiUUID(89)), test.principalErr
					},
				),
			).RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
			body := fmt.Sprintf(
				`{"series_id":%q,"instance_id":%q,"session_id":%q,"presented_kind":"qr_token","presented_value":"token"}`,
				request.SeriesID,
				request.InstanceID,
				request.SessionID,
			)
			httpRequest := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/xiangwan/admin/checkin-verifications",
				strings.NewReader(body),
			)
			httpRequest.Header.Set("Content-Type", "application/json")
			httpRequest.Header.Set("Idempotency-Key", request.IdempotencyKey.String())
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httpRequest)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

type fakeOnsiteCheckinApplication struct {
	verification checkinpostgres.VerifyCredentialResult
	recorded     checkinpostgres.RecordCheckinResult
	err          error

	verifyCalls         int
	recordCalls         int
	operator            xiangwanadmin.Principal
	verificationRequest OnsiteCheckinVerificationRequest
	recordRequest       OnsiteCheckinRecordRequest
}

type fakeOnsiteAdminPrincipalResolver func(*gin.Context) (xiangwanadmin.Principal, error)

func (resolver fakeOnsiteAdminPrincipalResolver) ResolveOnsiteAdminPrincipal(
	c *gin.Context,
) (xiangwanadmin.Principal, error) {
	return resolver(c)
}

func (fake *fakeOnsiteCheckinApplication) Verify(
	_ context.Context,
	operator xiangwanadmin.Principal,
	request OnsiteCheckinVerificationRequest,
) (checkinpostgres.VerifyCredentialResult, error) {
	fake.verifyCalls++
	fake.operator = operator
	fake.verificationRequest = request
	return fake.verification, fake.err
}

func (fake *fakeOnsiteCheckinApplication) Record(
	_ context.Context,
	operator xiangwanadmin.Principal,
	request OnsiteCheckinRecordRequest,
) (checkinpostgres.RecordCheckinResult, error) {
	fake.recordCalls++
	fake.operator = operator
	fake.recordRequest = request
	return fake.recorded, fake.err
}

func testAdminPrincipal(principalID uuid.UUID) xiangwanadmin.Principal {
	return xiangwanadmin.Principal{
		PrincipalID:    principalID,
		IdentityLinkID: uuid.New(),
	}
}
