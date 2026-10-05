package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/gin-gonic/gin"
)

func TestPublicHomeHandlerReturnsCanonicalSessionCards(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 14, 4, 30, 0, 0, time.UTC)
	seriesID := apiUUID(90)
	instanceID := apiUUID(91)
	sessionID := apiUUID(92)
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	service := &fakePublicHomeApplication{page: PublicHomePage{
		Profile: profile,
		Catalog: activity.HomeCatalog{
			Cards: []activity.HomeCard{{
				SeriesID:           seriesID,
				InstanceID:         instanceID,
				SessionID:          sessionID,
				PublicationVersion: 3,
				InstanceTitle:      "AI Roundtable",
				SessionTitle:       "Sunday Session",
				ActivityType:       activity.ActivityTypeAIRoundtable,
				QuickTagCodes:      []string{"ai"},
				CoverImageURL:      "https://cdn.example.com/covers/ai-roundtable.jpg",
				ParticipantAvatars: []string{"/avatars/one.png", "/avatars/two.png"},
				FavoriteAvatars:    []string{"/avatars/favorite-one.png", "/avatars/favorite-two.png"},
				SessionStartAt:     now.Add(2 * time.Hour),
				SessionEndAt:       now.Add(4 * time.Hour),
				PriceCents:         9900,
				DeliveryMode:       activity.DeliveryModeOffline,
				Area:               activity.AreaCodeHeping,
				VenueName:          "Xiangwan Lab",
				Display: activity.SessionDisplayDecision{
					State:                      activity.DisplayStateOpenNeedGroup,
					Capacity:                   20,
					ConfirmedRegistrationCount: 4,
					SellableCapacity:           16,
					NeededToReachGroupMinimum:  2,
				},
				HomeGroup:            activity.HomeGroupOpen,
				HeatCount:            30,
				CurrentFavoriteUsers: 8,
				CTAAction:            activity.HomeCTAActionSessionDetail,
				CTALabel:             activity.HomeCTALabelViewDetails,
			}},
			AvailableQuickTags: profile.AvailableQuickTags,
			ActiveFilter: activity.HomeFilter{
				ActivityType: activity.ActivityTypeAIRoundtable,
				Area:         activity.AreaCodeHeping,
				TimeWindow:   activity.HomeTimeWindowThisWeek,
				QuickTags:    []string{"ai"},
				Limit:        10,
			},
			BusinessTimezone:             activity.BusinessTimezone,
			OpenRegistrationSessionCount: 1,
			AsOf:                         now,
			NextCursor:                   "opaque-next",
		},
	}}
	engine := gin.New()
	NewPublicHomeHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/xiangwan/home-sessions?activity_type=ai_roundtable"+
			"&area=heping&time_window=this_week&quick_tag=ai&limit=10",
		nil,
	)
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("GET home status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var envelope struct {
		Code int                `json:"code"`
		Data PublicHomeResponse `json:"data"`
	}
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode home response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.BrandIntro != profile.BrandIntro ||
		envelope.Data.BusinessTimezone != activity.BusinessTimezone ||
		envelope.Data.OpenRegistrationSessionCount != 1 ||
		len(envelope.Data.AvailableQuickTags) != 1 ||
		len(envelope.Data.Cards) != 1 ||
		envelope.Data.Cards[0].SessionID != sessionID.String() ||
		envelope.Data.Cards[0].CoverImageURL != "https://cdn.example.com/covers/ai-roundtable.jpg" ||
		!reflect.DeepEqual(envelope.Data.Cards[0].ParticipantAvatars, []string{"/avatars/one.png", "/avatars/two.png"}) ||
		!reflect.DeepEqual(envelope.Data.Cards[0].FavoriteAvatars, []string{"/avatars/favorite-one.png", "/avatars/favorite-two.png"}) ||
		envelope.Data.Cards[0].CurrentFavoriteUsers != 8 ||
		envelope.Data.Cards[0].HomeGroup != "open" ||
		!envelope.Data.Cards[0].Display.RegistrationAllowed ||
		envelope.Data.Cards[0].CTAAction != activity.HomeCTAActionSessionDetail ||
		envelope.Data.NextCursor != "opaque-next" {
		t.Fatalf("GET home response = %+v", envelope.Data)
	}
	wantFilter := activity.HomeFilter{
		ActivityType: activity.ActivityTypeAIRoundtable,
		Area:         activity.AreaCodeHeping,
		TimeWindow:   activity.HomeTimeWindowThisWeek,
		QuickTags:    []string{"ai"},
		Limit:        10,
	}
	if service.calls != 1 || !reflect.DeepEqual(service.filter, wantFilter) {
		t.Fatalf("service calls=%d filter=%+v", service.calls, service.filter)
	}
	body := responseRecorder.Body.String()
	for _, forbidden := range []string{
		"active_hold_count",
		"current_publication_version",
		"published_by",
		"address",
		"longitude",
		"latitude",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("private fact %q crossed HTTP boundary: %s", forbidden, body)
		}
	}
}

func TestPublicHomeHandlerReturnsExplicitEmptyState(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	profile := validPublicHomeProfile(now.Add(-time.Hour))
	service := &fakePublicHomeApplication{page: PublicHomePage{
		Profile: profile,
		Catalog: activity.HomeCatalog{
			Cards:              []activity.HomeCard{},
			AvailableQuickTags: profile.AvailableQuickTags,
			ActiveFilter: activity.HomeFilter{
				ActivityType: activity.ActivityTypeAll,
				Area:         activity.AreaCodeAll,
				TimeWindow:   activity.HomeTimeWindowAll,
				QuickTags:    []string{},
				Limit:        activity.DefaultHomeLimit,
			},
			BusinessTimezone: activity.BusinessTimezone,
			AsOf:             now,
		},
	}}
	engine := gin.New()
	NewPublicHomeHandler(service).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		responseRecorder,
		httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/home-sessions", nil),
	)
	if responseRecorder.Code != http.StatusOK ||
		!strings.Contains(responseRecorder.Body.String(), `"cards":[]`) ||
		!strings.Contains(responseRecorder.Body.String(), `"empty_state":"no_matching_sessions"`) {
		t.Fatalf("empty home status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
}

func TestPublicHomeHandlerRejectsAmbiguousQueryBeforeService(t *testing.T) {
	t.Parallel()

	tests := []string{
		"?unknown=value",
		"?activity_type=all&activity_type=course",
		"?area=",
		"?limit=01",
		"?limit=+1",
		"?cursor=%20opaque",
		"?quick_tag=",
		"?quick_tag=a&quick_tag=b&quick_tag=c&quick_tag=d&quick_tag=e&quick_tag=f",
	}
	for _, suffix := range tests {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakePublicHomeApplication{}
			engine := gin.New()
			NewPublicHomeHandler(service).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			responseRecorder := httptest.NewRecorder()
			engine.ServeHTTP(
				responseRecorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/home-sessions"+suffix,
					nil,
				),
			)
			if responseRecorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("invalid query status=%d calls=%d body=%s", responseRecorder.Code, service.calls, responseRecorder.Body.String())
			}
		})
	}
}

func TestPublicHomeHandlerUsesStableOpaqueErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid", err: activity.ErrInvalidHomeFilter, wantStatus: http.StatusBadRequest, wantCode: `"code":10001`},
		{name: "stale", err: activity.ErrStaleHomeCursor, wantStatus: http.StatusConflict, wantCode: `"code":10005`},
		{name: "backend", err: errors.New("database credential is private"), wantStatus: http.StatusInternalServerError, wantCode: `"code":10006`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := gin.New()
			NewPublicHomeHandler(
				&fakePublicHomeApplication{err: test.err},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			responseRecorder := httptest.NewRecorder()
			engine.ServeHTTP(
				responseRecorder,
				httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/home-sessions", nil),
			)
			body := responseRecorder.Body.String()
			if responseRecorder.Code != test.wantStatus ||
				!strings.Contains(body, test.wantCode) ||
				strings.Contains(body, "database credential") {
				t.Fatalf("error status=%d body=%s", responseRecorder.Code, body)
			}
		})
	}
}

func TestPublicHomeHandlerRegistersCanonicalRoute(t *testing.T) {
	t.Parallel()

	engine := gin.New()
	NewPublicHomeHandler(&fakePublicHomeApplication{}).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	routes := engine.Routes()
	if len(routes) != 1 || routes[0].Method != http.MethodGet ||
		routes[0].Path != "/api/v1/xiangwan/home-sessions" {
		t.Fatalf("registered routes = %+v", routes)
	}
}

type fakePublicHomeApplication struct {
	page PublicHomePage
	err  error

	calls  int
	filter activity.HomeFilter
}

func (fake *fakePublicHomeApplication) ReadHome(
	_ context.Context,
	filter activity.HomeFilter,
) (PublicHomePage, error) {
	fake.calls++
	fake.filter = filter
	return fake.page, fake.err
}
