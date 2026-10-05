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

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestMyBenefitsHandlerReturnsOnlySafeIdentityProjection(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(240)
	profileID := apiUUID(241)
	seriesID := apiUUID(242)
	instanceID := apiUUID(243)
	applicationID := apiUUID(244)
	now := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	requirements := "Complete an interview."
	benefitsText := "Host support and recognition."
	application := people.HostApplicationSummary{
		ID:                applicationID,
		ApplicationCycle:  "2026-q4",
		PolicyVersion:     "host-rules-v3",
		ApplicationStatus: people.HostApplicationStatusPending,
		Version:           1,
		SubmittedAt:       now.Add(-time.Hour),
		UpdatedAt:         now.Add(-time.Hour),
	}
	service := &fakeMyBenefitsApplication{result: people.MyBenefits{
		TrustedPeopleProfileID: &profileID,
		HasHostIdentity:        true,
		CurrentRoles: []people.IdentityRoleSummary{{
			SeriesID:       seriesID,
			InstanceID:     instanceID,
			RoleCode:       people.InstanceRoleHost,
			RoleStatus:     people.RoleStatusActive,
			InstanceStatus: activity.InstanceStatusPublished,
			State:          people.IdentityRoleStateCurrent,
			SeriesTitle:    "AI roundtable",
			InstanceTitle:  "September",
			GrantedAt:      now.Add(-24 * time.Hour),
		}},
		RoleHistory:            []people.IdentityRoleSummary{},
		HostRulesState:         people.HostRulesStateConfigured,
		HostRequirements:       &requirements,
		HostBenefits:           &benefitsText,
		CurrentHostApplication: &application,
		HostApplicationHistory: []people.HostApplicationSummary{application},
		HostContributionCount:  1,
		HostContributionHistory: []contribution.HistoryItem{{
			SeriesID:         seriesID,
			InstanceID:       instanceID,
			ContributionType: contribution.TypeHostCheckin,
			State:            contribution.StateActive,
			EarnedAt:         now,
		}},
		IdentityHistoryAvailable: true,
	}}
	engine := gin.New()
	NewMyBenefitsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/benefits",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET My Benefits status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                `json:"code"`
		Data MyBenefitsResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode My Benefits response: %v", err)
	}
	if envelope.Code != 0 ||
		envelope.Data.TrustedPeopleProfileID != profileID.String() ||
		!envelope.Data.HasHostIdentity || len(envelope.Data.CurrentRoles) != 1 ||
		envelope.Data.CurrentRoles[0].InstanceID != instanceID.String() ||
		envelope.Data.HostRules.State != people.HostRulesStateConfigured ||
		envelope.Data.HostRules.Requirements != requirements ||
		envelope.Data.CurrentHostApplication == nil ||
		envelope.Data.CurrentHostApplication.ApplicationID != applicationID.String() ||
		envelope.Data.HostContributionCount != 1 ||
		len(envelope.Data.HostContributionHistory) != 1 ||
		service.calls != 1 || service.principalID != principalID {
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
		"people_binding_id",
		"registration_id",
		"session_id",
		"checkin_id",
		"checkin_event_id",
		"role_binding_id",
		"entry_id",
		principalID.String(),
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestMyBenefitsHandlerMakesPendingRulesAndEmptyHistoryExplicit(t *testing.T) {
	t.Parallel()

	service := &fakeMyBenefitsApplication{result: people.MyBenefits{
		CurrentRoles:            []people.IdentityRoleSummary{},
		RoleHistory:             []people.IdentityRoleSummary{},
		HostRulesState:          people.HostRulesStatePending,
		HostApplicationHistory:  []people.HostApplicationSummary{},
		HostContributionHistory: []contribution.HistoryItem{},
	}}
	engine := gin.New()
	NewMyBenefitsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return apiUUID(245), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/benefits", nil),
	)
	for _, fragment := range []string{
		`"current_roles":[]`,
		`"role_history":[]`,
		`"host_rules":{"state":"pending"}`,
		`"can_apply_for_host":false`,
		`"host_application_history":[]`,
		`"host_contribution_history":[]`,
	} {
		if recorder.Code != http.StatusOK ||
			!strings.Contains(recorder.Body.String(), fragment) {
			t.Fatalf("pending response missing %q: %s", fragment, recorder.Body.String())
		}
	}
}

func TestMyBenefitsHandlerRejectsQueryBeforeRead(t *testing.T) {
	t.Parallel()

	service := &fakeMyBenefitsApplication{}
	engine := gin.New()
	NewMyBenefitsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return apiUUID(246), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/benefits?expand=contact",
			nil,
		),
	)
	if recorder.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
	}
}

func TestMyBenefitsHandlerMapsAuthorizationAndOpaqueFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serviceErr   error
		principalErr error
		wantStatus   int
	}{
		{name: "inactive applicant", serviceErr: peoplepostgres.ErrHostApplicationApplicantUnavailable, wantStatus: http.StatusForbidden},
		{name: "reader failure", serviceErr: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
		{name: "missing principal", principalErr: errx.NewUnauthorized("missing principal"), wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyBenefitsApplication{err: test.serviceErr}
			engine := gin.New()
			NewMyBenefitsHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) {
					return apiUUID(247), test.principalErr
				},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/benefits", nil),
			)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestMyHostApplicationsHandlerReturnsOnlySafeOwnerHistory(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(248)
	applicationID := apiUUID(249)
	now := time.Date(2026, time.September, 14, 9, 0, 0, 0, time.UTC)
	reviewComment := "Please provide another time window."
	application := people.HostApplicationSummary{
		ID:                applicationID,
		ApplicationCycle:  "2026-q4",
		PolicyVersion:     "host-rules-v3",
		ApplicationStatus: people.HostApplicationStatusRejected,
		ReviewComment:     &reviewComment,
		Version:           2,
		SubmittedAt:       now.Add(-2 * time.Hour),
		UpdatedAt:         now,
	}
	service := &fakeMyBenefitsApplication{result: people.MyBenefits{
		HasHostIdentity:        false,
		CanApplyForHost:        false,
		HostApplicationHistory: []people.HostApplicationSummary{application},
	}}
	engine := gin.New()
	NewMyBenefitsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/host-applications",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"GET Host Applications status=%d body=%s",
			recorder.Code,
			recorder.Body.String(),
		)
	}
	var envelope struct {
		Code int                        `json:"code"`
		Data MyHostApplicationsResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Host Applications response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.HasHostIdentity ||
		envelope.Data.CanApplyForHost ||
		envelope.Data.CurrentHostApplication != nil ||
		len(envelope.Data.Items) != 1 ||
		envelope.Data.Items[0].ApplicationID != applicationID.String() ||
		envelope.Data.Items[0].ReviewComment != reviewComment ||
		service.calls != 1 || service.principalID != principalID {
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
		"trusted_people_profile_id",
		"current_roles",
		"host_contribution_history",
		principalID.String(),
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf(
				"private or unrelated field %q crossed HTTP boundary: %s",
				forbidden,
				recorder.Body.String(),
			)
		}
	}
}

func TestMyHostApplicationsHandlerRejectsQueryBeforeRead(t *testing.T) {
	t.Parallel()

	service := &fakeMyBenefitsApplication{}
	engine := gin.New()
	NewMyBenefitsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return apiUUID(250), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/host-applications?include=contact",
			nil,
		),
	)
	if recorder.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf(
			"status=%d calls=%d body=%s",
			recorder.Code,
			service.calls,
			recorder.Body.String(),
		)
	}
}

func TestMyIdentityHistoryHandlerReturnsOnlyTrustedIdentityFacts(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(163)
	profileID := apiUUID(164)
	seriesID := apiUUID(165)
	instanceID := apiUUID(166)
	now := time.Date(2026, time.September, 14, 11, 0, 0, 0, time.UTC)
	service := &fakeMyBenefitsApplication{result: people.MyBenefits{
		TrustedPeopleProfileID: &profileID,
		HasHostIdentity:        true,
		CurrentRoles: []people.IdentityRoleSummary{{
			SeriesID:       seriesID,
			InstanceID:     instanceID,
			RoleCode:       people.InstanceRoleHost,
			RoleStatus:     people.RoleStatusActive,
			InstanceStatus: activity.InstanceStatusPublished,
			State:          people.IdentityRoleStateCurrent,
			SeriesTitle:    "AI roundtable",
			InstanceTitle:  "Autumn",
			GrantedAt:      now.Add(-24 * time.Hour),
		}},
		RoleHistory: []people.IdentityRoleSummary{},
		HostApplicationHistory: []people.HostApplicationSummary{{
			ID:                apiUUID(167),
			ApplicationCycle:  "private-cycle-marker",
			PolicyVersion:     "private-policy-marker",
			ApplicationStatus: people.HostApplicationStatusApproved,
			Version:           2,
			SubmittedAt:       now.Add(-48 * time.Hour),
			UpdatedAt:         now.Add(-24 * time.Hour),
		}},
		HostContributionCount: 1,
		HostContributionHistory: []contribution.HistoryItem{{
			SeriesID:         seriesID,
			InstanceID:       instanceID,
			ContributionType: contribution.TypeHostCheckin,
			State:            contribution.StateActive,
			EarnedAt:         now,
		}},
		IdentityHistoryAvailable: true,
	}}
	engine := gin.New()
	NewMyBenefitsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/identity-history",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET Identity History status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                       `json:"code"`
		Data MyIdentityHistoryResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Identity History response: %v", err)
	}
	if envelope.Code != 0 ||
		envelope.Data.TrustedPeopleProfileID != profileID.String() ||
		!envelope.Data.HasHostIdentity || len(envelope.Data.CurrentRoles) != 1 ||
		envelope.Data.CurrentRoles[0].InstanceID != instanceID.String() ||
		envelope.Data.HostContributionCount != 1 ||
		len(envelope.Data.HostContributionHistory) != 1 ||
		!envelope.Data.IdentityHistoryAvailable || service.calls != 1 ||
		service.principalID != principalID {
		t.Fatalf("response=%+v service=%+v", envelope.Data, service)
	}
	for _, forbidden := range []string{
		"host_rules",
		"can_apply_for_host",
		"current_host_application",
		"host_application_history",
		"private-cycle-marker",
		"private-policy-marker",
		"principal_id",
		"binding_id",
		"role_binding_id",
		"entry_id",
		principalID.String(),
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf(
				"private or unrelated field %q crossed HTTP boundary: %s",
				forbidden,
				recorder.Body.String(),
			)
		}
	}
}

func TestMyIdentityHistoryHandlerRejectsQueryBeforeRead(t *testing.T) {
	t.Parallel()

	service := &fakeMyBenefitsApplication{}
	engine := gin.New()
	NewMyBenefitsHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return apiUUID(168), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/identity-history?profile_id="+apiUUID(169).String(),
			nil,
		),
	)
	if recorder.Code != http.StatusBadRequest || service.calls != 0 {
		t.Fatalf(
			"status=%d calls=%d body=%s",
			recorder.Code,
			service.calls,
			recorder.Body.String(),
		)
	}
}

type fakeMyBenefitsApplication struct {
	result people.MyBenefits
	err    error

	calls       int
	principalID uuid.UUID
}

func (fake *fakeMyBenefitsApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
) (people.MyBenefits, error) {
	fake.calls++
	fake.principalID = principalID
	return fake.result, fake.err
}
