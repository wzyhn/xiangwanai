package xiangwanapi

import (
	"context"
	"errors"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type publicReviewApplication interface {
	ReadInstanceReview(
		ctx context.Context,
		instanceID uuid.UUID,
		sessionID *uuid.UUID,
	) (PublicReviewPage, error)
}

type PublicReviewHandler struct {
	service publicReviewApplication
}

func NewPublicReviewHandler(service publicReviewApplication) *PublicReviewHandler {
	return &PublicReviewHandler{service: service}
}

func (handler *PublicReviewHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/instances/:instance_id/review", handler.GetInstanceReview)
}

// GetInstanceReview godoc
// @Summary Read one published Xiangwan Instance review
// @Description Anonymous read; session_id adds only that exact ended Session's public resources.
// @Tags xiangwan
// @Produce json
// @Param instance_id path string true "Instance ID"
// @Param session_id query string false "Exact Session resource context"
// @Success 200 {object} PublicReviewResponse
// @Failure 400 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/instances/{instance_id}/review [get]
func (handler *PublicReviewHandler) GetInstanceReview(c *gin.Context) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal("xiangwan public review is unavailable"))
		return
	}
	instanceID, err := parseCanonicalUUID(c.Param("instance_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid instance id"))
		return
	}
	sessionID, err := parsePublicReviewSession(c)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid review query"))
		return
	}
	page, err := handler.service.ReadInstanceReview(
		c.Request.Context(),
		instanceID,
		sessionID,
	)
	if err != nil {
		writePublicReviewError(c, err)
		return
	}
	response.OK(c, projectPublicReviewResponse(page))
}

func parseCanonicalUUID(value string) (uuid.UUID, error) {
	trimmed := strings.TrimSpace(value)
	parsed, err := uuid.Parse(trimmed)
	if err != nil || parsed == uuid.Nil || parsed.String() != trimmed {
		return uuid.Nil, ErrInvalidPublicReviewRequest
	}
	return parsed, nil
}

func parsePublicReviewSession(c *gin.Context) (*uuid.UUID, error) {
	query := c.Request.URL.Query()
	for key := range query {
		if key != "session_id" {
			return nil, ErrInvalidPublicReviewRequest
		}
	}
	values, exists := query["session_id"]
	if !exists {
		return nil, nil
	}
	if len(values) != 1 {
		return nil, ErrInvalidPublicReviewRequest
	}
	parsed, err := parseCanonicalUUID(values[0])
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func writePublicReviewError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidPublicReviewRequest):
		writeError(c, errx.NewBadRequest("invalid review request"))
	case errors.Is(err, resource.ErrPublicReviewTargetUnavailable),
		errors.Is(err, activity.ErrPastActivityNotFound):
		writeError(c, errx.NewNotFound("review not found"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan public review failed"))
	}
}

type PublicReviewResponse struct {
	SeriesID                         string                         `json:"series_id"`
	SeriesTitle                      string                         `json:"series_title"`
	SuccessfulPublishedInstanceCount int                            `json:"successful_published_instance_count"`
	HistoricalRegistrationCount      int64                          `json:"historical_registration_count"`
	InstanceID                       string                         `json:"instance_id"`
	InstanceTitle                    string                         `json:"instance_title"`
	InstanceStatus                   activity.InstanceStatus        `json:"instance_status"`
	ActivityType                     activity.ActivityType          `json:"activity_type"`
	PublicationVersion               int64                          `json:"publication_version"`
	PublishedAt                      string                         `json:"published_at"`
	CompletedAt                      string                         `json:"completed_at"`
	SelectedSessionID                string                         `json:"selected_session_id,omitempty"`
	ContentBlocks                    []activity.DetailBlock         `json:"content_blocks"`
	Documents                        []PublicReviewDocumentResponse `json:"documents"`
	NextInstance                     NextInstanceRouteResponse      `json:"next_instance"`
}

type PublicReviewDocumentResponse struct {
	RelationID string                      `json:"relation_id"`
	Scope      resource.RelationKind       `json:"scope"`
	SessionID  string                      `json:"session_id,omitempty"`
	Title      string                      `json:"title"`
	SortOrder  int                         `json:"sort_order"`
	Blocks     []PublicReviewBlockResponse `json:"blocks"`
}

type PublicReviewBlockResponse struct {
	BlockID      string                                 `json:"block_id"`
	Type         resource.PublicReviewBlockType         `json:"type"`
	SortOrder    int                                    `json:"sort_order"`
	Availability resource.PublicReviewBlockAvailability `json:"availability"`
	Text         string                                 `json:"text,omitempty"`
	Label        string                                 `json:"label,omitempty"`
	Subtitle     string                                 `json:"subtitle,omitempty"`
	ExternalURL  string                                 `json:"external_url,omitempty"`
	VideoChannel *resource.ReviewVideoChannel           `json:"video_channel,omitempty"`
	FileID       string                                 `json:"file_id,omitempty"`
	MIME         string                                 `json:"mime,omitempty"`
	MediaPath    string                                 `json:"media_path,omitempty"`
	IsCover      bool                                   `json:"is_cover,omitempty"`
}

type NextInstanceRouteResponse struct {
	Action              activity.SessionRouteResolutionKind `json:"action"`
	InstanceID          string                              `json:"instance_id,omitempty"`
	SessionID           string                              `json:"session_id,omitempty"`
	CandidateSessionIDs []string                            `json:"candidate_session_ids"`
}

func projectPublicReviewResponse(page PublicReviewPage) PublicReviewResponse {
	result := PublicReviewResponse{
		SeriesID:                         page.Review.Target.SeriesID.String(),
		SeriesTitle:                      page.Activity.SeriesTitle,
		SuccessfulPublishedInstanceCount: page.Activity.SuccessfulPublishedInstanceCount,
		HistoricalRegistrationCount:      page.Activity.HistoricalRegistrationCount,
		InstanceID:                       page.Review.Target.InstanceID.String(),
		InstanceTitle:                    page.Activity.InstanceTitle,
		InstanceStatus:                   page.Activity.InstanceStatus,
		ActivityType:                     page.Activity.ActivityType,
		PublicationVersion:               page.Activity.PublicationVersion,
		PublishedAt:                      formatPublicHomeTime(page.Activity.PublishedAt),
		CompletedAt:                      formatPublicHomeTime(page.Activity.CompletedAt),
		ContentBlocks:                    append([]activity.DetailBlock(nil), page.ContentBlocks...),
		Documents:                        make([]PublicReviewDocumentResponse, 0, len(page.Review.Documents)),
		NextInstance:                     projectNextInstanceRoute(page.NextRoute),
	}
	if result.ContentBlocks == nil {
		result.ContentBlocks = []activity.DetailBlock{}
	}
	if page.Review.Target.SessionID != nil {
		result.SelectedSessionID = page.Review.Target.SessionID.String()
	}
	for _, document := range page.Review.Documents {
		projected := PublicReviewDocumentResponse{
			RelationID: document.RelationID.String(),
			Scope:      document.Kind,
			Title:      document.Title,
			SortOrder:  document.SortOrder,
			Blocks:     make([]PublicReviewBlockResponse, 0, len(document.Blocks)),
		}
		if document.SessionID != nil {
			projected.SessionID = document.SessionID.String()
		}
		for _, block := range document.Blocks {
			projectedBlock := PublicReviewBlockResponse{
				BlockID:      block.BlockID.String(),
				Type:         block.Type,
				SortOrder:    block.SortOrder,
				Availability: block.Availability,
				Text:         block.Text,
				Label:        block.Label,
				Subtitle:     block.Subtitle,
				ExternalURL:  block.ExternalURL,
				VideoChannel: block.VideoChannel,
				MIME:         block.MIME,
				IsCover:      block.IsCover,
			}
			if block.FileID != nil {
				projectedBlock.FileID = block.FileID.String()
				if block.Availability == resource.PublicReviewBlockAvailable {
					projectedBlock.MediaPath = PublicMediaPath(
						document.RelationID,
						block.BlockID,
						*block.FileID,
					)
				}
			}
			projected.Blocks = append(projected.Blocks, projectedBlock)
		}
		result.Documents = append(result.Documents, projected)
	}
	return result
}

func projectNextInstanceRoute(
	value activity.NextInstanceRouteResolution,
) NextInstanceRouteResponse {
	result := NextInstanceRouteResponse{
		Action:              value.SessionRoute.Kind,
		CandidateSessionIDs: make([]string, 0, len(value.SessionRoute.CandidateSessionIDs)),
	}
	if value.TargetInstanceID != nil {
		result.InstanceID = value.TargetInstanceID.String()
	}
	if value.SessionRoute.SessionID != nil {
		result.SessionID = value.SessionRoute.SessionID.String()
	}
	for _, sessionID := range value.SessionRoute.CandidateSessionIDs {
		result.CandidateSessionIDs = append(
			result.CandidateSessionIDs,
			sessionID.String(),
		)
	}
	return result
}
