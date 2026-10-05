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
)

func TestPublicPastActivitiesHandlerReturnsAnonymousInstanceCards(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	item := activity.PastActivityItem{
		SeriesID:                         apiUUID(72),
		SeriesTitle:                      "AI Community Nights",
		SuccessfulPublishedInstanceCount: 4,
		HistoricalRegistrationCount:      86,
		InstanceID:                       apiUUID(73),
		InstanceTitle:                    "September Night",
		InstanceStatus:                   activity.InstanceStatusCompleted,
		ActivityType:                     activity.ActivityTypeAIRoundtable,
		CoverImageURL:                    "https://cdn.example.com/covers/september-night.png",
		PublicationVersion:               2,
		PublishedAt:                      completedAt.Add(-24 * time.Hour),
		CompletedAt:                      completedAt,
	}
	service := &fakePublicPastActivitiesApplication{page: activity.PastActivitiesPage{
		Items:              []activity.PastActivityItem{item},
		ActiveActivityType: activity.ActivityTypeAIRoundtable,
		AsOf:               completedAt.Add(time.Hour),
		NextCursor:         "next-page",
	}}
	engine := gin.New()
	NewPublicPastActivitiesHandler(service).RegisterRoutes(
		engine.Group("/api/v1/xiangwan"),
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/xiangwan/past-activities?activity_type=ai_roundtable&limit=10",
		nil,
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET past activities status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Code int                          `json:"code"`
		Data PublicPastActivitiesResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Code != 0 || len(envelope.Data.Items) != 1 ||
		envelope.Data.Items[0].InstanceID != item.InstanceID.String() ||
		envelope.Data.Items[0].SeriesTitle != item.SeriesTitle ||
		envelope.Data.Items[0].CoverImageURL != item.CoverImageURL ||
		envelope.Data.Items[0].CompletedAt != completedAt.Format(time.RFC3339Nano) ||
		envelope.Data.NextCursor != "next-page" {
		t.Fatalf("GET past activities response = %+v", envelope)
	}
	if service.request.ActivityType != activity.ActivityTypeAIRoundtable ||
		service.request.Limit != 10 {
		t.Fatalf("service request = %+v", service.request)
	}
}

func TestPublicPastActivitiesHandlerRejectsAmbiguousQuery(t *testing.T) {
	t.Parallel()

	for _, query := range []string{
		"?activity_type=unknown",
		"?activity_type=course&activity_type=course",
		"?limit=01",
		"?session_id=anything",
	} {
		service := &fakePublicPastActivitiesApplication{}
		engine := gin.New()
		NewPublicPastActivitiesHandler(service).RegisterRoutes(
			engine.Group("/api/v1/xiangwan"),
		)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(
			recorder,
			httptest.NewRequest(
				http.MethodGet,
				"/api/v1/xiangwan/past-activities"+query,
				nil,
			),
		)
		if recorder.Code != http.StatusBadRequest || service.calls != 0 {
			t.Fatalf("query=%s status=%d calls=%d body=%s", query, recorder.Code, service.calls, recorder.Body.String())
		}
	}
}

func TestPublicPastActivitiesHandlerUsesOpaqueErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err        error
		wantStatus int
	}{
		{err: activity.ErrStalePastActivitiesCursor, wantStatus: http.StatusConflict},
		{err: errors.New("private postgres host"), wantStatus: http.StatusInternalServerError},
	}
	for _, test := range tests {
		engine := gin.New()
		NewPublicPastActivitiesHandler(
			&fakePublicPastActivitiesApplication{err: test.err},
		).RegisterRoutes(engine.Group("/api/v1/xiangwan"))
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(
			recorder,
			httptest.NewRequest(http.MethodGet, "/api/v1/xiangwan/past-activities", nil),
		)
		if recorder.Code != test.wantStatus ||
			strings.Contains(recorder.Body.String(), "private postgres") {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

type fakePublicPastActivitiesApplication struct {
	page    activity.PastActivitiesPage
	err     error
	request PublicPastActivitiesRequest
	calls   int
}

func (fake *fakePublicPastActivitiesApplication) Read(
	_ context.Context,
	request PublicPastActivitiesRequest,
) (activity.PastActivitiesPage, error) {
	fake.calls++
	fake.request = request
	return fake.page, fake.err
}
