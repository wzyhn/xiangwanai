package xiangwanapi

import (
	"context"
	"errors"
	"strconv"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type publicPastActivitiesApplication interface {
	Read(
		context.Context,
		PublicPastActivitiesRequest,
	) (activity.PastActivitiesPage, error)
}

type PublicPastActivitiesHandler struct {
	service publicPastActivitiesApplication
}

func NewPublicPastActivitiesHandler(
	service publicPastActivitiesApplication,
) *PublicPastActivitiesHandler {
	return &PublicPastActivitiesHandler{service: service}
}

func (handler *PublicPastActivitiesHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.GET("/past-activities", handler.GetPastActivities)
}

// GetPastActivities godoc
// @Summary List completed Xiangwan activity Instances
// @Description Anonymous, tenant-fixed past activity cards. Cancelled Instances are excluded and pagination is bound to the category and PostgreSQL snapshot time.
// @Tags xiangwan
// @Produce json
// @Param activity_type query string false "Activity type" Enums(all, ai_roundtable, special_event, course, competition, custom)
// @Param cursor query string false "Opaque tenant-bound stable cursor"
// @Param limit query int false "Page size (1..50)" minimum(1) maximum(50) default(20)
// @Success 200 {object} PublicPastActivitiesResponse
// @Failure 400 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/past-activities [get]
func (handler *PublicPastActivitiesHandler) GetPastActivities(c *gin.Context) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal(
			"xiangwan public past activities is unavailable",
		))
		return
	}
	request, err := parsePublicPastActivitiesRequest(c)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid past activities query"))
		return
	}
	page, err := handler.service.Read(c.Request.Context(), request)
	if err != nil {
		writePublicPastActivitiesError(c, err)
		return
	}
	response.OK(c, projectPublicPastActivitiesResponse(page))
}

func parsePublicPastActivitiesRequest(
	c *gin.Context,
) (PublicPastActivitiesRequest, error) {
	query := c.Request.URL.Query()
	allowed := map[string]struct{}{
		"activity_type": {},
		"cursor":        {},
		"limit":         {},
	}
	for key := range query {
		if _, exists := allowed[key]; !exists {
			return PublicPastActivitiesRequest{},
				ErrInvalidPublicPastActivitiesRequest
		}
	}
	for _, key := range []string{"activity_type", "cursor", "limit"} {
		if values, exists := query[key]; exists &&
			(len(values) != 1 || !canonicalHomeQueryValue(values[0])) {
			return PublicPastActivitiesRequest{},
				ErrInvalidPublicPastActivitiesRequest
		}
	}
	request := PublicPastActivitiesRequest{}
	if values, exists := query["activity_type"]; exists {
		request.ActivityType = activity.ActivityType(values[0])
		if !activity.ValidPastActivityType(request.ActivityType) {
			return PublicPastActivitiesRequest{},
				ErrInvalidPublicPastActivitiesRequest
		}
	}
	if values, exists := query["cursor"]; exists {
		if len(values[0]) > 2048 {
			return PublicPastActivitiesRequest{},
				ErrInvalidPublicPastActivitiesRequest
		}
		request.Cursor = values[0]
	}
	if values, exists := query["limit"]; exists {
		limit, err := strconv.Atoi(values[0])
		if err != nil || strconv.Itoa(limit) != values[0] || limit < 1 ||
			limit > activity.MaxPastActivitiesLimit {
			return PublicPastActivitiesRequest{},
				ErrInvalidPublicPastActivitiesRequest
		}
		request.Limit = limit
	}
	return request, nil
}

func writePublicPastActivitiesError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidPublicPastActivitiesRequest),
		errors.Is(err, activity.ErrInvalidPastActivitiesFilter),
		errors.Is(err, activity.ErrInvalidPastActivitiesCursor):
		writeError(c, errx.NewBadRequest("invalid past activities request"))
	case errors.Is(err, activity.ErrStalePastActivitiesCursor):
		writeError(c, errx.NewConflict(
			"past activities changed; restart pagination",
		))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan past activities failed"))
	}
}

type PublicPastActivitiesResponse struct {
	ActiveActivityType activity.ActivityType            `json:"active_activity_type"`
	Items              []PublicPastActivityItemResponse `json:"items"`
	AsOf               string                           `json:"as_of"`
	NextCursor         string                           `json:"next_cursor,omitempty"`
	EmptyState         string                           `json:"empty_state,omitempty"`
}

type PublicPastActivityItemResponse struct {
	SeriesID                         string                  `json:"series_id"`
	SeriesTitle                      string                  `json:"series_title"`
	SuccessfulPublishedInstanceCount int                     `json:"successful_published_instance_count"`
	HistoricalRegistrationCount      int64                   `json:"historical_registration_count"`
	InstanceID                       string                  `json:"instance_id"`
	InstanceTitle                    string                  `json:"instance_title"`
	InstanceStatus                   activity.InstanceStatus `json:"instance_status"`
	ActivityType                     activity.ActivityType   `json:"activity_type"`
	CoverImageURL                    string                  `json:"cover_image_url"`
	PublicationVersion               int64                   `json:"publication_version"`
	PublishedAt                      string                  `json:"published_at"`
	CompletedAt                      string                  `json:"completed_at"`
}

func projectPublicPastActivitiesResponse(
	page activity.PastActivitiesPage,
) PublicPastActivitiesResponse {
	result := PublicPastActivitiesResponse{
		ActiveActivityType: page.ActiveActivityType,
		Items:              make([]PublicPastActivityItemResponse, 0, len(page.Items)),
		AsOf:               formatPublicHomeTime(page.AsOf),
		NextCursor:         page.NextCursor,
	}
	for _, item := range page.Items {
		result.Items = append(result.Items, PublicPastActivityItemResponse{
			SeriesID:                         item.SeriesID.String(),
			SeriesTitle:                      item.SeriesTitle,
			SuccessfulPublishedInstanceCount: item.SuccessfulPublishedInstanceCount,
			HistoricalRegistrationCount:      item.HistoricalRegistrationCount,
			InstanceID:                       item.InstanceID.String(),
			InstanceTitle:                    item.InstanceTitle,
			InstanceStatus:                   item.InstanceStatus,
			ActivityType:                     item.ActivityType,
			CoverImageURL:                    item.CoverImageURL,
			PublicationVersion:               item.PublicationVersion,
			PublishedAt:                      formatPublicHomeTime(item.PublishedAt),
			CompletedAt:                      formatPublicHomeTime(item.CompletedAt),
		})
	}
	if len(result.Items) == 0 {
		result.EmptyState = "no_past_activities"
	}
	return result
}
