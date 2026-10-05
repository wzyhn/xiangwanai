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
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type stubInstanceCancellationService struct {
	preview func(context.Context, xiangwanadmin.PreviewInstanceCancellationCommand) (activity.InstanceCancellationPreview, error)
	cancel  func(context.Context, xiangwanadmin.CancelInstanceCommand) (xiangwanadmin.InstanceCancellationResult, error)
}

func (stub stubInstanceCancellationService) PreviewInstanceCancellation(
	ctx context.Context,
	command xiangwanadmin.PreviewInstanceCancellationCommand,
) (activity.InstanceCancellationPreview, error) {
	if stub.preview == nil {
		return activity.InstanceCancellationPreview{}, errors.New("unexpected cancellation preview call")
	}
	return stub.preview(ctx, command)
}

func (stub stubInstanceCancellationService) CancelInstance(
	ctx context.Context,
	command xiangwanadmin.CancelInstanceCommand,
) (xiangwanadmin.InstanceCancellationResult, error) {
	if stub.cancel == nil {
		return xiangwanadmin.InstanceCancellationResult{}, errors.New("unexpected cancellation call")
	}
	return stub.cancel(ctx, command)
}

func serveAdminCancellation(
	catalog xiangwanadmin.Catalog,
	cancellation xiangwanadmin.InstanceCancellationService,
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
	NewAdminCatalogHandlerWithCancellation(catalog, cancellation).RegisterRoutes(
		engine.Group("/api/v1/xiangwan/admin"),
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func adminCancellationRequest(
	method string,
	path string,
	body string,
	operationID string,
) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if operationID != "" {
		request.Header.Set("Idempotency-Key", operationID)
	}
	return request
}

func TestAdminCancellationPreviewPassesPrincipalAndOperation(t *testing.T) {
	t.Parallel()

	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(190)
	operationID := uuid.New()
	requestID := "request-cancellation-preview"
	var captured xiangwanadmin.PreviewInstanceCancellationCommand
	service := stubInstanceCancellationService{
		preview: func(_ context.Context, command xiangwanadmin.PreviewInstanceCancellationCommand) (activity.InstanceCancellationPreview, error) {
			captured = command
			return activity.InstanceCancellationPreview{
				ID:                         apiUUID(191),
				TenantID:                   apiUUID(192),
				InstanceID:                 instanceID,
				ExpectedInstanceVersion:    7,
				SessionCount:               2,
				CancelledRegistrationCount: 3,
				RequestedRefundCents:       4200,
				CouponAdjustmentCount:      1,
				CancellationReason:         "场地调整",
				NotificationStrategy:       activity.CancellationNotificationManualRequired,
				ExpiresAt:                  time.Date(2026, 9, 27, 9, 0, 0, 0, time.FixedZone("CST", 8*60*60)),
			}, nil
		},
	}
	request := adminCancellationRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/cancellation-previews",
		`{"reason":"场地调整"}`,
		operationID.String(),
	)
	request.Header.Set("X-Request-ID", requestID)
	recorder := serveAdminCancellation(
		stubAdminCatalog{}, service, &principal, request,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("preview status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.ActorID != principal.PrincipalID ||
		captured.IdentityLinkID != principal.IdentityLinkID ||
		captured.OperationID != operationID ||
		captured.InstanceID != instanceID ||
		captured.Reason != "场地调整" ||
		captured.RequestID == "" {
		t.Fatalf("preview command = %+v", captured)
	}
	var envelope struct {
		Code int                                      `json:"code"`
		Data AdminInstanceCancellationPreviewResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.ID != apiUUID(191).String() ||
		envelope.Data.ExpectedInstanceVersion != 7 ||
		envelope.Data.RequestedRefundCents != 4200 ||
		envelope.Data.CouponAdjustmentCount != 1 {
		t.Fatalf("preview response = %+v", envelope.Data)
	}
}

func TestAdminCancellationPassesExactCommandAndProjectsReceipt(t *testing.T) {
	t.Parallel()

	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(193)
	previewID := apiUUID(194)
	operationID := uuid.New()
	now := time.Date(2026, 9, 27, 8, 30, 0, 0, time.UTC)
	var captured xiangwanadmin.CancelInstanceCommand
	service := stubInstanceCancellationService{
		cancel: func(_ context.Context, command xiangwanadmin.CancelInstanceCommand) (xiangwanadmin.InstanceCancellationResult, error) {
			captured = command
			return xiangwanadmin.InstanceCancellationResult{
				Instance: activity.Instance{
					ID: instanceID, Status: activity.InstanceStatusCancelled,
					Version: 8, UpdatedAt: now,
				},
				Receipt: activity.InstanceCancellationReceipt{
					ID:                       apiUUID(195),
					CancelledAt:              now,
					ResultingInstanceVersion: 8,
				},
			}, nil
		},
	}
	request := adminCancellationRequest(
		http.MethodPost,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/cancellation",
		`{"preview_id":"`+previewID.String()+`","expected_instance_version":7,"reason":"场地调整"}`,
		operationID.String(),
	)
	recorder := serveAdminCancellation(
		stubAdminCatalog{}, service, &principal, request,
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.ActorID != principal.PrincipalID ||
		captured.IdentityLinkID != principal.IdentityLinkID ||
		captured.OperationID != operationID ||
		captured.InstanceID != instanceID ||
		captured.PreviewID != previewID ||
		captured.ExpectedInstanceVersion != 7 ||
		captured.Reason != "场地调整" || captured.RequestID == "" {
		t.Fatalf("cancel command = %+v", captured)
	}
	var envelope struct {
		Code int                               `json:"code"`
		Data AdminInstanceCancellationResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode cancel response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.Instance.ID != instanceID.String() ||
		envelope.Data.Instance.Status != string(activity.InstanceStatusCancelled) ||
		envelope.Data.ReceiptID != apiUUID(195).String() ||
		envelope.Data.ResultingInstanceVersion != 8 ||
		envelope.Data.CancelledAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("cancel response = %+v", envelope.Data)
	}
}

func TestAdminCancellationRejectsMalformedRequestsBeforeService(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(196)
	called := false
	service := stubInstanceCancellationService{
		preview: func(context.Context, xiangwanadmin.PreviewInstanceCancellationCommand) (activity.InstanceCancellationPreview, error) {
			called = true
			return activity.InstanceCancellationPreview{}, nil
		},
		cancel: func(context.Context, xiangwanadmin.CancelInstanceCommand) (xiangwanadmin.InstanceCancellationResult, error) {
			called = true
			return xiangwanadmin.InstanceCancellationResult{}, nil
		},
	}
	tests := []struct {
		name string
		path string
		body string
		key  string
		want int
	}{
		{name: "missing principal", path: "/api/v1/xiangwan/admin/instances/" + instanceID.String() + "/cancellation-previews", body: `{"reason":"x"}`, key: uuid.NewString(), want: http.StatusUnauthorized},
		{name: "missing operation key", path: "/api/v1/xiangwan/admin/instances/" + instanceID.String() + "/cancellation-previews", body: `{"reason":"x"}`, want: http.StatusBadRequest},
		{name: "invalid instance id", path: "/api/v1/xiangwan/admin/instances/not-a-uuid/cancellation-previews", body: `{"reason":"x"}`, key: uuid.NewString(), want: http.StatusBadRequest},
		{name: "unknown preview field", path: "/api/v1/xiangwan/admin/instances/" + instanceID.String() + "/cancellation-previews", body: `{"reason":"x","unexpected":true}`, key: uuid.NewString(), want: http.StatusBadRequest},
		{name: "invalid preview id", path: "/api/v1/xiangwan/admin/instances/" + instanceID.String() + "/cancellation", body: `{"preview_id":"not-a-uuid","expected_instance_version":1,"reason":"x"}`, key: uuid.NewString(), want: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called = false
			var actor *xiangwanadmin.Principal = &principal
			if test.name == "missing principal" {
				actor = nil
			}
			recorder := serveAdminCancellation(
				stubAdminCatalog{}, service, actor,
				adminCancellationRequest(http.MethodPost, test.path, test.body, test.key),
			)
			if recorder.Code != test.want || called {
				t.Fatalf("status = %d called = %t body = %s, want %d and no call", recorder.Code, called, recorder.Body.String(), test.want)
			}
		})
	}
}

func TestAdminCancellationMapsCouponPolicyAndStateErrors(t *testing.T) {
	principal := xiangwanAdminPrincipalForTest()
	instanceID := apiUUID(197)
	request := func() *http.Request {
		return adminCancellationRequest(
			http.MethodPost,
			"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/cancellation",
			`{"preview_id":"`+apiUUID(198).String()+`","expected_instance_version":1,"reason":"x"}`,
			uuid.NewString(),
		)
	}
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "coupon policy unavailable", err: activitypostgres.ErrSessionCancellationCouponPolicy, want: http.StatusConflict},
		{name: "preview conflict", err: activitypostgres.ErrInstanceCancellationPreviewConflict, want: http.StatusConflict},
		{name: "target missing", err: activitypostgres.ErrInstanceCancellationNotFound, want: http.StatusNotFound},
		{name: "transaction failure", err: activitypostgres.ErrInstanceCancellationTransaction, want: http.StatusInternalServerError},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := stubInstanceCancellationService{
				cancel: func(context.Context, xiangwanadmin.CancelInstanceCommand) (xiangwanadmin.InstanceCancellationResult, error) {
					return xiangwanadmin.InstanceCancellationResult{}, test.err
				},
			}
			recorder := serveAdminCancellation(
				stubAdminCatalog{}, service, &principal, request(),
			)
			if recorder.Code != test.want {
				t.Fatalf("status = %d body = %s, want %d", recorder.Code, recorder.Body.String(), test.want)
			}
		})
	}
}
