package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stubReviewResourceWriter struct {
	create func(context.Context, resource.CreateReviewResourceCommand) (resource.ReviewResourceReceipt, error)
}

func (stub stubReviewResourceWriter) CreateReviewResource(
	ctx context.Context,
	command resource.CreateReviewResourceCommand,
) (resource.ReviewResourceReceipt, error) {
	if stub.create == nil {
		return resource.ReviewResourceReceipt{}, errors.New("unexpected review writer call")
	}
	return stub.create(ctx, command)
}

func serveAdminReviewWriter(
	catalog stubAdminCatalog,
	writer resource.ReviewResourceWriter,
	principal *xiangwanadmin.Principal,
	request *http.Request,
) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	if principal != nil {
		engine.Use(func(c *gin.Context) {
			c.Set(adminPrincipalContextKey, *principal)
		})
	}
	handler := NewAdminCatalogHandler(catalog)
	handler.SetReviewResourceWriter(writer)
	handler.RegisterRoutes(engine.Group("/api/v1/xiangwan/admin"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestCreateReviewResourcePassesExactAdminCommand(t *testing.T) {
	t.Parallel()

	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(230)
	sessionID := apiUUID(231)
	operationID := uuid.New()
	fileID := uuid.New()
	var captured resource.CreateReviewResourceCommand
	receipt := resource.ReviewResourceReceipt{
		RelationID: uuid.New(), PublicationID: uuid.New(), ContentID: uuid.New(),
		TenantID: uuid.New(), InstanceID: instanceID, SessionID: &sessionID,
		Title: "本期视频回顾",
	}
	writer := stubReviewResourceWriter{create: func(_ context.Context, command resource.CreateReviewResourceCommand) (resource.ReviewResourceReceipt, error) {
		captured = command
		return receipt, nil
	}}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/review-resources",
		strings.NewReader(`{"expected_target_version":7,"session_id":"`+sessionID.String()+`","title":"本期视频回顾","description":"活动摘要","video_url":"https://video.example.com/channel/1","photos":["https://media.example.com/photo.webp"],"files":[{"file_id":"`+fileID.String()+`","kind":"material","sha256":"`+strings.Repeat("a", 64)+`","reviewed":true}],"recording":{"enabled":true,"title":"讨论纪要","subtitle":"核心观点","url":"https://feishu.example.com/recording"},"materials":{"enabled":true,"title":"资料合集","subtitle":"现场分享","url":"https://feishu.example.com/materials"},"sort_order":4}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	request.Header.Set("X-Request-ID", "review-writer-request")
	recorder := serveAdminReviewWriter(stubAdminCatalog{}, writer, &principal, request)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if captured.ActorID != principal.PrincipalID ||
		captured.IdentityLinkID != principal.IdentityLinkID || captured.InstanceID != instanceID ||
		captured.SessionID == nil || *captured.SessionID != sessionID ||
		captured.ExpectedTargetVersion != 7 || captured.Title != "本期视频回顾" ||
		captured.Description != "活动摘要" || captured.VideoURL != "https://video.example.com/channel/1" ||
		len(captured.Photos) != 1 || captured.Photos[0] != "https://media.example.com/photo.webp" ||
		len(captured.Files) != 1 || captured.Files[0].FileID != fileID ||
		captured.Files[0].Kind != resource.ReviewResourceFileMaterial ||
		captured.Files[0].SHA256 != strings.Repeat("a", 64) ||
		!captured.Files[0].Reviewed ||
		len(captured.Links) != 2 || captured.Links[0].Kind != resource.ReviewResourceLinkRecording ||
		captured.Links[0].Title != "讨论纪要" || captured.Links[0].Subtitle != "核心观点" ||
		captured.Links[0].URL != "https://feishu.example.com/recording" ||
		captured.Links[1].Kind != resource.ReviewResourceLinkMaterials ||
		captured.SortOrder != 4 || captured.OperationID != operationID || captured.RequestID != "review-writer-request" {
		t.Fatalf("command=%+v", captured)
	}
	if !strings.Contains(recorder.Body.String(), receipt.RelationID.String()) ||
		!strings.Contains(recorder.Body.String(), receipt.PublicationID.String()) {
		t.Fatalf("receipt not projected: %s", recorder.Body.String())
	}
}

func TestCreateReviewResourceRequiresAdminAndMapsExternalLinkRejection(t *testing.T) {
	t.Parallel()

	instanceID := apiUUID(232)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/review-resources",
		strings.NewReader(`{"expected_target_version":1,"title":"回顾","video_url":"https://video.example.com/1"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	called := false
	writer := stubReviewResourceWriter{create: func(context.Context, resource.CreateReviewResourceCommand) (resource.ReviewResourceReceipt, error) {
		called = true
		return resource.ReviewResourceReceipt{}, resource.ErrReviewResourceExternalLink
	}}
	withoutPrincipal := serveAdminReviewWriter(stubAdminCatalog{}, writer, nil, request)
	if withoutPrincipal.Code != http.StatusUnauthorized || called {
		t.Fatalf("without principal status=%d called=%t", withoutPrincipal.Code, called)
	}
	principal := xiangwanAdminPrincipalForTest()
	withPrincipal := serveAdminReviewWriter(stubAdminCatalog{}, writer, &principal, request)
	if withPrincipal.Code != http.StatusBadRequest || !called {
		t.Fatalf("external link status=%d called=%t body=%s", withPrincipal.Code, called, withPrincipal.Body.String())
	}
}

func TestCreateReviewResourceMapsWriterAuthorizationRejection(t *testing.T) {
	t.Parallel()

	instanceID := apiUUID(233)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/review-resources",
		strings.NewReader(`{"expected_target_version":1,"title":"回顾","video_url":"https://video.example.com/1"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	writer := stubReviewResourceWriter{create: func(context.Context, resource.CreateReviewResourceCommand) (resource.ReviewResourceReceipt, error) {
		return resource.ReviewResourceReceipt{}, xiangwanadmin.ErrScopeForbidden
	}}
	principal := xiangwanAdminPrincipalForTest()
	recorder := serveAdminReviewWriter(stubAdminCatalog{}, writer, &principal, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("authorization rejection status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
