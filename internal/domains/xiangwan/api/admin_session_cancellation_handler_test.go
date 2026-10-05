package xiangwanapi

import (
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type stubSessionCancellationService struct {
	preview func(context.Context, xiangwanadmin.PreviewSessionCancellationCommand) (activity.SessionCancellationPreview, error)
	cancel  func(context.Context, xiangwanadmin.CancelSessionCommand) (xiangwanadmin.SessionCancellationResult, error)
}

func (s stubSessionCancellationService) PreviewSessionCancellation(ctx context.Context, c xiangwanadmin.PreviewSessionCancellationCommand) (activity.SessionCancellationPreview, error) {
	return s.preview(ctx, c)
}
func (s stubSessionCancellationService) CancelSession(ctx context.Context, c xiangwanadmin.CancelSessionCommand) (xiangwanadmin.SessionCancellationResult, error) {
	return s.cancel(ctx, c)
}

func serveSessionCancellation(principal *xiangwanadmin.Principal, service stubSessionCancellationService, request *http.Request) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if principal != nil {
		engine.Use(func(c *gin.Context) { c.Set(adminPrincipalContextKey, *principal) })
	}
	handler := NewAdminCatalogHandler(stubAdminCatalog{})
	handler.sessionCancellation = service
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestAdminSessionCancellationUsesTrustedActorAndExactSession(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	sessionID, previewID, operationID := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	calls := 0
	service := stubSessionCancellationService{
		preview: func(_ context.Context, c xiangwanadmin.PreviewSessionCancellationCommand) (activity.SessionCancellationPreview, error) {
			calls++
			if c.ActorID != principal.PrincipalID || c.IdentityLinkID != principal.IdentityLinkID || c.OperationID != operationID || c.SessionID != sessionID || c.RequestID == "" {
				t.Fatalf("command %+v", c)
			}
			return activity.SessionCancellationPreview{ID: previewID, SessionID: sessionID, ExpectedSessionVersion: 5, ExpiresAt: now.Add(time.Minute)}, nil
		},
		cancel: func(_ context.Context, c xiangwanadmin.CancelSessionCommand) (xiangwanadmin.SessionCancellationResult, error) {
			calls++
			if c.ActorID != principal.PrincipalID || c.IdentityLinkID != principal.IdentityLinkID || c.SessionID != sessionID || c.PreviewID != previewID || c.ExpectedSessionVersion != 5 || c.Reason != "场地调整" {
				t.Fatalf("command %+v", c)
			}
			return xiangwanadmin.SessionCancellationResult{Session: activity.Session{ID: sessionID, Status: activity.SessionStatusCancelled}, Receipt: activity.SessionCancellationReceipt{ID: uuid.New(), CancelledAt: now}}, nil
		},
	}
	for _, item := range []struct{ path, body string }{{"cancellation-previews", "{}"}, {"cancellation", `{"preview_id":"` + previewID.String() + `","expected_session_version":5,"reason":"场地调整"}`}} {
		r := serveSessionCancellation(&principal, service, adminCancellationRequest("POST", "/api/v1/xiangwan/admin/sessions/"+sessionID.String()+"/"+item.path, item.body, operationID.String()))
		if r.Code != 200 {
			t.Fatalf("status %d %s", r.Code, r.Body.String())
		}
	}
	if calls != 2 {
		t.Fatalf("calls %d", calls)
	}
}

func TestAdminSessionCancellationRejectsIdentityInjectionAndMapsConflicts(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	path := "/api/v1/xiangwan/admin/sessions/" + uuid.NewString() + "/cancellation-previews"
	service := stubSessionCancellationService{preview: func(context.Context, xiangwanadmin.PreviewSessionCancellationCommand) (activity.SessionCancellationPreview, error) {
		return activity.SessionCancellationPreview{}, activitypostgres.ErrSessionCancellationPreviewConflict
	}}
	for _, item := range []struct {
		principal *xiangwanadmin.Principal
		body      string
		want      int
	}{{nil, "{}", 401}, {&principal, `{"principal_id":"foreign"}`, 400}, {&principal, "{}", 409}} {
		r := serveSessionCancellation(item.principal, service, adminCancellationRequest("POST", path, item.body, uuid.NewString()))
		if r.Code != item.want {
			t.Fatalf("status %d wanted %d: %s", r.Code, item.want, r.Body.String())
		}
	}
	service.preview = func(context.Context, xiangwanadmin.PreviewSessionCancellationCommand) (activity.SessionCancellationPreview, error) {
		return activity.SessionCancellationPreview{}, errors.New("database failed")
	}
	r := serveSessionCancellation(&principal, service, adminCancellationRequest("POST", path, "{}", uuid.NewString()))
	if r.Code != 500 {
		t.Fatal(r.Code)
	}
}
