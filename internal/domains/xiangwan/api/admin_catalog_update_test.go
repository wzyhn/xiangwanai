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
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stubAdminCatalog struct {
	xiangwanadmin.Catalog

	updateInstance func(
		context.Context,
		xiangwanadmin.UpdateInstanceCommand,
	) (activity.Instance, error)
}

func (stub stubAdminCatalog) UpdateInstance(
	ctx context.Context,
	command xiangwanadmin.UpdateInstanceCommand,
) (activity.Instance, error) {
	if stub.updateInstance == nil {
		return activity.Instance{}, errors.New("unexpected UpdateInstance call")
	}
	return stub.updateInstance(ctx, command)
}

func serveAdminCatalog(
	catalog xiangwanadmin.Catalog,
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
	NewAdminCatalogHandler(catalog).RegisterRoutes(
		engine.Group("/api/v1/xiangwan/admin"),
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func adminPatchRequest(
	t *testing.T,
	instanceID string,
	body string,
	withOperationKey bool,
) *http.Request {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/xiangwan/admin/instances/"+instanceID,
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	if withOperationKey {
		request.Header.Set("Idempotency-Key", uuid.NewString())
	}
	return request
}

func TestUpdateInstanceRequiresAdminPrincipal(t *testing.T) {
	t.Parallel()

	called := false
	catalog := stubAdminCatalog{
		updateInstance: func(context.Context, xiangwanadmin.UpdateInstanceCommand) (activity.Instance, error) {
			called = true
			return activity.Instance{}, nil
		},
	}
	recorder := serveAdminCatalog(
		catalog,
		nil,
		adminPatchRequest(
			t, apiUUID(150).String(),
			`{"expected_presentation_revision":1,"cover_image_url":""}`, true,
		),
	)
	if recorder.Code != http.StatusUnauthorized || called {
		t.Fatalf("UpdateInstance() without principal status = %d called = %t, want 401", recorder.Code, called)
	}
}

func TestUpdateInstanceRejectsMalformedRequestsBeforeCatalog(t *testing.T) {
	t.Parallel()

	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := stubAdminCatalog{
		updateInstance: func(context.Context, xiangwanadmin.UpdateInstanceCommand) (activity.Instance, error) {
			called = true
			return activity.Instance{}, nil
		},
	}
	instanceID := apiUUID(151).String()
	tests := []struct {
		name    string
		target  string
		body    string
		withKey bool
	}{
		{name: "invalid instance id", target: "not-a-uuid", body: `{"expected_presentation_revision":1,"cover_image_url":""}`, withKey: true},
		{name: "missing operation key", target: instanceID, body: `{"expected_presentation_revision":1,"cover_image_url":""}`},
		{name: "no updatable field", target: instanceID, body: `{"expected_presentation_revision":1}`, withKey: true},
		{name: "unknown field", target: instanceID, body: `{"expected_presentation_revision":1,"title":"改标题"}`, withKey: true},
		{name: "malformed json", target: instanceID, body: `{"expected_presentation_revision":`, withKey: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			called = false
			recorder := serveAdminCatalog(
				catalog,
				&principal,
				adminPatchRequest(t, test.target, test.body, test.withKey),
			)
			if recorder.Code != http.StatusBadRequest || called {
				t.Fatalf("UpdateInstance() status = %d called = %t, want 400 without catalog", recorder.Code, called)
			}
		})
	}
}

func TestUpdateInstanceMapsDomainErrors(t *testing.T) {
	t.Parallel()

	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(152).String()
	tests := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{name: "invalid detail blocks", err: xiangwanadmin.ErrInvalidCatalogRequest, wantStatus: http.StatusBadRequest},
		{name: "target missing", err: xiangwanadmin.ErrTargetNotFound, wantStatus: http.StatusNotFound},
		{name: "version conflict", err: xiangwanadmin.ErrVersionConflict, wantStatus: http.StatusConflict},
		{name: "operation conflict", err: xiangwanadmin.ErrOperationConflict, wantStatus: http.StatusConflict},
		{name: "scope forbidden", err: xiangwanadmin.ErrScopeForbidden, wantStatus: http.StatusForbidden},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			catalog := stubAdminCatalog{
				updateInstance: func(context.Context, xiangwanadmin.UpdateInstanceCommand) (activity.Instance, error) {
					return activity.Instance{}, test.err
				},
			}
			recorder := serveAdminCatalog(
				catalog,
				&principal,
				adminPatchRequest(
					t, instanceID,
					`{"expected_presentation_revision":3,"cover_image_url":"https://cdn.example.com/covers/a.jpg"}`,
					true,
				),
			)
			if recorder.Code != test.wantStatus {
				t.Fatalf("UpdateInstance() status = %d, want %d (body %s)", recorder.Code, test.wantStatus, recorder.Body.String())
			}
		})
	}
}

func TestUpdateInstancePassesExactCommandAndProjectsResponse(t *testing.T) {
	t.Parallel()

	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(153)
	now := time.Date(2026, time.September, 20, 8, 0, 0, 0, time.UTC)
	blocks := []activity.DetailBlock{
		{Type: activity.DetailBlockTypeText, Title: "活动亮点", Body: "三面环水"},
		{
			Type:    activity.DetailBlockTypeImage,
			URL:     "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.jpg",
			Caption: "现场",
		},
	}
	var captured xiangwanadmin.UpdateInstanceCommand
	catalog := stubAdminCatalog{
		updateInstance: func(_ context.Context, command xiangwanadmin.UpdateInstanceCommand) (activity.Instance, error) {
			captured = command
			return activity.Instance{
				ID:                   instanceID,
				TenantID:             apiUUID(154),
				SeriesID:             apiUUID(155),
				Title:                "九月市集",
				Status:               activity.InstanceStatusPublished,
				DetailBlocks:         blocks,
				PresentationRevision: 2,
				Version:              4,
				UpdatedAt:            now,
			}, nil
		},
	}
	operationID := uuid.New()
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String(),
		strings.NewReader(`{"expected_presentation_revision":3,"detail_blocks":[{"type":"text","title":"活动亮点","body":"三面环水"},{"type":"image","url":"/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.jpg","caption":"现场"}]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("UpdateInstance() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.InstanceID != instanceID || captured.ExpectedPresentationRevision != 3 ||
		captured.OperationID != operationID ||
		captured.ActorID != principal.PrincipalID ||
		captured.IdentityLinkID != principal.IdentityLinkID ||
		captured.RequestID == "" ||
		captured.CoverImageURL != nil || captured.DetailBlocks == nil ||
		len(*captured.DetailBlocks) != 2 {
		t.Fatalf("UpdateInstanceCommand = %+v", captured)
	}
	var envelope struct {
		Code int                   `json:"code"`
		Data AdminInstanceResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.ID != instanceID.String() ||
		envelope.Data.Version != 4 || envelope.Data.PresentationRevision != 2 ||
		len(envelope.Data.DetailBlocks) != 2 ||
		envelope.Data.DetailBlocks[1].Caption != "现场" {
		t.Fatalf("update response = %+v", envelope.Data)
	}
}
