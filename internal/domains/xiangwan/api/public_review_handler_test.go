package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestPublicReviewHandlerReturnsSafeAnonymousProjection(t *testing.T) {
	t.Parallel()

	seriesID := apiUUID(40)
	instanceID := apiUUID(41)
	sessionID := apiUUID(42)
	nextInstanceID := apiUUID(43)
	nextSessionA := apiUUID(44)
	nextSessionB := apiUUID(45)
	fileID := apiUUID(46)
	relationID := apiUUID(47)
	service := &fakePublicReviewApplication{page: PublicReviewPage{
		Activity: publicReviewSummary(seriesID, instanceID),
		ContentBlocks: []activity.DetailBlock{
			{
				Type:  activity.DetailBlockTypeText,
				Title: "简介",
				Body:  "本期活动介绍",
			},
			{
				Type:    activity.DetailBlockTypeImage,
				URL:     "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.webp",
				Caption: "现场照片",
			},
		},
		Review: resource.PublicReviewDetail{
			Target: resource.PastHighlightReviewTarget{
				SeriesID:   seriesID,
				InstanceID: instanceID,
				SessionID:  &sessionID,
			},
			Documents: []resource.PublicReviewDocument{
				{
					RelationID: relationID,
					ContentID:  apiUUID(48),
					SessionID:  &sessionID,
					Kind:       resource.RelationKindSessionResources,
					Title:      "Session resources",
					SortOrder:  2,
					Blocks: []resource.PublicReviewBlock{
						{
							BlockID:      apiUUID(49),
							Type:         resource.PublicReviewBlockTypeImage,
							SortOrder:    1,
							FileID:       &fileID,
							MIME:         "image/webp",
							Availability: resource.PublicReviewBlockAvailable,
						},
					},
				},
			},
		},
		NextRoute: activity.NextInstanceRouteResolution{
			SeriesID:         seriesID,
			SourceInstanceID: instanceID,
			TargetInstanceID: &nextInstanceID,
			SessionRoute: activity.SessionRouteResolution{
				Kind: activity.SessionRouteSelectionRequired,
				CandidateSessionIDs: []uuid.UUID{
					nextSessionA,
					nextSessionB,
				},
			},
		},
	}}
	engine := gin.New()
	NewPublicReviewHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)

	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/xiangwan/instances/"+instanceID.String()+
			"/review?session_id="+sessionID.String(),
		nil,
	)
	responseRecorder := httptest.NewRecorder()
	engine.ServeHTTP(responseRecorder, request)

	if responseRecorder.Code != http.StatusOK {
		t.Fatalf("GET review status = %d body=%s", responseRecorder.Code, responseRecorder.Body.String())
	}
	var envelope struct {
		Code    int                  `json:"code"`
		Message string               `json:"message"`
		Data    PublicReviewResponse `json:"data"`
	}
	if err := json.Unmarshal(responseRecorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Code != 0 || envelope.Message != "ok" ||
		envelope.Data.SeriesID != seriesID.String() ||
		envelope.Data.SeriesTitle != "AI Community Nights" ||
		envelope.Data.InstanceID != instanceID.String() ||
		envelope.Data.InstanceTitle != "September Night" ||
		envelope.Data.SuccessfulPublishedInstanceCount != 3 ||
		envelope.Data.HistoricalRegistrationCount != 42 ||
		envelope.Data.SelectedSessionID != sessionID.String() ||
		len(envelope.Data.Documents) != 1 ||
		len(envelope.Data.ContentBlocks) != 2 ||
		envelope.Data.ContentBlocks[0].Type != activity.DetailBlockTypeText ||
		envelope.Data.ContentBlocks[0].Title != "简介" ||
		envelope.Data.ContentBlocks[0].Body != "本期活动介绍" ||
		envelope.Data.ContentBlocks[1].Type != activity.DetailBlockTypeImage ||
		envelope.Data.ContentBlocks[1].URL != "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.webp" ||
		envelope.Data.ContentBlocks[1].Caption != "现场照片" ||
		envelope.Data.Documents[0].SessionID != sessionID.String() ||
		envelope.Data.Documents[0].Blocks[0].FileID != fileID.String() ||
		envelope.Data.Documents[0].Blocks[0].MediaPath != PublicMediaPath(
			relationID,
			apiUUID(49),
			fileID,
		) ||
		envelope.Data.NextInstance.Action !=
			activity.SessionRouteSelectionRequired ||
		len(envelope.Data.NextInstance.CandidateSessionIDs) != 2 {
		t.Fatalf("GET review response = %+v", envelope)
	}
	if service.instanceID != instanceID || service.sessionID == nil ||
		*service.sessionID != sessionID {
		t.Fatalf("service input = %+v", service)
	}
	body := responseRecorder.Body.String()
	if strings.Contains(body, "content_id") || strings.Contains(body, "file_key") ||
		strings.Contains(body, "cdn_url") {
		t.Fatalf("private storage detail crossed HTTP boundary: %s", body)
	}
}

func TestProjectPublicReviewResponseInitializesEmptyContentBlocks(t *testing.T) {
	t.Parallel()

	response := projectPublicReviewResponse(PublicReviewPage{
		Activity: publicReviewSummary(apiUUID(55), apiUUID(56)),
		Review: resource.PublicReviewDetail{
			Target: resource.PastHighlightReviewTarget{
				SeriesID:   apiUUID(55),
				InstanceID: apiUUID(56),
			},
		},
	})
	if response.ContentBlocks == nil {
		t.Fatal("content_blocks is nil; public JSON contract requires an empty array")
	}
	if len(response.ContentBlocks) != 0 {
		t.Fatalf("content_blocks = %+v, want empty", response.ContentBlocks)
	}
}

func TestPublicReviewHandlerRegistersCanonicalRoute(t *testing.T) {
	t.Parallel()

	engine := gin.New()
	NewPublicReviewHandler(&fakePublicReviewApplication{}).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	routes := engine.Routes()
	if len(routes) != 1 || routes[0].Method != http.MethodGet ||
		routes[0].Path !=
			"/api/v1/xiangwan/instances/:instance_id/review" {
		t.Fatalf("registered routes = %+v", routes)
	}
}

func TestPublicReviewHandlerRejectsAmbiguousQueryBeforeService(t *testing.T) {
	t.Parallel()

	instanceID := apiUUID(50).String()
	letteredInstanceID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	sessionID := apiUUID(51).String()
	tests := []string{
		"not-a-uuid/review",
		strings.ToUpper(letteredInstanceID) + "/review",
		instanceID + "/review?session_id=",
		instanceID + "/review?session_id=" + sessionID +
			"&session_id=" + sessionID,
		instanceID + "/review?series_id=" + apiUUID(52).String(),
	}
	for _, suffix := range tests {
		suffix := suffix
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			service := &fakePublicReviewApplication{}
			engine := gin.New()
			NewPublicReviewHandler(service).RegisterRoutes(
				engine.Group("/api/v1/xiangwan"),
			)
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/xiangwan/instances/"+suffix,
				nil,
			)
			responseRecorder := httptest.NewRecorder()
			engine.ServeHTTP(responseRecorder, request)
			if responseRecorder.Code != http.StatusBadRequest ||
				service.calls != 0 {
				t.Fatalf("invalid request status=%d calls=%d body=%s", responseRecorder.Code, service.calls, responseRecorder.Body.String())
			}
		})
	}
}

func TestPublicReviewHandlerUsesOpaquePublicErrors(t *testing.T) {
	t.Parallel()

	instanceID := apiUUID(60)
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unavailable",
			err:        resource.ErrPublicReviewTargetUnavailable,
			wantStatus: http.StatusNotFound,
			wantCode:   `"code":10004`,
		},
		{
			name:       "backend",
			err:        errors.New("database host and credential must stay private"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   `"code":10006`,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			engine := gin.New()
			NewPublicReviewHandler(
				&fakePublicReviewApplication{err: test.err},
			).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
			request := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/xiangwan/instances/"+instanceID.String()+"/review",
				nil,
			)
			responseRecorder := httptest.NewRecorder()
			engine.ServeHTTP(responseRecorder, request)
			body := responseRecorder.Body.String()
			if responseRecorder.Code != test.wantStatus ||
				!strings.Contains(body, test.wantCode) ||
				strings.Contains(body, "database host") {
				t.Fatalf("error response status=%d body=%s", responseRecorder.Code, body)
			}
		})
	}
}

type fakePublicReviewApplication struct {
	page PublicReviewPage
	err  error

	calls      int
	instanceID uuid.UUID
	sessionID  *uuid.UUID
}

func (fake *fakePublicReviewApplication) ReadInstanceReview(
	_ context.Context,
	instanceID uuid.UUID,
	sessionID *uuid.UUID,
) (PublicReviewPage, error) {
	fake.calls++
	fake.instanceID = instanceID
	fake.sessionID = sessionID
	return fake.page, fake.err
}
