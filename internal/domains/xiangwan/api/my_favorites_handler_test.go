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
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestMyFavoritesHandlerReturnsOnlyConsumerSafeSeriesFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 25, 10, 11, 12, 0, time.UTC)
	principalID := apiUUID(241)
	seriesID := apiUUID(242)
	archivedSeriesID := apiUUID(243)
	service := &fakeMyFavoritesApplication{page: activity.MyFavoritesPage{
		AsOf:       now,
		NextCursor: "next-owner-cursor",
		Items: []activity.MyFavoriteItem{
			{
				SeriesID:          seriesID,
				Title:             "AI Roundtable",
				SeriesStatus:      activity.SeriesStatusActive,
				FavoriteCount:     8,
				FavoriteAvatars:   []string{"/avatars/one.png", "/avatars/two.png"},
				FavoritedAt:       now.Add(-time.Hour),
				SessionsAvailable: true,
			},
			{
				SeriesID:      archivedSeriesID,
				Title:         "Past Salon",
				SeriesStatus:  activity.SeriesStatusArchived,
				FavoriteCount: 3,
				FavoritedAt:   now.Add(-2 * time.Hour),
			},
		},
	}}
	engine := gin.New()
	NewMyFavoritesHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/me/favorites?cursor=opaque&limit=10",
			nil,
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET My Favorites status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                 `json:"code"`
		Data MyFavoritesResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode My Favorites response: %v", err)
	}
	wantSessionsPath := "/api/v1/xiangwan/series/" + seriesID.String() + "/sessions"
	if envelope.Code != 0 || len(envelope.Data.Items) != 2 ||
		envelope.Data.Items[0].SeriesID != seriesID.String() ||
		envelope.Data.Items[0].SessionsPath != wantSessionsPath ||
		len(envelope.Data.Items[0].FavoriteAvatars) != 2 ||
		!envelope.Data.Items[0].SessionsAvailable ||
		envelope.Data.Items[1].SessionsPath != "" ||
		envelope.Data.NextCursor != "next-owner-cursor" {
		t.Fatalf("GET My Favorites response = %+v", envelope.Data)
	}
	if service.calls != 1 || service.principalID != principalID ||
		service.request.Cursor != "opaque" || service.request.Limit != 10 {
		t.Fatalf("service = %+v", service)
	}
	for _, forbidden := range []string{
		"favorite_id",
		"tenant_id",
		"principal_id",
		"current_public_instance_id",
		"owner_scope",
	} {
		if strings.Contains(recorder.Body.String(), forbidden) {
			t.Fatalf("private/internal field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
		}
	}
}

func TestMyFavoritesHandlerRejectsAmbiguousQuery(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{
		"?unknown=value",
		"?cursor=one&cursor=two",
		"?cursor=",
		"?cursor=%20opaque",
		"?limit=0",
		"?limit=01",
		"?limit=101",
	} {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyFavoritesApplication{}
			engine := gin.New()
			NewMyFavoritesHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(244), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/me/favorites"+suffix,
					nil,
				),
			)
			if recorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
		})
	}
}

func TestMyFavoritesHandlerMapsStableErrorsAndEmptyState(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name         string
		serviceErr   error
		principalErr error
		wantStatus   int
	}{
		{name: "invalid cursor", serviceErr: activitypostgres.ErrInvalidMyFavoritesCursor, wantStatus: http.StatusBadRequest},
		{name: "stale cursor", serviceErr: activitypostgres.ErrStaleMyFavoritesCursor, wantStatus: http.StatusConflict},
		{name: "reader failure", serviceErr: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
		{name: "missing principal", principalErr: errx.NewUnauthorized("missing principal"), wantStatus: http.StatusUnauthorized},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeMyFavoritesApplication{err: test.serviceErr}
			engine := gin.New()
			NewMyFavoritesHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) {
					return apiUUID(245), test.principalErr
				},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/favorites", nil),
			)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}

	service := &fakeMyFavoritesApplication{page: activity.MyFavoritesPage{
		Items: []activity.MyFavoriteItem{},
		AsOf:  time.Now().UTC(),
	}}
	engine := gin.New()
	NewMyFavoritesHandler(
		service,
		func(*gin.Context) (uuid.UUID, error) { return apiUUID(246), nil },
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/me/favorites", nil),
	)
	if recorder.Code != http.StatusOK ||
		!strings.Contains(recorder.Body.String(), `"items":[]`) ||
		!strings.Contains(recorder.Body.String(), `"empty_state":"no_favorites"`) {
		t.Fatalf("empty status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

type fakeMyFavoritesApplication struct {
	page activity.MyFavoritesPage
	err  error

	calls       int
	principalID uuid.UUID
	request     MyFavoritesRequest
}

func (service *fakeMyFavoritesApplication) Read(
	_ context.Context,
	principalID uuid.UUID,
	request MyFavoritesRequest,
) (activity.MyFavoritesPage, error) {
	service.calls++
	service.principalID = principalID
	service.request = request
	return service.page, service.err
}
