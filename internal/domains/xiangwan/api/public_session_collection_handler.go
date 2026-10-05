package xiangwanapi

import (
	"context"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type publicSessionCollectionApplication interface {
	ReadSeriesSessions(
		context.Context,
		uuid.UUID,
	) (PublicSessionCollectionPage, error)
	ReadInstanceSessions(
		context.Context,
		uuid.UUID,
	) (PublicSessionCollectionPage, error)
}

type PublicSessionCollectionHandler struct {
	service publicSessionCollectionApplication
}

func NewPublicSessionCollectionHandler(
	service publicSessionCollectionApplication,
) *PublicSessionCollectionHandler {
	return &PublicSessionCollectionHandler{service: service}
}

func (handler *PublicSessionCollectionHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.GET("/series/:series_id/sessions", handler.GetSeriesSessions)
	group.GET("/instances/:instance_id/sessions", handler.GetInstanceSessions)
}

// GetSeriesSessions godoc
// @Summary Resolve public Sessions for one Xiangwan Series
// @Description Resolves the Series through its current public Instance and returns zero, one, or an ordered explicit Session choice; multiple Sessions are never defaulted.
// @Tags xiangwan
// @Produce json
// @Param series_id path string true "Series ID"
// @Success 200 {object} PublicSessionCollectionResponse
// @Failure 400 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/series/{series_id}/sessions [get]
func (handler *PublicSessionCollectionHandler) GetSeriesSessions(c *gin.Context) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal("xiangwan Session collection is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid Session collection query"))
		return
	}
	seriesID, err := parseCanonicalUUID(c.Param("series_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Series id"))
		return
	}
	page, err := handler.service.ReadSeriesSessions(
		c.Request.Context(),
		seriesID,
	)
	if err != nil {
		writePublicSessionCollectionError(c, err)
		return
	}
	response.OK(c, projectPublicSessionCollectionResponse(page))
}

// GetInstanceSessions godoc
// @Summary Resolve public Sessions for one Xiangwan Instance
// @Description Returns zero, one, or an ordered explicit Session choice; multiple Sessions are never defaulted.
// @Tags xiangwan
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Success 200 {object} PublicSessionCollectionResponse
// @Failure 400 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/instances/{instance_id}/sessions [get]
func (handler *PublicSessionCollectionHandler) GetInstanceSessions(
	c *gin.Context,
) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal("xiangwan Session collection is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid Session collection query"))
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Instance id"))
		return
	}
	page, err := handler.service.ReadInstanceSessions(
		c.Request.Context(),
		instanceID,
	)
	if err != nil {
		writePublicSessionCollectionError(c, err)
		return
	}
	response.OK(c, projectPublicSessionCollectionResponse(page))
}

func writePublicSessionCollectionError(c *gin.Context, err error) {
	if errors.Is(err, ErrInvalidPublicSessionCollectionRequest) {
		writeError(c, errx.NewBadRequest("invalid Session collection request"))
		return
	}
	_ = c.Error(err)
	writeError(c, errx.NewInternal("xiangwan Session collection failed"))
}

type PublicSessionCollectionResponse struct {
	BrandStatus     activity.BrandLifecycleStatus       `json:"brand_status"`
	SeriesID        string                              `json:"series_id,omitempty"`
	InstanceID      string                              `json:"instance_id,omitempty"`
	Action          activity.SessionRouteResolutionKind `json:"action"`
	DirectSessionID string                              `json:"direct_session_id,omitempty"`
	Sessions        []PublicSessionChoiceResponse       `json:"sessions"`
	EmptyState      string                              `json:"empty_state,omitempty"`
}

type PublicSessionChoiceResponse struct {
	SessionID      string                 `json:"session_id"`
	SessionTitle   string                 `json:"session_title"`
	Status         activity.SessionStatus `json:"status"`
	SessionStartAt string                 `json:"session_start_at"`
	SortOrder      int                    `json:"sort_order"`
	DetailPath     string                 `json:"detail_path,omitempty"`
	ReviewPath     string                 `json:"review_path,omitempty"`
}

func projectPublicSessionCollectionResponse(
	page PublicSessionCollectionPage,
) PublicSessionCollectionResponse {
	result := PublicSessionCollectionResponse{
		BrandStatus: page.BrandStatus,
		Action:      page.Route.Kind,
		Sessions:    make([]PublicSessionChoiceResponse, 0, len(page.Sessions)),
	}
	if page.SeriesID != nil {
		result.SeriesID = page.SeriesID.String()
	}
	if page.InstanceID != uuid.Nil {
		result.InstanceID = page.InstanceID.String()
	}
	if page.Route.SessionID != nil {
		result.DirectSessionID = page.Route.SessionID.String()
	}
	for _, session := range page.Sessions {
		choice := PublicSessionChoiceResponse{
			SessionID:      session.SessionID.String(),
			SessionTitle:   session.Title,
			Status:         session.Status,
			SessionStartAt: session.SessionStartAt.UTC().Format(time.RFC3339Nano),
			SortOrder:      session.SortOrder,
		}
		if session.ReviewOnly {
			choice.ReviewPath = "/api/v1/xiangwan/instances/" + page.InstanceID.String() +
				"/review?session_id=" + session.SessionID.String()
		} else {
			choice.DetailPath = "/api/v1/xiangwan/sessions/" + session.SessionID.String()
		}
		result.Sessions = append(result.Sessions, choice)
	}
	if len(result.Sessions) == 0 {
		result.EmptyState = "no_public_sessions"
	}
	return result
}
