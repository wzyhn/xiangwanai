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

type questionnaireAdminCatalogStub struct {
	xiangwanadmin.Catalog
	publish func(context.Context, xiangwanadmin.PublishInstanceQuestionnaireCommand) (xiangwanadmin.InstanceQuestionnaire, error)
}

func (stub questionnaireAdminCatalogStub) PublishInstanceQuestionnaire(
	ctx context.Context,
	command xiangwanadmin.PublishInstanceQuestionnaireCommand,
) (xiangwanadmin.InstanceQuestionnaire, error) {
	return stub.publish(ctx, command)
}

func TestPublishInstanceQuestionnaireMapsOperatorFields(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(181)
	operationID := uuid.New()
	var captured xiangwanadmin.PublishInstanceQuestionnaireCommand
	catalog := questionnaireAdminCatalogStub{
		publish: func(_ context.Context, command xiangwanadmin.PublishInstanceQuestionnaireCommand) (xiangwanadmin.InstanceQuestionnaire, error) {
			captured = command
			return xiangwanadmin.InstanceQuestionnaire{
				QuestionnaireVersionID: apiUUID(182), InstanceID: instanceID, Version: 1,
				PrivacyPurpose: "用于活动报名", PrivacyPolicyVersion: "privacy-v1",
				PublishedAt: time.Date(2026, time.September, 27, 0, 0, 0, 0, time.UTC),
				Fields: []activity.QuestionnaireField{{
					FieldID: apiUUID(183), Code: "role", Type: activity.QuestionnaireFieldSingleLine,
					Label: "职业", Required: true, MaxLength: intPointer(100),
				}},
			}, nil
		},
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/questionnaire",
		strings.NewReader(`{"privacy_purpose":"用于活动报名","privacy_policy_version":"privacy-v1","fields":[{"code":"role","type":"single_line","label":"职业","help_text":"","required":true,"sort_order":0,"min_length":null,"max_length":100,"max_selections":null,"options":[]}]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("PublishInstanceQuestionnaire() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.InstanceID != instanceID || captured.OperationID != operationID ||
		captured.PrivacyPolicyVersion != "privacy-v1" || len(captured.Fields) != 1 ||
		captured.Fields[0].Code != "role" || captured.Fields[0].MaxLength == nil || *captured.Fields[0].MaxLength != 100 {
		t.Fatalf("captured questionnaire command = %+v", captured)
	}
	var envelope struct {
		Code int                                `json:"code"`
		Data AdminInstanceQuestionnaireResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(recorder.Body.String())), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != 0 || !envelope.Data.Configured || len(envelope.Data.Fields) != 1 || envelope.Data.Fields[0].Code != "role" {
		t.Fatalf("questionnaire response = %+v", envelope.Data)
	}
}

func intPointer(value int) *int { return &value }
