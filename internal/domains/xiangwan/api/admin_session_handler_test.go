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

type sessionManagementCatalogStub struct {
	xiangwanadmin.Catalog
	update func(context.Context, xiangwanadmin.UpdateSessionCommand) (activity.Session, error)
}

func (stub sessionManagementCatalogStub) UpdateSession(
	ctx context.Context,
	command xiangwanadmin.UpdateSessionCommand,
) (activity.Session, error) {
	if stub.update == nil {
		panic("unexpected UpdateSession call")
	}
	return stub.update(ctx, command)
}

func TestUpdateSessionRejectsMalformedTimesBeforeCatalog(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := sessionManagementCatalogStub{
		update: func(context.Context, xiangwanadmin.UpdateSessionCommand) (activity.Session, error) {
			called = true
			return activity.Session{}, nil
		},
	}
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/xiangwan/admin/instances/"+apiUUID(220).String()+"/sessions/"+apiUUID(221).String(),
		strings.NewReader(`{"expected_instance_version":2,"expected_session_version":1,"title":"场次"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("UpdateSession() status = %d called = %t, want 400 without catalog", recorder.Code, called)
	}
}

func TestUpdateSessionPassesCompleteCommandAndProjectsResponse(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	instanceID, sessionID := apiUUID(222), apiUUID(223)
	operationID := uuid.New()
	registrationStart := time.Date(2026, time.October, 1, 1, 0, 0, 0, time.UTC)
	registrationEnd := registrationStart.Add(24 * time.Hour)
	sessionStart := registrationEnd.Add(24 * time.Hour)
	sessionEnd := sessionStart.Add(2 * time.Hour)
	var captured xiangwanadmin.UpdateSessionCommand
	catalog := sessionManagementCatalogStub{
		update: func(_ context.Context, command xiangwanadmin.UpdateSessionCommand) (activity.Session, error) {
			captured = command
			capacity, groupMinimum, lowStock := 30, 2, 5
			price := int64(8800)
			delivery := activity.DeliveryModeOffline
			area := activity.AreaCodeHeping
			venue, address := "五大道", "天津市和平区"
			longitude, latitude := 117.2, 39.12
			return activity.Session{
				ID: sessionID, TenantID: apiUUID(224), InstanceID: instanceID,
				Title: command.Title, Status: activity.SessionStatusDraft,
				RegistrationStartAt: &registrationStart, RegistrationEndAt: &registrationEnd,
				SessionStartAt: &sessionStart, SessionEndAt: &sessionEnd,
				Capacity: &capacity, GroupMinimum: &groupMinimum,
				LowStockThreshold: &lowStock, PriceCents: &price,
				DeliveryMode: &delivery, Area: &area, VenueName: &venue, Address: &address,
				Longitude: &longitude, Latitude: &latitude, Version: 4,
			}, nil
		},
	}
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/xiangwan/admin/instances/"+instanceID.String()+"/sessions/"+sessionID.String(),
		strings.NewReader(`{
"expected_instance_version":7,"expected_session_version":3,"title":"AI 共创场",
"registration_start_at":"2026-10-01T01:00:00Z","registration_end_at":"2026-10-02T01:00:00Z",
"session_start_at":"2026-10-03T01:00:00Z","session_end_at":"2026-10-03T03:00:00Z",
"capacity":30,"group_minimum":2,"low_stock_threshold":5,"price_cents":8800,
"delivery_mode":"offline","area":"heping","venue_name":"五大道","address":"天津市和平区",
"longitude":117.2,"latitude":39.12,"online_participation_mode":"","online_compliant":false,"sort_order":1
}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("UpdateSession() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.InstanceID != instanceID || captured.SessionID != sessionID ||
		captured.ExpectedInstanceVersion != 7 || captured.ExpectedSessionVersion != 3 ||
		captured.OperationID != operationID || captured.ActorID != principal.PrincipalID ||
		captured.IdentityLinkID != principal.IdentityLinkID || captured.RequestID == "" ||
		captured.Title != "AI 共创场" || !captured.RegistrationStartAt.Equal(registrationStart) ||
		!captured.SessionEndAt.Equal(sessionEnd) || captured.Capacity != 30 ||
		captured.GroupMinimum != 2 || captured.LowStockThreshold != 5 ||
		captured.PriceCents != 8800 || captured.DeliveryMode != activity.DeliveryModeOffline ||
		captured.Area != activity.AreaCodeHeping || captured.VenueName != "五大道" ||
		captured.Address != "天津市和平区" || captured.Longitude == nil ||
		captured.Latitude == nil || *captured.Longitude != 117.2 || *captured.Latitude != 39.12 {
		t.Fatalf("UpdateSessionCommand = %+v", captured)
	}
	if !strings.Contains(recorder.Body.String(), `"title":"AI 共创场"`) ||
		!strings.Contains(recorder.Body.String(), `"version":4`) {
		t.Fatalf("UpdateSession() response = %s", recorder.Body.String())
	}
}

func TestUpdateSessionMapsVersionConflict(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	catalog := sessionManagementCatalogStub{
		update: func(context.Context, xiangwanadmin.UpdateSessionCommand) (activity.Session, error) {
			return activity.Session{}, xiangwanadmin.ErrVersionConflict
		},
	}
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/xiangwan/admin/instances/"+apiUUID(225).String()+"/sessions/"+apiUUID(226).String(),
		strings.NewReader(`{"expected_instance_version":2,"expected_session_version":1,"title":"场次","registration_start_at":"2026-10-01T01:00:00Z","registration_end_at":"2026-10-02T01:00:00Z","session_start_at":"2026-10-03T01:00:00Z","session_end_at":"2026-10-03T03:00:00Z","capacity":30,"group_minimum":2,"low_stock_threshold":5,"price_cents":0,"delivery_mode":"online","area":"online","online_participation_mode":"腾讯会议","online_compliant":true,"sort_order":0}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("UpdateSession() status = %d body = %s, want 409", recorder.Code, recorder.Body.String())
	}
}
