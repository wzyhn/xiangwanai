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
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestPublicSessionDetailHandlerProjectsEnhancedContent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	sessionID := apiUUID(125)
	detail := validPublicSessionDetail(sessionID, now)
	detail.ContentBlocks = []activity.DetailBlock{
		{Type: activity.DetailBlockTypeText, Title: "活动亮点", Body: "三面环水"},
		{
			Type:    activity.DetailBlockTypeImage,
			URL:     "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.jpg",
			Caption: "现场",
		},
	}
	completedAt := now.Add(-7 * 24 * time.Hour)
	page := PublicSessionDetailPage{
		BrandStatus: activity.BrandLifecycleActive,
		Detail:      detail,
		QuickTags: []activity.HomeQuickTag{
			{Code: "ai", Label: "AI 社区"},
			{Code: "newcomer", Label: "新人友好"},
		},
		Leaders: []people.SessionLeader{
			{
				RoleCode:    people.InstanceRoleHost,
				RoleLabel:   "主理人",
				DisplayName: "主理",
				Headline:    "行家",
				AvatarURL:   "https://cdn.example.com/avatar.jpg",
			},
			{
				RoleCode:    people.InstanceRoleEventSpeaker,
				RoleLabel:   "活动分享者",
				DisplayName: "分享者",
			},
		},
		PreviousReview: &resource.PreviousInstanceReview{
			InstanceID:  apiUUID(126),
			Title:       "第八期圆桌",
			CompletedAt: completedAt,
			Images: []resource.PreviousReviewImage{
				{
					RelationID: apiUUID(127), BlockID: apiUUID(128),
					FileID: apiUUID(129),
				},
				{RelationID: apiUUID(127), BlockID: apiUUID(130), ExternalURL: "https://images.example.com/review.jpg"},
			},
		},
	}
	service := &fakePublicSessionDetailApplication{page: page}
	engine := gin.New()
	NewPublicSessionDetailHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		responseRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/sessions/"+sessionID.String(),
			nil,
		),
	)
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("GET Session status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var envelope struct {
		Code int                         `json:"code"`
		Data PublicSessionDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Session response: %v", err)
	}
	data := envelope.Data
	if len(data.ContentBlocks) != 2 ||
		data.ContentBlocks[0].Type != activity.DetailBlockTypeText ||
		data.ContentBlocks[1].Caption != "现场" {
		t.Fatalf("content_blocks = %+v", data.ContentBlocks)
	}
	if len(data.QuickTags) != 2 || data.QuickTags[0].Code != "ai" ||
		data.QuickTags[0].Label != "AI 社区" {
		t.Fatalf("quick_tags = %+v", data.QuickTags)
	}
	if len(data.Leaders) != 2 || data.Leaders[0].RoleLabel != "主理人" ||
		data.Leaders[1].AvatarURL != "" {
		t.Fatalf("leaders = %+v", data.Leaders)
	}
	if data.PreviousReview == nil ||
		data.PreviousReview.InstanceID != apiUUID(126).String() ||
		data.PreviousReview.CompletedAt != completedAt.UTC().Format(time.RFC3339Nano) ||
		len(data.PreviousReview.ImageURLs) != 2 {
		t.Fatalf("previous_review = %+v", data.PreviousReview)
	}
	wantMediaPath := "/api/v1/xiangwan/media/" + apiUUID(127).String() + "/" +
		apiUUID(128).String() + "/" + apiUUID(129).String()
	if data.PreviousReview.ImageURLs[0] != wantMediaPath {
		t.Fatalf("previous_review image = %q, want %q",
			data.PreviousReview.ImageURLs[0], wantMediaPath)
	}
	if data.PreviousReview.ImageURLs[1] != "https://images.example.com/review.jpg" {
		t.Fatalf("previous_review external image = %q", data.PreviousReview.ImageURLs[1])
	}
}

func TestPublicSessionDetailHandlerProjectsEmptyExtras(t *testing.T) {
	t.Parallel()

	detail := validPublicSessionDetail(apiUUID(135), time.Now().UTC())
	projected := projectPublicSessionDetailResponse(PublicSessionDetailPage{
		BrandStatus: activity.BrandLifecycleActive,
		Detail:      detail,
	})
	if projected.ContentBlocks == nil || len(projected.ContentBlocks) != 0 {
		t.Fatalf("content_blocks = %+v, want []", projected.ContentBlocks)
	}
	if projected.Leaders == nil || len(projected.Leaders) != 0 {
		t.Fatalf("leaders = %+v, want []", projected.Leaders)
	}
	if projected.QuickTags == nil || len(projected.QuickTags) != 0 {
		t.Fatalf("quick_tags = %+v, want []", projected.QuickTags)
	}
	if projected.PreviousReview != nil {
		t.Fatalf("previous_review = %+v, want nil", projected.PreviousReview)
	}
	body, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	for _, fragment := range []string{
		`"content_blocks":[]`,
		`"leaders":[]`,
		`"quick_tags":[]`,
		`"previous_review":null`,
	} {
		if !strings.Contains(string(body), fragment) {
			t.Fatalf("response missing %s: %s", fragment, body)
		}
	}
}

func TestPublicSessionDetailHandlerKeepsLinkOnlyPreviousReview(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, time.September, 7, 8, 0, 0, 0, time.UTC)
	projected := projectPublicSessionDetailResponse(PublicSessionDetailPage{
		BrandStatus: activity.BrandLifecycleActive,
		Detail:      validPublicSessionDetail(apiUUID(136), completedAt),
		PreviousReview: &resource.PreviousInstanceReview{
			InstanceID:  apiUUID(137),
			Title:       "第八期圆桌",
			CompletedAt: completedAt,
			Images:      []resource.PreviousReviewImage{},
		},
	})
	if projected.PreviousReview == nil ||
		projected.PreviousReview.InstanceID != apiUUID(137).String() ||
		projected.PreviousReview.ImageURLs == nil ||
		len(projected.PreviousReview.ImageURLs) != 0 {
		t.Fatalf("link-only previous_review = %+v, want identity with empty image_urls", projected.PreviousReview)
	}
	body, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(body), `"image_urls":[]`) {
		t.Fatalf("link-only response image_urls is not an empty array: %s", body)
	}
}

func TestPublicSessionDetailHandlerReturnsOneExactSession(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	sessionID := apiUUID(120)
	detail := validPublicSessionDetail(sessionID, now)
	longitude := 117.2
	latitude := 39.1
	detail.Longitude = &longitude
	detail.Latitude = &latitude
	service := &fakePublicSessionDetailApplication{page: PublicSessionDetailPage{
		BrandStatus: activity.BrandLifecycleActive,
		Detail:      detail,
	}}
	engine := gin.New()
	NewPublicSessionDetailHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		responseRecorder,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/xiangwan/sessions/"+sessionID.String(),
			nil,
		),
	)
	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("GET Session status=%d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var envelope struct {
		Code int                         `json:"code"`
		Data PublicSessionDetailResponse `json:"data"`
	}
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode Session response: %v", err)
	}
	if envelope.Code != 0 || envelope.Data.SessionID != sessionID.String() ||
		envelope.Data.InstanceID != detail.InstanceID.String() ||
		envelope.Data.Delivery.Mode != activity.DeliveryModeOffline ||
		envelope.Data.Delivery.Address != "Heping District" ||
		envelope.Data.Delivery.Longitude == nil ||
		*envelope.Data.Delivery.Longitude != longitude ||
		envelope.Data.SuccessfulPublishedInstanceCount != 4 ||
		envelope.Data.HistoricalRegistrationCount != 28 ||
		envelope.Data.CTA.Action != activity.SessionDetailCTAActionStartRegistration ||
		!envelope.Data.CTA.Enabled || service.sessionID != sessionID {
		t.Fatalf("GET Session response=%+v service=%+v", envelope.Data, service)
	}
	body := responseRecorder.Body.String()
	for _, forbidden := range []string{
		"candidate_session_ids",
		"session_selector",
		"active_hold_count",
		"online_url",
		"private_link",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("forbidden detail field %q crossed boundary: %s", forbidden, body)
		}
	}
}

func TestPublicSessionDetailHandlerProjectsOnlineModeWithoutLocation(t *testing.T) {
	t.Parallel()

	detail := validPublicSessionDetail(apiUUID(121), time.Now().UTC())
	detail.DeliveryMode = activity.DeliveryModeOnline
	detail.Area = activity.AreaCodeOnline
	detail.VenueName = ""
	detail.Address = ""
	detail.OnlineParticipationMode = "wechat_group_after_registration"
	projected := projectPublicSessionDetailResponse(PublicSessionDetailPage{
		BrandStatus: activity.BrandLifecycleActive,
		Detail:      detail,
	})
	if projected.Delivery.Mode != activity.DeliveryModeOnline ||
		projected.Delivery.Area != activity.AreaCodeOnline ||
		projected.Delivery.OnlineParticipationMode != "wechat_group_after_registration" ||
		projected.Delivery.Longitude != nil || projected.Delivery.Latitude != nil ||
		projected.Delivery.Address != "" {
		t.Fatalf("online delivery response = %+v", projected.Delivery)
	}
}

func TestPublicSessionDetailHandlerRejectsAmbiguousIdentityBeforeService(t *testing.T) {
	t.Parallel()

	letteredID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	tests := []string{
		"not-a-uuid",
		strings.ToUpper(letteredID),
		apiUUID(122).String() + "?instance_id=" + apiUUID(123).String(),
	}
	for _, suffix := range tests {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakePublicSessionDetailApplication{}
			engine := gin.New()
			NewPublicSessionDetailHandler(service).RegisterRoutes(
				engine.Group("/api/v1/xiangwan"),
			)
			responseRecorder := httptest.NewRecorder()
			engine.ServeHTTP(
				responseRecorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/sessions/"+suffix,
					nil,
				),
			)
			if responseRecorder.Code != http.StatusBadRequest || service.calls != 0 {
				t.Fatalf("invalid identity status=%d calls=%d body=%s", responseRecorder.Code, service.calls, responseRecorder.Body.String())
			}
		})
	}
}

func TestPublicSessionDetailHandlerUsesOpaqueErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "unavailable", err: activity.ErrSessionDetailUnavailable, wantStatus: http.StatusNotFound, wantCode: `"code":10004`},
		{name: "backend", err: errors.New("database credential is private"), wantStatus: http.StatusInternalServerError, wantCode: `"code":10006`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := gin.New()
			NewPublicSessionDetailHandler(
				&fakePublicSessionDetailApplication{err: test.err},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			responseRecorder := httptest.NewRecorder()
			engine.ServeHTTP(
				responseRecorder,
				httptest.NewRequest(
					http.MethodGet,
					"/api/v1/xiangwan/sessions/"+apiUUID(124).String(),
					nil,
				),
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

func TestPublicSessionDetailHandlerRegistersCanonicalRoute(t *testing.T) {
	t.Parallel()

	engine := gin.New()
	NewPublicSessionDetailHandler(
		&fakePublicSessionDetailApplication{},
	).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
	routes := engine.Routes()
	if len(routes) != 1 || routes[0].Method != http.MethodGet ||
		routes[0].Path != "/api/v1/xiangwan/sessions/:session_id" {
		t.Fatalf("registered routes = %+v", routes)
	}
}

type fakePublicSessionDetailApplication struct {
	page PublicSessionDetailPage
	err  error

	calls     int
	sessionID uuid.UUID
}

func (fake *fakePublicSessionDetailApplication) ReadSessionDetail(
	_ context.Context,
	sessionID uuid.UUID,
) (PublicSessionDetailPage, error) {
	fake.calls++
	fake.sessionID = sessionID
	return fake.page, fake.err
}
