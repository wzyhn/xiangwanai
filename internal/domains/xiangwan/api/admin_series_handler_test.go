package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

type seriesManagementCatalogStub struct {
	xiangwanadmin.Catalog
	updateSeries func(context.Context, xiangwanadmin.UpdateSeriesCommand) (activity.Series, error)
}

func (stub seriesManagementCatalogStub) UpdateSeries(
	ctx context.Context,
	command xiangwanadmin.UpdateSeriesCommand,
) (activity.Series, error) {
	if stub.updateSeries == nil {
		return activity.Series{}, errors.New("unexpected UpdateSeries call")
	}
	return stub.updateSeries(ctx, command)
}

func TestUpdateSeriesRejectsMalformedRequestBeforeCatalog(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	called := false
	catalog := seriesManagementCatalogStub{
		updateSeries: func(context.Context, xiangwanadmin.UpdateSeriesCommand) (activity.Series, error) {
			called = true
			return activity.Series{}, nil
		},
	}
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/xiangwan/admin/series/"+apiUUID(180).String(),
		strings.NewReader(`{"expected_version":1}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusBadRequest || called {
		t.Fatalf("UpdateSeries() status = %d called = %t, want 400 without catalog", recorder.Code, called)
	}
}

func TestUpdateSeriesPassesExactCommandAndProjectsResponse(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	seriesID := apiUUID(181)
	operationID := uuid.New()
	title := "天津 AI 圆桌派"
	homeVisible := false
	var captured xiangwanadmin.UpdateSeriesCommand
	updatedAt := time.Date(2026, time.September, 28, 8, 0, 0, 0, time.UTC)
	catalog := seriesManagementCatalogStub{
		updateSeries: func(_ context.Context, command xiangwanadmin.UpdateSeriesCommand) (activity.Series, error) {
			captured = command
			return activity.Series{
				ID: seriesID, TenantID: apiUUID(182), Title: title,
				Status: activity.SeriesStatusActive, HomeVisible: homeVisible,
				Version: 4, UpdatedAt: updatedAt,
			}, nil
		},
	}
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/xiangwan/admin/series/"+seriesID.String(),
		strings.NewReader(`{"expected_version":3,"title":"天津 AI 圆桌派","home_visible":false}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", operationID.String())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("UpdateSeries() status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	if captured.SeriesID != seriesID || captured.ExpectedVersion != 3 ||
		captured.OperationID != operationID || captured.ActorID != principal.PrincipalID ||
		captured.IdentityLinkID != principal.IdentityLinkID || captured.RequestID == "" ||
		captured.Title == nil || *captured.Title != title || captured.HomeVisible == nil || *captured.HomeVisible {
		t.Fatalf("UpdateSeriesCommand = %+v", captured)
	}
	if !strings.Contains(recorder.Body.String(), `"title":"天津 AI 圆桌派"`) ||
		!strings.Contains(recorder.Body.String(), `"home_visible":false`) {
		t.Fatalf("UpdateSeries() response = %s", recorder.Body.String())
	}
}

func TestUpdateSeriesMapsVersionConflict(t *testing.T) {
	t.Parallel()
	principal := xiangwanAdminPrincipalForTest()
	catalog := seriesManagementCatalogStub{
		updateSeries: func(context.Context, xiangwanadmin.UpdateSeriesCommand) (activity.Series, error) {
			return activity.Series{}, xiangwanadmin.ErrVersionConflict
		},
	}
	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/v1/xiangwan/admin/series/"+apiUUID(183).String(),
		strings.NewReader(`{"expected_version":2,"home_visible":true}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", uuid.NewString())
	recorder := serveAdminCatalog(catalog, &principal, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("UpdateSeries() status = %d body = %s, want 409", recorder.Code, recorder.Body.String())
	}
}
