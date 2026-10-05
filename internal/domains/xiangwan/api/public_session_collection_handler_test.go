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
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestPublicSessionCollectionHandlerRequiresExplicitChoiceForMany(t *testing.T) {
	t.Parallel()

	seriesID := apiUUID(150)
	instanceID := apiUUID(151)
	firstID := apiUUID(152)
	secondID := apiUUID(153)
	now := time.Date(2026, time.September, 15, 4, 0, 0, 0, time.UTC)
	service := &fakePublicSessionCollectionApplication{page: PublicSessionCollectionPage{
		BrandStatus: activity.BrandLifecycleActive,
		SeriesID:    &seriesID,
		InstanceID:  instanceID,
		Route: activity.SessionRouteResolution{
			Kind: activity.SessionRouteSelectionRequired,
			CandidateSessionIDs: []uuid.UUID{
				firstID,
				secondID,
			},
		},
		Sessions: []PublicInstanceSession{
			{
				SessionID:      firstID,
				Title:          "Morning workshop",
				Status:         activity.SessionStatusPublished,
				SessionStartAt: now.Add(time.Hour),
				SortOrder:      1,
			},
			{
				SessionID:      secondID,
				Title:          "Evening workshop",
				Status:         activity.SessionStatusPublished,
				SessionStartAt: now.Add(2 * time.Hour),
				SortOrder:      2,
			},
		},
	}}
	engine := gin.New()
	NewPublicSessionCollectionHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		responseRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/instances/"+instanceID.String()+"/sessions",
			nil,
		),
	)
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("GET Session collection status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var envelope struct {
		Code int                             `json:"code"`
		Data PublicSessionCollectionResponse `json:"data"`
	}
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Session collection response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.SeriesID != seriesID.String() ||
		envelope.Data.InstanceID != instanceID.String() ||
		envelope.Data.Action != activity.SessionRouteSelectionRequired ||
		envelope.Data.DirectSessionID != "" || len(envelope.Data.Sessions) != 2 ||
		envelope.Data.Sessions[0].SessionID != firstID.String() ||
		envelope.Data.Sessions[0].SessionTitle != "Morning workshop" ||
		envelope.Data.Sessions[0].DetailPath !=
			"/api/v1/xiangwan/sessions/"+firstID.String() ||
		service.instanceID != instanceID {
		t.Fatalf("Session collection response=%+v service=%+v", envelope.Data, service)
	}
	if strings.Contains(responseRecorder.Body.String(), "default_session") {
		t.Fatalf("multiple Sessions exposed a default: %s", responseRecorder.Body.String())
	}
}

func TestPublicSessionCollectionHandlerReturnsExplicitEmptyState(t *testing.T) {
	t.Parallel()

	instanceID := apiUUID(154)
	service := &fakePublicSessionCollectionApplication{page: PublicSessionCollectionPage{
		BrandStatus: activity.BrandLifecycleActive,
		InstanceID:  instanceID,
		Route: activity.SessionRouteResolution{
			Kind:                activity.SessionRouteUnavailable,
			CandidateSessionIDs: []uuid.UUID{},
		},
		Sessions: []PublicInstanceSession{},
	}}
	engine := gin.New()
	NewPublicSessionCollectionHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		responseRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/instances/"+instanceID.String()+"/sessions",
			nil,
		),
	)
	if responseRecorder.Code != http.StatusOK ||
		!strings.Contains(responseRecorder.Body.String(), `"sessions":[]`) ||
		!strings.Contains(responseRecorder.Body.String(), `"empty_state":"no_public_sessions"`) {
		t.Fatalf("empty Session collection status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func TestPublicSessionCollectionProjectionUsesReviewTargetForReviewOnlySession(t *testing.T) {
	t.Parallel()

	instanceID := apiUUID(161)
	sessionID := apiUUID(162)
	result := projectPublicSessionCollectionResponse(PublicSessionCollectionPage{
		BrandStatus: activity.BrandLifecycleActive,
		InstanceID:  instanceID,
		Route: activity.SessionRouteResolution{
			Kind:      activity.SessionRouteDirect,
			SessionID: &sessionID,
		},
		Sessions: []PublicInstanceSession{{
			SessionID:      sessionID,
			Title:          "Review-only Session",
			Status:         activity.SessionStatusEnded,
			ReviewOnly:     true,
			SessionStartAt: time.Date(2026, time.September, 14, 4, 0, 0, 0, time.UTC),
		}},
	})

	if len(result.Sessions) != 1 || result.Sessions[0].DetailPath != "" ||
		result.Sessions[0].ReviewPath != "/api/v1/xiangwan/instances/"+
			instanceID.String()+"/review?session_id="+sessionID.String() {
		t.Fatalf("review-only Session target = %+v", result.Sessions)
	}
}

func TestPublicSessionCollectionHandlerResolvesCanonicalSeriesRoute(t *testing.T) {
	t.Parallel()

	seriesID := apiUUID(158)
	instanceID := apiUUID(159)
	sessionID := apiUUID(160)
	service := &fakePublicSessionCollectionApplication{page: PublicSessionCollectionPage{
		BrandStatus: activity.BrandLifecycleActive,
		SeriesID:    &seriesID,
		InstanceID:  instanceID,
		Route: activity.SessionRouteResolution{
			Kind:      activity.SessionRouteDirect,
			SessionID: &sessionID,
		},
		Sessions: []PublicInstanceSession{{
			SessionID:      sessionID,
			Title:          "Current Session",
			Status:         activity.SessionStatusPublished,
			SessionStartAt: time.Date(2026, time.September, 15, 6, 0, 0, 0, time.UTC),
		}},
	}}
	engine := gin.New()
	NewPublicSessionCollectionHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		responseRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/series/"+seriesID.String()+"/sessions",
			nil,
		),
	)
	if responseRecorder.Code != http.StatusOK || service.calls != 1 ||
		service.seriesID != seriesID || service.instanceID != uuid.Nil ||
		!strings.Contains(
			responseRecorder.Body.String(),
			`"direct_session_id":"`+sessionID.String()+`"`,
		) {
		t.Fatalf(
			"Series route status=%d service=%+v body=%s",
			responseRecorder.Code,
			service,
			responseRecorder.Body.String(),
		)
	}
}

func TestPublicSessionCollectionHandlerRejectsAmbiguousRequest(t *testing.T) {
	t.Parallel()

	letteredID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	tests := []string{
		"not-a-uuid/sessions",
		strings.ToUpper(letteredID) + "/sessions",
		apiUUID(155).String() + "/sessions?session_id=" + apiUUID(156).String(),
	}
	for _, suffix := range tests {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakePublicSessionCollectionApplication{}
			engine := gin.New()
			NewPublicSessionCollectionHandler(service).RegisterRoutes(
				engine.Group("/api/v1/xiangwan"),
			)
			responseRecorder := httptest.NewRecorder()
			engine.ServeHTTP(
				responseRecorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/instances/"+suffix,
					nil,
				),
			)
			if responseRecorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("invalid collection request status=%d calls=%d body=%s", responseRecorder.Code, service.calls, responseRecorder.Body.String())
			}
		})
	}
}

func TestPublicSessionCollectionHandlerUsesOpaqueBackendErrors(t *testing.T) {
	t.Parallel()

	engine := gin.New()
	NewPublicSessionCollectionHandler(
		&fakePublicSessionCollectionApplication{
			err: errors.New("database credential is private"),
		},
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		responseRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/instances/"+apiUUID(157).String()+"/sessions",
			nil,
		),
	)
	body := responseRecorder.Body.String()
	if responseRecorder.Code != http.StatusInternalServerError ||
		!strings.Contains(body, `"code":10006`) ||
		strings.Contains(body, "database credential") {
		t.Fatalf("backend error status=%d body=%s", responseRecorder.Code, body)
	}
}

func TestPublicSessionCollectionHandlerRegistersCanonicalRoute(t *testing.T) {
	t.Parallel()

	engine := gin.New()
	NewPublicSessionCollectionHandler(
		&fakePublicSessionCollectionApplication{},
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	routes := engine.Routes()
	if len(routes) != 2 || routes[0].Method != http.MethodGet ||
		routes[0].Path != "/api/v1/xiangwan/series/:series_id/sessions" ||
		routes[1].Method != http.MethodGet ||
		routes[1].Path != "/api/v1/xiangwan/instances/:instance_id/sessions" {
		t.Fatalf("registered routes = %+v", routes)
	}
}

type fakePublicSessionCollectionApplication struct {
	page PublicSessionCollectionPage
	err  error

	calls      int
	seriesID   uuid.UUID
	instanceID uuid.UUID
}

func (fake *fakePublicSessionCollectionApplication) ReadSeriesSessions(
	_ context.Context,
	seriesID uuid.UUID,
) (PublicSessionCollectionPage, error) {
	fake.calls++
	fake.seriesID = seriesID
	return fake.page, fake.err
}

func (fake *fakePublicSessionCollectionApplication) ReadInstanceSessions(
	_ context.Context,
	instanceID uuid.UUID,
) (PublicSessionCollectionPage, error) {
	fake.calls++
	fake.instanceID = instanceID
	return fake.page, fake.err
}
