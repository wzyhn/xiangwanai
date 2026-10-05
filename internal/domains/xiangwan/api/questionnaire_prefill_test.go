package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type prefillApplication struct {
	fakeSessionQuestionnaireApplication
	owner, session uuid.UUID
	calls          int
}

func (stub *prefillApplication) ReadPrefill(_ context.Context, owner, session uuid.UUID) (QuestionnairePrefillResponse, error) {
	stub.owner, stub.session = owner, session
	stub.calls++
	return QuestionnairePrefillResponse{SessionID: session.String(), Answers: []QuestionnairePrefillAnswer{}}, nil
}

func TestQuestionnairePrefillBindsOwnerAndRejectsClientIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner, session := uuid.New(), uuid.New()
	for _, tc := range []struct {
		suffix         string
		principalError error
		status         int
		calls          int
	}{
		{"", nil, http.StatusOK, 1},
		{"?principal_id=" + uuid.New().String(), nil, http.StatusBadRequest, 0},
		{"", errx.NewUnauthorized("login required"), http.StatusUnauthorized, 0},
	} {
		app := &prefillApplication{}
		router := gin.New()
		NewSessionQuestionnaireHandler(app, func(*gin.Context) (uuid.UUID, error) { return owner, tc.principalError }).RegisterRoutes(router.Group("/api/v1/xiangwan"))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/questionnaire-prefill/"+session.String()+tc.suffix, nil))
		if recorder.Code != tc.status || app.calls != tc.calls || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("status=%d calls=%d cache=%s", recorder.Code, app.calls, recorder.Header().Get("Cache-Control"))
		}
		if app.calls > 0 && (app.owner != owner || app.session != session) {
			t.Fatal("owner/session binding lost")
		}
	}
}

type prefillRepository struct {
	fakeSessionQuestionnaireReader
	answers       []activity.QuestionnaireAnswer
	owner, tenant uuid.UUID
}

func (stub *prefillRepository) ReadQuestionnairePrefill(_ context.Context, tenant, owner uuid.UUID, _ activity.SessionQuestionnaire) ([]activity.QuestionnaireAnswer, error) {
	stub.owner, stub.tenant = owner, tenant
	return stub.answers, nil
}

func TestQuestionnairePrefillValidatesCurrentFieldsAndEligibleSession(t *testing.T) {
	owner, tenant, session, instance := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	q := validAPIQuestionnaire(instance, session)
	repo := &prefillRepository{fakeSessionQuestionnaireReader: fakeSessionQuestionnaireReader{questionnaire: q}, answers: []activity.QuestionnaireAnswer{{FieldID: uuid.New(), Values: []string{"do not reuse an old field ID"}}}}
	page := &fakeQuestionnaireSessionReader{page: PublicSessionDetailPage{BrandStatus: activity.BrandLifecycleActive, Detail: activity.SessionDetail{InstanceID: instance, SessionID: session, CTA: activity.SessionDetailCTA{Action: activity.SessionDetailCTAActionStartRegistration, Enabled: true}}}}
	service, err := NewSessionQuestionnaireService(tenant, page, repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadPrefill(context.Background(), owner, session); !errors.Is(err, activity.ErrInvalidQuestionnaireAnswers) {
		t.Fatalf("stale field accepted: %v", err)
	}
	if repo.owner != owner || repo.tenant != tenant {
		t.Fatal("read lost owner/tenant")
	}
	page.page.Detail.CTA.Enabled = false
	if _, err := service.ReadPrefill(context.Background(), owner, session); !errors.Is(err, ErrQuestionnaireSessionNotSubmittable) {
		t.Fatalf("closed session accepted: %v", err)
	}
}
