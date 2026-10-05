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

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestCheckinCredentialHandlerReturnsOnlyOneTimeConsumerSecrets(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(250)
	principalID := apiUUID(251)
	registrationID := apiUUID(252)
	issued := knownIssuedCheckinCredential(
		t,
		tenantID,
		principalID,
		registrationID,
		10*time.Minute,
	)
	service := &fakeCheckinCredentialApplication{issued: issued}
	engine := gin.New()
	NewCheckinCredentialHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/registrations/"+registrationID.String()+
			"/checkin-credentials",
		nil,
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST Checkin credential status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                       `json:"code"`
		Data CheckinCredentialResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Checkin credential response: %v", err)
	}
	if envelope.Code != 0 ||
		envelope.Data.RegistrationID != registrationID.String() ||
		envelope.Data.CredentialJTI != issued.Credential.CredentialJTI.String() ||
		envelope.Data.QRToken != issued.QRToken ||
		envelope.Data.BackupCode != issued.BackupCode ||
		service.calls != 1 || service.principalID != principalID ||
		service.registrationID != registrationID {
		t.Fatalf("response=%+v service=%+v", envelope.Data, service)
	}
	for _, forbidden := range []string{
		"tenant_id",
		"principal_id",
		"credential_id",
		"qr_token_hash",
		"backup_code_hash",
		"revocation_reason",
		tenantID.String(),
		principalID.String(),
		issued.Credential.ID.String(),
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestCheckinCredentialHandlerRejectsAmbiguousIssueRequests(t *testing.T) {
	t.Parallel()

	registrationID := apiUUID(253)
	tests := []struct {
		name    string
		path    string
		body    string
		headers map[string]string
	}{
		{name: "query", path: "/api/v1/xiangwan/registrations/" + registrationID.String() + "/checkin-credentials?ttl=900"},
		{name: "body", path: "/api/v1/xiangwan/registrations/" + registrationID.String() + "/checkin-credentials", body: `{}`},
		{name: "idempotency key", path: "/api/v1/xiangwan/registrations/" + registrationID.String() + "/checkin-credentials", headers: map[string]string{"Idempotency-Key": apiUUID(254).String()}},
		{name: "invalid id", path: "/api/v1/xiangwan/registrations/not-a-uuid/checkin-credentials"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeCheckinCredentialApplication{}
			engine := gin.New()
			NewCheckinCredentialHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(255), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			for name, value := range test.headers {
				request.Header.Set(name, value)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
		})
	}
}

func TestCheckinCredentialHandlerMapsStableErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serviceErr   error
		principalErr error
		wantStatus   int
	}{
		{name: "unavailable", serviceErr: checkinpostgres.ErrRegistrationCredentialUnavailable, wantStatus: http.StatusConflict},
		{name: "rate limited", serviceErr: checkinpostgres.ErrRegistrationCredentialRateLimited, wantStatus: http.StatusTooManyRequests},
		{name: "generation inactive", serviceErr: checkinpostgres.ErrRegistrationCredentialGenerationInactive, wantStatus: http.StatusServiceUnavailable},
		{name: "changed", serviceErr: checkinpostgres.ErrRegistrationCredentialTransactionConflict, wantStatus: http.StatusConflict},
		{name: "database failure", serviceErr: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
		{name: "missing principal", principalErr: errx.NewUnauthorized("missing principal"), wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeCheckinCredentialApplication{err: test.serviceErr}
			engine := gin.New()
			NewCheckinCredentialHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) {
					return apiUUID(1), test.principalErr
				},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodPost,
					"/api/v1/xiangwan/registrations/"+apiUUID(2).String()+
						"/checkin-credentials",
					nil,
				),
			)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if test.serviceErr == checkinpostgres.ErrRegistrationCredentialRateLimited &&
				recorder.Header().Get("Retry-After") != "5" {
				t.Fatalf("Retry-After=%q", recorder.Header().Get("Retry-After"))
			}
		})
	}
}

type fakeCheckinCredentialApplication struct {
	issued checkin.IssuedCredential
	err    error

	calls          int
	principalID    uuid.UUID
	registrationID uuid.UUID
}

func (fake *fakeCheckinCredentialApplication) Issue(
	_ context.Context,
	principalID uuid.UUID,
	registrationID uuid.UUID,
) (checkin.IssuedCredential, error) {
	fake.calls++
	fake.principalID = principalID
	fake.registrationID = registrationID
	return fake.issued, fake.err
}
