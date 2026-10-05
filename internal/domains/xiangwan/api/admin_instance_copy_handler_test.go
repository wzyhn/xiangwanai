package xiangwanapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type instanceCopyCatalogStub struct {
	xiangwanadmin.Catalog
	create func(context.Context, xiangwanadmin.CreateInstanceCommand) (activity.Instance, error)
	get    func(context.Context, xiangwanadmin.Principal, uuid.UUID) (xiangwanadmin.InstanceDetail, error)
}

func (stub instanceCopyCatalogStub) CreateInstance(
	ctx context.Context, command xiangwanadmin.CreateInstanceCommand,
) (activity.Instance, error) {
	return stub.create(ctx, command)
}

func (stub instanceCopyCatalogStub) GetInstance(
	ctx context.Context, principal xiangwanadmin.Principal, id uuid.UUID,
) (xiangwanadmin.InstanceDetail, error) {
	return stub.get(ctx, principal, id)
}

func TestCopyInstancePassesSourceFenceAndReturnsLineage(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	seriesID, sourceID, newID := apiUUID(201), apiUUID(202), apiUUID(203)
	questionnaireID := apiUUID(205)
	operationID := uuid.New()
	var captured xiangwanadmin.CreateInstanceCommand
	catalog := instanceCopyCatalogStub{
		create: func(_ context.Context, command xiangwanadmin.CreateInstanceCommand) (activity.Instance, error) {
			captured = command
			activityType := activity.ActivityTypeCustom
			return activity.Instance{
				ID: newID, TenantID: apiUUID(204), SeriesID: seriesID,
				IssueNo: 5, Title: "第5期AI 共创", Status: activity.InstanceStatusDraft,
				ActivityType: &activityType, QuickTagCodes: []string{"workshop"},
				Version: 1, PresentationRevision: 1, UpdatedAt: time.Now().UTC(),
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/instances/"+sourceID.String()+"/copies",
		strings.NewReader(`{"series_id":"`+seriesID.String()+`","expected_series_version":4,`+
			`"expected_source_version":7,"expected_source_presentation_revision":3,`+
			`"expected_source_questionnaire_version_id":"`+questionnaireID.String()+`",`+
			`"activity_type":"custom","quick_tag_codes":["workshop"]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("CopyInstance() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.ActorID != principal.PrincipalID || captured.IdentityLinkID != principal.IdentityLinkID ||
		captured.OperationID != operationID || captured.RequestID == "" ||
		captured.SeriesID != seriesID || captured.ExpectedSeriesVersion != 4 ||
		captured.SourceInstanceID != sourceID || captured.ExpectedSourceVersion != 7 ||
		captured.ExpectedSourcePresentationRevision != 3 ||
		captured.ExpectedSourceQuestionnaireVersionID != questionnaireID ||
		captured.ActivityType != activity.ActivityTypeCustom {
		t.Fatalf("CopyInstance() command = %+v", captured)
	}
	for _, expected := range []string{
		`"id":"` + newID.String() + `"`,
		`"source_instance_id":"` + sourceID.String() + `"`,
		`"source_instance_version":7`,
		`"source_presentation_revision":3`,
		`"source_questionnaire_version_id":"` + questionnaireID.String() + `"`,
	} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("CopyInstance() response lacks %s: %s", expected, recorder.Body.String())
		}
	}
}

func TestCopyInstanceRejectsMalformedSourceBeforeWrite(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := instanceCopyCatalogStub{
		create: func(context.Context, xiangwanadmin.CreateInstanceCommand) (activity.Instance, error) {
			called = true
			return activity.Instance{}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/instances/invalid/copies", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("CopyInstance() status = %d called = %t", recorder.Code, called)
	}
}

func TestCopyInstanceRejectsMalformedQuestionnaireFenceBeforeWrite(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := instanceCopyCatalogStub{
		create: func(context.Context, xiangwanadmin.CreateInstanceCommand) (activity.Instance, error) {
			called = true
			return activity.Instance{}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost,
		"/api/v1/xiangwan/admin/instances/"+apiUUID(206).String()+"/copies",
		strings.NewReader(`{"series_id":"`+apiUUID(207).String()+`",`+
			`"expected_source_questionnaire_version_id":"invalid"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("CopyInstance() status = %d called = %t", recorder.Code, called)
	}
}

func TestGetInstanceProjectsPersistedCopyLineage(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	seriesID, sourceID, targetID := apiUUID(211), apiUUID(212), apiUUID(213)
	updatedAt := time.Now().UTC()
	catalog := instanceCopyCatalogStub{
		get: func(_ context.Context, _ xiangwanadmin.Principal, id uuid.UUID) (xiangwanadmin.InstanceDetail, error) {
			if id != targetID {
				t.Fatalf("GetInstance() id = %s", id)
			}
			return xiangwanadmin.InstanceDetail{
				Series: activity.Series{
					ID: seriesID, Status: activity.SeriesStatusActive,
					Version: 1, UpdatedAt: updatedAt,
				},
				Instance: activity.Instance{
					ID: targetID, SeriesID: seriesID, IssueNo: 2,
					Status:  activity.InstanceStatusDraft,
					Version: 1, PresentationRevision: 1, UpdatedAt: updatedAt,
				},
				CopyLineage: &xiangwanadmin.InstanceCopyLineage{
					SourceInstanceID: sourceID, SourceInstanceVersion: 7,
					SourcePresentationRevision:   3,
					SourceQuestionnaireVersionID: apiUUID(214),
				},
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/xiangwan/admin/instances/"+targetID.String(), nil)
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"copy_lineage":{"source_instance_id":"`+sourceID.String()) ||
		!strings.Contains(recorder.Body.String(), `"source_instance_version":7`) {
		t.Fatalf("GetInstance() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"source_questionnaire_version_id":"`+apiUUID(214).String()+`"`) {
		t.Fatalf("GetInstance() omitted questionnaire lineage: %s", recorder.Body.String())
	}
}
