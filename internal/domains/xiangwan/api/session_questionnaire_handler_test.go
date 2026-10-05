package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestSessionQuestionnaireHandlerReturnsFieldConstraints(t *testing.T) {
	t.Parallel()

	principalID := uuid.New()
	sessionID := uuid.New()
	instanceID := uuid.New()
	questionnaire := validAPIQuestionnaire(instanceID, sessionID)
	maxSelections := 1
	questionnaire.Fields = append(questionnaire.Fields, activity.QuestionnaireField{
		FieldID:       uuid.New(),
		Code:          "topics",
		Type:          activity.QuestionnaireFieldMultipleChoice,
		Label:         "感兴趣的话题",
		HelpText:      "最多选择一项",
		Required:      true,
		SortOrder:     10,
		MaxSelections: &maxSelections,
		Options: []activity.QuestionnaireOption{
			{Code: "agents", Label: "智能体"},
			{Code: "product", Label: "产品"},
		},
	})
	application := &fakeSessionQuestionnaireApplication{
		questionnaire: questionnaire,
	}
	engine := gin.New()
	NewSessionQuestionnaireHandler(
		application,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/sessions/"+sessionID.String()+"/questionnaire",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET questionnaire status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                          `json:"code"`
		Data SessionQuestionnaireResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode questionnaire response: %v", err)
	}
	if envelope.Code != 0 ||
		envelope.Data.QuestionnaireVersionID !=
			questionnaire.QuestionnaireVersionID.String() ||
		envelope.Data.InstanceID != instanceID.String() ||
		envelope.Data.SessionID != sessionID.String() ||
		len(envelope.Data.Fields) != 2 ||
		envelope.Data.Fields[1].MaxSelections == nil ||
		*envelope.Data.Fields[1].MaxSelections != 1 ||
		len(envelope.Data.Fields[1].Options) != 2 {
		t.Fatalf("GET questionnaire response = %+v", envelope.Data)
	}
	if application.calls != 1 || application.principalID != principalID ||
		application.sessionID != sessionID {
		t.Fatalf("application = %+v", application)
	}
	for _, forbidden := range []string{
		"tenant_id",
		"assigned_by",
		"published_by",
		"principal_id",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("internal field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestSessionQuestionnaireHandlerRejectsInvalidInputBeforeRead(t *testing.T) {
	t.Parallel()

	for _, path := range []string{
		"/api/v1/xiangwan/sessions/not-a-uuid/questionnaire",
		"/api/v1/xiangwan/sessions/" +
			strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa") +
			"/questionnaire",
		"/api/v1/xiangwan/sessions/" + uuid.New().String() +
			"/questionnaire?unexpected=true",
	} {
		path := path
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			application := &fakeSessionQuestionnaireApplication{}
			engine := gin.New()
			NewSessionQuestionnaireHandler(
				application,
				func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					path,
					nil,
				),
			)
			if recorder.Code != http.StatusBadRequest || application.calls != 0 {
				t.Fatalf("invalid request status=%d calls=%d body=%s", recorder.Code, application.calls, recorder.Body.String())
			}
		})
	}
}

func TestSessionQuestionnaireHandlerMapsSafeFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "not configured",
			err:        activity.ErrQuestionnaireUnavailable,
			wantStatus: http.StatusOK,
			wantCode:   `"available":false`,
		},
		{
			name:       "session not visible",
			err:        activity.ErrSessionDetailUnavailable,
			wantStatus: http.StatusNotFound,
			wantCode:   `"code":10004`,
		},
		{
			name:       "not submittable",
			err:        ErrQuestionnaireSessionNotSubmittable,
			wantStatus: http.StatusConflict,
			wantCode:   `"code":10005`,
		},
		{
			name:       "backend",
			err:        errors.New("private database address"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   `"code":10006`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := gin.New()
			NewSessionQuestionnaireHandler(
				&fakeSessionQuestionnaireApplication{err: test.err},
				func(*gin.Context) (uuid.UUID, error) { return uuid.New(), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/sessions/"+uuid.New().String()+
						"/questionnaire",
					nil,
				),
			)
			body := recorder.Body.String()
			if recorder.Code != test.wantStatus ||
				!strings.Contains(body, test.wantCode) ||
				strings.Contains(body, "database address") {
				t.Fatalf("error status=%d body=%s", recorder.Code, body)
			}
		})
	}
}

type fakeSessionQuestionnaireApplication struct {
	questionnaire activity.SessionQuestionnaire
	err           error

	calls       int
	principalID uuid.UUID
	sessionID   uuid.UUID
}

func (application *fakeSessionQuestionnaireApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	sessionID uuid.UUID,
) (activity.SessionQuestionnaire, error) {
	application.calls++
	application.principalID = principalID
	application.sessionID = sessionID
	return application.questionnaire, application.err
}
