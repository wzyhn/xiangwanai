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

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestHostApplicationSubmissionHandlerReturnsSafeBusinessState(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(158)
	principalID := apiUUID(159)
	now := time.Date(2026, time.September, 14, 10, 0, 0, 0, time.UTC)
	application, err := people.NewHostApplication(
		people.NewHostApplicationCommand{
			TenantID:             tenantID,
			PrincipalID:          principalID,
			ApplicationCycle:     "2026-q4",
			PolicyVersion:        "host-rules-v3",
			PersonalIntroduction: "private introduction marker",
			RelevantExperience:   "private experience marker",
			Availability:         "private schedule marker",
			ContactMethod:        "private contact marker",
			SubmittedAt:          now,
		},
	)
	if err != nil {
		t.Fatalf("NewHostApplication() error = %v", err)
	}
	service := &fakeHostApplicationSubmissionApplication{
		result: peoplepostgres.HostApplicationResult{
			Application: application,
			Duplicate:   true,
		},
	}
	engine := gin.New()
	NewHostApplicationSubmissionHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/me/host-applications",
		strings.NewReader(`{
			"personal_introduction":"I facilitate technical communities.",
			"relevant_experience":"Three years of community events.",
			"availability":"Weekday evenings.",
			"contact_method":"customer-approved contact"
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("POST Host Application status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                               `json:"code"`
		Data HostApplicationSubmissionResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Host Application response: %v", err)
	}
	if envelope.Code != 0 || !envelope.Data.Duplicate ||
		envelope.Data.Application.ApplicationID != application.ID.String() ||
		envelope.Data.Application.ApplicationCycle != application.ApplicationCycle ||
		envelope.Data.Application.PolicyVersion != application.PolicyVersion ||
		envelope.Data.Application.ApplicationStatus != application.ApplicationStatus ||
		service.calls != 1 || service.principalID != principalID ||
		service.request.PersonalIntroduction != "I facilitate technical communities." ||
		service.request.ContactMethod != "customer-approved contact" {
		t.Fatalf("response=%+v service=%+v", envelope.Data, service)
	}
	for _, forbidden := range []string{
		"tenant_id",
		"principal_id",
		"personal_introduction",
		"relevant_experience",
		"availability",
		"contact_method",
		"reviewed_by",
		"withdrawn_by",
		"private introduction marker",
		"private experience marker",
		"private schedule marker",
		"private contact marker",
		principalID.String(),
		tenantID.String(),
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestHostApplicationSubmissionHandlerRejectsMalformedTransport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		path        string
		body        string
		header      string
		wantStatus  int
	}{
		{
			name:       "missing content type",
			path:       "/api/v1/xiangwan/me/host-applications",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:        "unknown field",
			contentType: "application/json",
			path:        "/api/v1/xiangwan/me/host-applications",
			body:        `{"unexpected":true}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "query",
			contentType: "application/json",
			path:        "/api/v1/xiangwan/me/host-applications?cycle=other",
			body:        `{}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "operation key",
			contentType: "application/json",
			path:        "/api/v1/xiangwan/me/host-applications",
			body:        `{}`,
			header:      apiUUID(160).String(),
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "oversized",
			contentType: "application/json",
			path:        "/api/v1/xiangwan/me/host-applications",
			body: `{"personal_introduction":"` +
				strings.Repeat("x", maxHostApplicationSubmissionBodyBytes) + `"}`,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeHostApplicationSubmissionApplication{}
			engine := gin.New()
			NewHostApplicationSubmissionHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(161), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			request := httptest.NewRequest(
				http.MethodPost,
				test.path,
				strings.NewReader(test.body),
			)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.header != "" {
				request.Header.Set("Idempotency-Key", test.header)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || service.calls != 0 {
				t.Fatalf(
					"status=%d calls=%d body=%s",
					recorder.Code,
					service.calls,
					recorder.Body.String(),
				)
			}
		})
	}
}

func TestHostApplicationSubmissionHandlerMapsFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serviceErr   error
		principalErr error
		wantStatus   int
	}{
		{
			name:       "rules pending",
			serviceErr: peoplepostgres.ErrHostApplicationRulesUnavailable,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "already host",
			serviceErr: peoplepostgres.ErrHostApplicationAlreadyHost,
			wantStatus: http.StatusConflict,
		},
		{
			name:       "inactive applicant",
			serviceErr: peoplepostgres.ErrHostApplicationApplicantUnavailable,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "invalid application",
			serviceErr: peoplepostgres.ErrInvalidHostApplicationCommand,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:         "missing principal",
			principalErr: errx.NewUnauthorized("missing principal"),
			wantStatus:   http.StatusUnauthorized,
		},
		{
			name:       "storage failure",
			serviceErr: errors.New("private database address"),
			wantStatus: http.StatusInternalServerError,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeHostApplicationSubmissionApplication{err: test.serviceErr}
			engine := gin.New()
			NewHostApplicationSubmissionHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) {
					return apiUUID(162), test.principalErr
				},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/xiangwan/me/host-applications",
				strings.NewReader(`{
					"personal_introduction":"intro",
					"relevant_experience":"experience",
					"availability":"evenings",
					"contact_method":"contact"
				}`),
			)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

type fakeHostApplicationSubmissionApplication struct {
	result peoplepostgres.HostApplicationResult
	err    error

	calls       int
	principalID uuid.UUID
	request     HostApplicationSubmissionRequest
}

func (fake *fakeHostApplicationSubmissionApplication) Apply(
	_ context.Context,
	principalID uuid.UUID,
	request HostApplicationSubmissionRequest,
) (peoplepostgres.HostApplicationResult, error) {
	fake.calls++
	fake.principalID = principalID
	fake.request = request
	return fake.result, fake.err
}
