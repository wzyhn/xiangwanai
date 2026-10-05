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

type questionnaireTemplateCatalogStub struct {
	xiangwanadmin.Catalog
	list   func(context.Context, xiangwanadmin.Principal, int, int) (xiangwanadmin.QuestionnaireTemplatePage, error)
	create func(context.Context, xiangwanadmin.CreateQuestionnaireTemplateCommand) (xiangwanadmin.QuestionnaireTemplate, error)
}

func (stub questionnaireTemplateCatalogStub) ListQuestionnaireTemplates(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	page int,
	pageSize int,
) (xiangwanadmin.QuestionnaireTemplatePage, error) {
	return stub.list(ctx, principal, page, pageSize)
}

func (stub questionnaireTemplateCatalogStub) GetQuestionnaireTemplate(context.Context, xiangwanadmin.Principal, uuid.UUID) (xiangwanadmin.QuestionnaireTemplate, error) {
	panic("unexpected GetQuestionnaireTemplate call")
}
func (stub questionnaireTemplateCatalogStub) CreateQuestionnaireTemplate(ctx context.Context, command xiangwanadmin.CreateQuestionnaireTemplateCommand) (xiangwanadmin.QuestionnaireTemplate, error) {
	if stub.create == nil {
		panic("unexpected CreateQuestionnaireTemplate call")
	}
	return stub.create(ctx, command)
}
func (stub questionnaireTemplateCatalogStub) UpdateQuestionnaireTemplate(context.Context, xiangwanadmin.UpdateQuestionnaireTemplateCommand) (xiangwanadmin.QuestionnaireTemplate, error) {
	panic("unexpected UpdateQuestionnaireTemplate call")
}
func (stub questionnaireTemplateCatalogStub) ArchiveQuestionnaireTemplate(context.Context, xiangwanadmin.ArchiveQuestionnaireTemplateCommand) (xiangwanadmin.QuestionnaireTemplate, error) {
	panic("unexpected ArchiveQuestionnaireTemplate call")
}
func (stub questionnaireTemplateCatalogStub) ApplyQuestionnaireTemplate(context.Context, xiangwanadmin.ApplyQuestionnaireTemplateCommand) (xiangwanadmin.InstanceQuestionnaire, error) {
	panic("unexpected ApplyQuestionnaireTemplate call")
}

func TestListQuestionnaireTemplatesProjectsVersionedFields(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	templateID, versionID, fieldID := apiUUID(201), apiUUID(202), apiUUID(203)
	catalog := questionnaireTemplateCatalogStub{
		list: func(_ context.Context, got xiangwanadmin.Principal, page, pageSize int) (xiangwanadmin.QuestionnaireTemplatePage, error) {
			if got.PrincipalID != principal.PrincipalID || page != 0 || pageSize != 0 {
				t.Fatalf("list arguments = principal %v page %d size %d", got.PrincipalID, page, pageSize)
			}
			return xiangwanadmin.QuestionnaireTemplatePage{
				Items: []xiangwanadmin.QuestionnaireTemplate{{
					ID: templateID, VersionID: versionID, Name: "活动报名",
					Description: "AI 活动通用模板", Version: 2, Status: "active",
					PrivacyPurpose: "用于活动报名", PrivacyPolicyVersion: "privacy-v1",
					CreatedAt: time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC),
					UpdatedAt: time.Date(2026, time.September, 28, 1, 0, 0, 0, time.UTC),
					Fields:    []activity.QuestionnaireField{{FieldID: fieldID, Code: "role", Type: activity.QuestionnaireFieldSingleLine, Label: "你的角色", Required: true, MaxLength: intPointer(80)}},
				}},
				Page: 1, PageSize: 20, Total: 1,
			}, nil
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/admin/questionnaire-templates", nil)
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("ListQuestionnaireTemplates() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                                    `json:"code"`
		Data AdminQuestionnaireTemplatePageResponse `json:"data"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(recorder.Body.String())), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.Total != 1 || len(envelope.Data.Items) != 1 || envelope.Data.Items[0].Version != 2 || envelope.Data.Items[0].Fields[0].FieldID != fieldID.String() {
		t.Fatalf("template page response = %+v", envelope.Data)
	}
}

func TestCreateQuestionnaireTemplateMapsFieldContract(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	operationID := uuid.New()
	var captured xiangwanadmin.CreateQuestionnaireTemplateCommand
	catalog := questionnaireTemplateCatalogStub{
		create: func(_ context.Context, command xiangwanadmin.CreateQuestionnaireTemplateCommand) (xiangwanadmin.QuestionnaireTemplate, error) {
			captured = command
			return xiangwanadmin.QuestionnaireTemplate{ID: apiUUID(211), VersionID: apiUUID(212), Name: command.Name, Version: 1, Status: "active", Fields: command.Fields}, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/xiangwan/admin/questionnaire-templates", strings.NewReader(`{"name":"活动报名","description":"通用模板","privacy_purpose":"用于活动报名","privacy_policy_version":"privacy-v1","fields":[{"code":"role","type":"single_choice","label":"你的角色","help_text":"","required":true,"sort_order":0,"min_length":null,"max_length":null,"max_selections":null,"options":[{"code":"builder","label":"共创者"}]}]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("CreateQuestionnaireTemplate() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.OperationID != operationID || captured.Name != "活动报名" || len(captured.Fields) != 1 || captured.Fields[0].Options[0].Code != "builder" {
		t.Fatalf("captured template command = %+v", captured)
	}
}
