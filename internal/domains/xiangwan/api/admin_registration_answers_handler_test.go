package xiangwanapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type registrationAnswerCatalog struct {
	xiangwanadmin.Catalog
	read func(context.Context, xiangwanadmin.Principal, xiangwanadmin.RegistrationAnswerSummaryFilter) (xiangwanadmin.RegistrationAnswerSummaryPage, error)
}

func (catalog registrationAnswerCatalog) ListRegistrationAnswerSummaries(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	filter xiangwanadmin.RegistrationAnswerSummaryFilter,
) (xiangwanadmin.RegistrationAnswerSummaryPage, error) {
	return catalog.read(ctx, principal, filter)
}

func TestListRegistrationAnswerSummariesJSONNeverProjectsRawValues(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(211)
	registrationID := apiUUID(212)
	called := false
	catalog := registrationAnswerCatalog{read: func(
		_ context.Context,
		actor xiangwanadmin.Principal,
		filter xiangwanadmin.RegistrationAnswerSummaryFilter,
	) (xiangwanadmin.RegistrationAnswerSummaryPage, error) {
		called = true
		if actor.PrincipalID != principal.PrincipalID || filter.InstanceID == nil ||
			*filter.InstanceID != instanceID || filter.Page != 2 || filter.PageSize != 10 ||
			filter.Format != "json" {
			t.Fatalf("unexpected answer summary filter: %+v", filter)
		}
		return xiangwanadmin.RegistrationAnswerSummaryPage{
			Items: []xiangwanadmin.RegistrationAnswerSummary{{
				RegistrationID: registrationID, InstanceID: instanceID,
				SessionID: apiUUID(213), QuestionnaireVersionID: apiUUID(214),
				FieldID: apiUUID(215), FieldCode: "interest", FieldType: "single_line",
				FieldLabel: "兴趣", Required: true, SortOrder: 0,
				Answered: true, ValueCount: 1, CreatedAt: time.Date(2026, 9, 28, 1, 2, 3, 0, time.UTC),
			}},
			Page: 2, PageSize: 10, Total: 11,
		}, nil
	}}
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/registrations/answer-summaries?instance_id="+
			instanceID.String()+"&page=2&page_size=10", nil)
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusOK || !called {
		t.Fatalf("status = %d, called = %t; want 200, true", recorder.Code, called)
	}
	body := recorder.Body.String()
	if strings.Contains(body, "answer_values") || strings.Contains(body, "raw secret") {
		t.Fatalf("raw answer value appeared in summary response: %s", body)
	}
	var envelope struct {
		Data struct {
			Items []struct {
				Answered   bool `json:"answered"`
				ValueCount int  `json:"value_count"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Items) != 1 || !envelope.Data.Items[0].Answered || envelope.Data.Items[0].ValueCount != 1 {
		t.Fatalf("summary projection = %+v", envelope.Data.Items)
	}
}

func TestListRegistrationAnswerSummariesCSVIsMetadataOnly(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	sessionID := apiUUID(216)
	catalog := registrationAnswerCatalog{read: func(
		_ context.Context,
		_ xiangwanadmin.Principal,
		filter xiangwanadmin.RegistrationAnswerSummaryFilter,
	) (xiangwanadmin.RegistrationAnswerSummaryPage, error) {
		if filter.SessionID == nil || *filter.SessionID != sessionID || filter.Format != "csv" {
			t.Fatalf("unexpected CSV filter: %+v", filter)
		}
		return xiangwanadmin.RegistrationAnswerSummaryPage{
			Items: []xiangwanadmin.RegistrationAnswerSummary{{
				RegistrationID: apiUUID(217), InstanceID: apiUUID(218), SessionID: sessionID,
				QuestionnaireVersionID: apiUUID(219), FieldID: apiUUID(220),
				FieldCode: "area", FieldType: "area", FieldLabel: "区域",
				ValueCount: 0, CreatedAt: time.Unix(0, 0),
			}},
			Page: 1, PageSize: 20, Total: 1,
		}, nil
	}}
	recorder := serveAdminCatalog(catalog, &principal, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/xiangwan/admin/registrations/answer-summaries?session_id="+sessionID.String()+"&format=csv",
		nil,
	))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("CSV response status/content-type = %d/%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "registration_id,instance_id,session_id") || strings.Contains(body, "answer_values") {
		t.Fatalf("unexpected CSV body: %s", body)
	}
}

func TestListRegistrationAnswerSummariesRequiresFilter(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := registrationAnswerCatalog{read: func(
		context.Context,
		xiangwanadmin.Principal,
		xiangwanadmin.RegistrationAnswerSummaryFilter,
	) (xiangwanadmin.RegistrationAnswerSummaryPage, error) {
		called = true
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, nil
	}}
	recorder := serveAdminCatalog(catalog, &principal, httptest.NewRequest(
		http.MethodGet, "/api/v1/xiangwan/admin/registrations/answer-summaries", nil,
	))
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("unfiltered answer summary status/called = %d/%t, want 400/false", recorder.Code, called)
	}
}

type registrationAnswerDetailCatalog struct {
	xiangwanadmin.Catalog
	read func(context.Context, xiangwanadmin.Principal, string, string) (xiangwanadmin.RegistrationAnswerDetailSet, error)
}

func (catalog registrationAnswerDetailCatalog) GetRegistrationAnswers(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	registrationID uuid.UUID,
	purpose string,
) (xiangwanadmin.RegistrationAnswerDetailSet, error) {
	return catalog.read(ctx, principal, registrationID.String(), purpose)
}

func TestGetRegistrationAnswersRequiresExactIDAndPurpose(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := registrationAnswerDetailCatalog{read: func(
		context.Context, xiangwanadmin.Principal, string, string,
	) (xiangwanadmin.RegistrationAnswerDetailSet, error) {
		called = true
		return xiangwanadmin.RegistrationAnswerDetailSet{}, nil
	}}
	for _, path := range []string{
		"/api/v1/xiangwan/admin/registrations/not-a-uuid/answers?purpose=activity_coordination",
		"/api/v1/xiangwan/admin/registrations/" + apiUUID(221).String() + "/answers",
		"/api/v1/xiangwan/admin/registrations/" + apiUUID(221).String() + "/answers?purpose=other",
	} {
		result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet, path, nil))
		if result.Code != http.StatusBadRequest || called {
			t.Fatalf("path %q status/called = %d/%t, want 400/false", path, result.Code, called)
		}
	}
}

func TestGetRegistrationAnswersProjectsSingleRecordWithoutContact(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	registrationID := apiUUID(222)
	catalog := registrationAnswerDetailCatalog{read: func(
		_ context.Context, actor xiangwanadmin.Principal, exactID, purpose string,
	) (xiangwanadmin.RegistrationAnswerDetailSet, error) {
		if actor.PrincipalID != principal.PrincipalID || exactID != registrationID.String() || purpose != "event_followup" {
			t.Fatalf("unexpected answer detail access: actor=%s id=%s purpose=%s", actor.PrincipalID, exactID, purpose)
		}
		return xiangwanadmin.RegistrationAnswerDetailSet{
			RegistrationID: registrationID, InstanceID: apiUUID(223), SessionID: apiUUID(224),
			Items: []xiangwanadmin.RegistrationAnswerDetail{{
				QuestionnaireVersionID: apiUUID(225), FieldID: apiUUID(226),
				FieldCode: "interest", FieldType: "single_choice", FieldLabel: "兴趣",
				Options: []activity.QuestionnaireOption{{Code: "walking", Label: "散步"}},
				Values:  []string{"walking"}, CreatedAt: time.Date(2026, 9, 29, 1, 2, 3, 0, time.UTC),
			}},
		}, nil
	}}
	result := serveAdminCatalog(catalog, &principal, httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/registrations/"+registrationID.String()+"/answers?purpose=event_followup", nil))
	if result.Code != http.StatusOK || result.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("answer detail status/cache = %d/%q", result.Code, result.Header().Get("Cache-Control"))
	}
	if strings.Contains(result.Body.String(), "contact") || !strings.Contains(result.Body.String(), `"answer_values":["walking"]`) {
		t.Fatalf("unexpected single-record answer response: %s", result.Body.String())
	}
}
