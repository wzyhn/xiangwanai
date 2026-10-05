package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights"
	datarightspostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestDataRightsHandlerSubmitsOwnerBoundRequestWithoutPrivateFacts(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(4)
	principalID := apiUUID(5)
	operationKey := uuid.New()
	submission := datarights.Submission{
		TenantID:             tenantID,
		PrincipalID:          principalID,
		OperationKey:         operationKey,
		RequestType:          datarights.RequestTypeDeletion,
		RequestScope:         datarights.RequestScopeAll,
		PrivacyPolicyVersion: "privacy-v3",
	}
	dataCase := dataRightsCaseForService(t, submission)
	application := &fakeDataRightsApplication{
		submitResult: datarightspostgres.SubmissionResult{
			Case:     dataCase,
			Replayed: true,
		},
	}
	engine := newDataRightsTestEngine(
		application,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/me/data-rights-requests",
		strings.NewReader(`{"request_type":"deletion","request_scope":"all_xiangwan_data"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationKey.String())
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || application.submitCalls != 1 ||
		application.principalID != principalID ||
		application.request.OperationKey != operationKey ||
		application.request.RequestType != datarights.RequestTypeDeletion ||
		application.request.RequestScope != datarights.RequestScopeAll {
		t.Fatalf("POST Data Rights status=%d app=%+v body=%s", recorder.Code, application, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, required := range []string{
		`"case_id":"` + dataCase.ID.String() + `"`,
		`"request_type":"deletion"`,
		`"privacy_policy_version":"privacy-v3"`,
		`"physical_deletion_promised":false`,
		`"replayed":true`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("POST Data Rights response missing %q: %s", required, body)
		}
	}
	assertNoPrivateDataRightsFacts(t, body, submission, nil)
}

func TestDataRightsHandlerListsSafeLifecycleAndDeliverySummary(t *testing.T) {
	t.Parallel()

	tenantID := apiUUID(6)
	principalID := apiUUID(7)
	submission := datarights.Submission{
		TenantID:             tenantID,
		PrincipalID:          principalID,
		OperationKey:         uuid.New(),
		RequestType:          datarights.RequestTypeExport,
		RequestScope:         datarights.RequestScopeAll,
		PrivacyPolicyVersion: "privacy-v3",
	}
	dataCase := dataRightsCaseForService(t, submission)
	dataCase.Status = datarights.CaseStatusApproved
	dataCase.Version = 4
	dataCase.UpdatedAt = dataCase.SubmittedAt.Add(3 * time.Hour)
	ownerID := principalID
	staffID := apiUUID(8)
	evidence := []byte("private-evidence-digest-32-bytes!!")
	events := []datarights.CaseEvent{
		{
			ID:                 uuid.New(),
			TenantID:           tenantID,
			CaseID:             dataCase.ID,
			CaseVersion:        1,
			EventType:          datarights.EventTypeSubmitted,
			ResultingStatus:    datarights.CaseStatusSubmitted,
			ActorPrincipalID:   &ownerID,
			PolicyBasisVersion: "privacy-v3",
			OccurredAt:         dataCase.SubmittedAt,
		},
		{
			ID:               uuid.New(),
			TenantID:         tenantID,
			CaseID:           dataCase.ID,
			CaseVersion:      2,
			EventType:        datarights.EventTypeReviewStarted,
			ResultingStatus:  datarights.CaseStatusInReview,
			ActorPrincipalID: &staffID,
			EvidenceDigest:   evidence,
			OccurredAt:       dataCase.SubmittedAt.Add(time.Hour),
		},
		{
			ID:                 uuid.New(),
			TenantID:           tenantID,
			CaseID:             dataCase.ID,
			CaseVersion:        3,
			EventType:          datarights.EventTypeApproved,
			ResultingStatus:    datarights.CaseStatusApproved,
			PolicyBasisVersion: "privacy-v3",
			OccurredAt:         dataCase.SubmittedAt.Add(2 * time.Hour),
		},
		{
			ID:              uuid.New(),
			TenantID:        tenantID,
			CaseID:          dataCase.ID,
			CaseVersion:     4,
			EventType:       datarights.EventTypeDeliverySucceeded,
			ResultingStatus: datarights.CaseStatusApproved,
			DeliveryKind:    datarights.DeliveryKindExportArchive,
			DeliveryStatus:  datarights.DeliveryStatusDelivered,
			OccurredAt:      dataCase.SubmittedAt.Add(3 * time.Hour),
		},
	}
	application := &fakeDataRightsApplication{histories: []datarights.CaseHistory{{
		Case:   dataCase,
		Events: events,
	}}}
	engine := newDataRightsTestEngine(
		application,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/data-rights-requests", nil),
	)

	body := recorder.Body.String()
	if recorder.Code != http.StatusOK || application.listCalls != 1 ||
		application.principalID != principalID ||
		!strings.Contains(body, `"event_type":"review_started"`) ||
		!strings.Contains(body, `"kind":"export_archive"`) ||
		!strings.Contains(body, `"status":"delivered"`) {
		t.Fatalf("GET Data Rights status=%d app=%+v body=%s", recorder.Code, application, body)
	}
	assertNoPrivateDataRightsFacts(t, body, submission, evidence)
}

func TestDataRightsHandlerRejectsMalformedRequestsBeforePrincipal(t *testing.T) {
	t.Parallel()

	validKey := uuid.New().String()
	validBody := `{"request_type":"access","request_scope":"profile"}`
	tests := []struct {
		name        string
		method      string
		path        string
		contentType string
		body        string
		headers     []string
		wantStatus  int
	}{
		{name: "post query", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests?x=1", contentType: "application/json", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "post content type", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "text/plain", body: validBody, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "missing key", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: validBody, wantStatus: http.StatusBadRequest},
		{name: "multiple keys", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: validBody, headers: []string{validKey, uuid.New().String()}, wantStatus: http.StatusBadRequest},
		{name: "non v4 key", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: validBody, headers: []string{"00000000-0000-1000-8000-000000000001"}, wantStatus: http.StatusBadRequest},
		{name: "uppercase key", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: validBody, headers: []string{strings.ToUpper(validKey)}, wantStatus: http.StatusBadRequest},
		{name: "missing type", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: `{"request_scope":"profile"}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "unknown type", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: `{"request_type":"erase_now","request_scope":"profile"}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "unknown scope", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: `{"request_type":"access","request_scope":"everything"}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "unknown field", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: `{"request_type":"access","request_scope":"profile","extra":true}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "trailing json", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: validBody + `{}`, headers: []string{validKey}, wantStatus: http.StatusBadRequest},
		{name: "oversize", method: http.MethodPost, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", body: `{"request_type":"access","request_scope":"` + strings.Repeat("x", maxDataRightsSubmissionBodyBytes) + `"}`, headers: []string{validKey}, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "get query", method: http.MethodGet, path: "/api/v1/xiangwan/me/data-rights-requests?x=1", wantStatus: http.StatusBadRequest},
		{name: "get body", method: http.MethodGet, path: "/api/v1/xiangwan/me/data-rights-requests", body: `{}`, wantStatus: http.StatusBadRequest},
		{name: "get content type", method: http.MethodGet, path: "/api/v1/xiangwan/me/data-rights-requests", contentType: "application/json", wantStatus: http.StatusBadRequest},
		{name: "get operation key", method: http.MethodGet, path: "/api/v1/xiangwan/me/data-rights-requests", headers: []string{validKey}, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeDataRightsApplication{}
			principalCalls := 0
			engine := newDataRightsTestEngine(
				application,
				func(*gin.Context) (uuid.UUID, error) {
					principalCalls++
					return apiUUID(9), nil
				},
			)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				test.method,
				test.path,
				strings.NewReader(test.body),
			)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			for _, value := range test.headers {
				request.Header.Add("Idempotency-Key", value)
			}
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus || application.submitCalls != 0 ||
				application.listCalls != 0 || principalCalls != 0 {
				t.Fatalf("status=%d app=%+v principal=%d body=%s", recorder.Code, application, principalCalls, recorder.Body.String())
			}
		})
	}
}

func TestDataRightsHandlerMapsSafeFailuresAndEmptyState(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "policy unavailable", err: ErrDataRightsPolicyUnavailable, wantStatus: http.StatusServiceUnavailable},
		{name: "operation conflict", err: datarightspostgres.ErrDataRightsOperationConflict, wantStatus: http.StatusConflict},
		{name: "database failure", err: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			application := &fakeDataRightsApplication{submitErr: test.err}
			engine := newDataRightsTestEngine(
				application,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(10), nil },
			)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/xiangwan/me/data-rights-requests",
				strings.NewReader(`{"request_type":"access","request_scope":"profile"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", uuid.New().String())
			engine.ServeHTTP(recorder, request)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") ||
				strings.Contains(recorder.Body.String(), test.err.Error()) {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	unauthorizedApp := &fakeDataRightsApplication{}
	unauthorizedEngine := newDataRightsTestEngine(
		unauthorizedApp,
		func(*gin.Context) (uuid.UUID, error) {
			return uuid.Nil, errx.NewUnauthorized("missing principal")
		},
	)
	unauthorizedRecorder := httptest.NewRecorder()
	unauthorizedEngine.ServeHTTP(
		unauthorizedRecorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/data-rights-requests", nil),
	)
	if unauthorizedRecorder.Code != http.StatusUnauthorized ||
		unauthorizedApp.listCalls != 0 {
		t.Fatalf("unauthorized status=%d app=%+v", unauthorizedRecorder.Code, unauthorizedApp)
	}

	emptyApp := &fakeDataRightsApplication{histories: []datarights.CaseHistory{}}
	emptyEngine := newDataRightsTestEngine(
		emptyApp,
		func(*gin.Context) (uuid.UUID, error) { return apiUUID(11), nil },
	)
	emptyRecorder := httptest.NewRecorder()
	emptyEngine.ServeHTTP(
		emptyRecorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/data-rights-requests", nil),
	)
	if emptyRecorder.Code != http.StatusOK ||
		!strings.Contains(emptyRecorder.Body.String(), `"items":[]`) ||
		!strings.Contains(emptyRecorder.Body.String(), `"empty_state":"no_data_rights_requests"`) {
		t.Fatalf("empty status=%d body=%s", emptyRecorder.Code, emptyRecorder.Body.String())
	}
}

func newDataRightsTestEngine(
	application dataRightsApplication,
	principal PrincipalResolver,
) *gin.Engine {
	engine := gin.New()
	NewDataRightsHandler(application, principal).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	return engine
}

func assertNoPrivateDataRightsFacts(
	t *testing.T,
	body string,
	submission datarights.Submission,
	evidence []byte,
) {
	t.Helper()
	for _, forbidden := range []string{
		"tenant_id",
		"principal_id",
		"operation_key",
		"request_fingerprint",
		"actor_principal_id",
		"evidence_digest",
		"delivery_destination",
		submission.OperationKey.String(),
		submission.PrincipalID.String(),
		string(evidence),
	} {
		if forbidden != "" && strings.Contains(body, forbidden) {
			t.Fatalf("private Data Rights fact %q crossed HTTP boundary: %s", forbidden, body)
		}
	}
}

type fakeDataRightsApplication struct {
	submitResult datarightspostgres.SubmissionResult
	histories    []datarights.CaseHistory
	submitErr    error
	listErr      error

	submitCalls int
	listCalls   int
	principalID uuid.UUID
	request     DataRightsSubmissionRequest
}

func (application *fakeDataRightsApplication) Submit(
	_ context.Context,
	principalID uuid.UUID,
	request DataRightsSubmissionRequest,
) (datarightspostgres.SubmissionResult, error) {
	application.submitCalls++
	application.principalID = principalID
	application.request = request
	return application.submitResult, application.submitErr
}

func (application *fakeDataRightsApplication) ListMine(
	_ context.Context,
	principalID uuid.UUID,
) ([]datarights.CaseHistory, error) {
	application.listCalls++
	application.principalID = principalID
	return application.histories, application.listErr
}
