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

func TestSeriesFavoriteHandlerAppliesTargetStates(t *testing.T) {
	t.Parallel()

	principalID := apiUUID(178)
	seriesID := apiUUID(179)
	now := time.Date(2026, time.September, 14, 13, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		method      string
		favorited   bool
		changed     bool
		wantCount   int64
		wantVersion int64
	}{
		{
			name:        "favorite",
			method:      http.MethodPut,
			favorited:   true,
			changed:     true,
			wantCount:   8,
			wantVersion: 4,
		},
		{
			name:        "already unfavorited",
			method:      http.MethodDelete,
			favorited:   false,
			changed:     false,
			wantCount:   7,
			wantVersion: 3,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeSeriesFavoriteApplication{state: activity.SeriesFavoriteState{
				TenantID:      apiUUID(180),
				PrincipalID:   principalID,
				SeriesID:      seriesID,
				Favorited:     test.favorited,
				Changed:       test.changed,
				FavoriteCount: test.wantCount,
				SeriesVersion: test.wantVersion,
				OccurredAt:    now,
			}}
			engine := gin.New()
			NewSeriesFavoriteHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return principalID, nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			request := httptest.NewRequest(
				test.method,
				"/api/v1/xiangwan/series/"+seriesID.String()+"/favorite",
				nil,
			)
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("favorite status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			var envelope struct {
				Code int                    `json:"code"`
				Data SeriesFavoriteResponse `json:"data"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode favorite response: %v", err)
			}
			if envelope.Code != 0 || envelope.Data.SeriesID != seriesID.String() ||
				envelope.Data.Favorited != test.favorited ||
				envelope.Data.Changed != test.changed ||
				envelope.Data.FavoriteCount != test.wantCount ||
				envelope.Data.SeriesVersion != test.wantVersion ||
				service.calls != 1 || service.principalID != principalID ||
				service.seriesID != seriesID || service.favorited != test.favorited {
				t.Fatalf("response=%+v service=%+v", envelope.Data, service)
			}
			for _, forbidden := range []string{
				"tenant_id",
				"principal_id",
				"favorite_id",
				principalID.String(),
				apiUUID(180).String(),
			} {
				if strings.Contains(recorder.Body.String(), forbidden) {
					t.Fatalf("private field %q crossed HTTP boundary: %s", forbidden, recorder.Body.String())
				}
			}
		})
	}
}

func TestSeriesFavoriteHandlerRejectsNonCanonicalTransport(t *testing.T) {
	t.Parallel()

	letteredID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	tests := []struct {
		name         string
		path         string
		body         string
		contentType  string
		operationKey string
	}{
		{name: "invalid id", path: "/api/v1/xiangwan/series/not-a-uuid/favorite"},
		{
			name: "uppercase id",
			path: "/api/v1/xiangwan/series/" + strings.ToUpper(letteredID) + "/favorite",
		},
		{
			name: "query",
			path: "/api/v1/xiangwan/series/" + apiUUID(181).String() + "/favorite?target=true",
		},
		{
			name: "body",
			path: "/api/v1/xiangwan/series/" + apiUUID(182).String() + "/favorite",
			body: `{}`,
		},
		{
			name:        "unsupported content type",
			path:        "/api/v1/xiangwan/series/" + apiUUID(183).String() + "/favorite",
			contentType: "text/plain",
		},
		{
			name:         "operation key",
			path:         "/api/v1/xiangwan/series/" + apiUUID(184).String() + "/favorite",
			operationKey: apiUUID(185).String(),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeSeriesFavoriteApplication{}
			engine := gin.New()
			NewSeriesFavoriteHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) { return apiUUID(186), nil },
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			request := httptest.NewRequest(
				http.MethodPut,
				test.path,
				strings.NewReader(test.body),
			)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if test.operationKey != "" {
				request.Header.Set("Idempotency-Key", test.operationKey)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, service.calls, recorder.Body.String())
			}
		})
	}
}

func TestSeriesFavoriteHandlerMapsOpaqueFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		serviceErr   error
		principalErr error
		wantStatus   int
	}{
		{name: "unavailable Series", serviceErr: activitypostgres.ErrSeriesFavoriteUnavailable, wantStatus: http.StatusNotFound},
		{name: "inactive principal", serviceErr: activitypostgres.ErrSeriesFavoritePrincipalUnavailable, wantStatus: http.StatusForbidden},
		{name: "concurrent change", serviceErr: activitypostgres.ErrSeriesFavoriteTransactionConflict, wantStatus: http.StatusConflict},
		{name: "missing principal", principalErr: errx.NewUnauthorized("missing principal"), wantStatus: http.StatusUnauthorized},
		{name: "storage failure", serviceErr: errors.New("private database address"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := &fakeSeriesFavoriteApplication{err: test.serviceErr}
			engine := gin.New()
			NewSeriesFavoriteHandler(
				service,
				func(*gin.Context) (uuid.UUID, error) {
					return apiUUID(187), test.principalErr
				},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(
				recorder,
				httptest.NewRequest(
					http.MethodPut,
					"/api/v1/xiangwan/series/"+apiUUID(188).String()+"/favorite",
					nil,
				),
			)
			if recorder.Code != test.wantStatus ||
				strings.Contains(recorder.Body.String(), "database address") {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

type fakeSeriesFavoriteApplication struct {
	state activity.SeriesFavoriteState
	err   error

	calls       int
	principalID uuid.UUID
	seriesID    uuid.UUID
	favorited   bool
}

func (fake *fakeSeriesFavoriteApplication) Set(
	_ context.Context,
	principalID uuid.UUID,
	seriesID uuid.UUID,
	favorited bool,
) (activity.SeriesFavoriteState, error) {
	fake.calls++
	fake.principalID = principalID
	fake.seriesID = seriesID
	fake.favorited = favorited
	return fake.state, fake.err
}
