package xiangwanapi

import (
	"context"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type publicSessionDetailApplication interface {
	ReadSessionDetail(
		context.Context,
		uuid.UUID,
	) (PublicSessionDetailPage, error)
}

type PublicSessionDetailHandler struct {
	service publicSessionDetailApplication
}

func NewPublicSessionDetailHandler(
	service publicSessionDetailApplication,
) *PublicSessionDetailHandler {
	return &PublicSessionDetailHandler{service: service}
}

func (handler *PublicSessionDetailHandler) RegisterRoutes(
	group *gin.RouterGroup,
) {
	group.GET("/sessions/:session_id", handler.GetSessionDetail)
}

// GetSessionDetail godoc
// @Summary Read one exact published Xiangwan Session
// @Description Anonymous detail for the path Session only; never substitutes or returns a Session selector.
// @Tags xiangwan
// @Produce json
// @Param session_id path string true "Session ID"
// @Success 200 {object} PublicSessionDetailResponse
// @Failure 400 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/sessions/{session_id} [get]
func (handler *PublicSessionDetailHandler) GetSessionDetail(c *gin.Context) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal("xiangwan Session detail is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid Session detail query"))
		return
	}
	sessionID, err := parseCanonicalUUID(c.Param("session_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Session id"))
		return
	}
	page, err := handler.service.ReadSessionDetail(
		c.Request.Context(),
		sessionID,
	)
	if err != nil {
		writePublicSessionDetailError(c, err)
		return
	}
	response.OK(c, projectPublicSessionDetailResponse(page))
}

func writePublicSessionDetailError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidPublicSessionDetailRequest):
		writeError(c, errx.NewBadRequest("invalid Session detail request"))
	case errors.Is(err, activity.ErrSessionDetailUnavailable):
		writeError(c, errx.NewNotFound("Session not found"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Session detail failed"))
	}
}

type PublicSessionDetailResponse struct {
	BrandStatus                      activity.BrandLifecycleStatus  `json:"brand_status"`
	SeriesID                         string                         `json:"series_id"`
	InstanceID                       string                         `json:"instance_id"`
	SessionID                        string                         `json:"session_id"`
	PublicationVersion               int64                          `json:"publication_version"`
	SuccessfulPublishedInstanceCount int                            `json:"successful_published_instance_count"`
	HistoricalRegistrationCount      int64                          `json:"historical_registration_count"`
	InstanceTitle                    string                         `json:"instance_title"`
	SessionTitle                     string                         `json:"session_title"`
	ActivityType                     activity.ActivityType          `json:"activity_type"`
	QuickTagCodes                    []string                       `json:"quick_tag_codes"`
	QuickTags                        []PublicHomeQuickTagResponse   `json:"quick_tags"`
	CoverImageURL                    string                         `json:"cover_image_url"`
	ContentBlocks                    []activity.DetailBlock         `json:"content_blocks"`
	Leaders                          []PublicSessionLeaderResponse  `json:"leaders"`
	PreviousReview                   *PublicPreviousReviewResponse  `json:"previous_review"`
	RegistrationStartAt              string                         `json:"registration_start_at"`
	RegistrationEndAt                string                         `json:"registration_end_at"`
	SessionStartAt                   string                         `json:"session_start_at"`
	SessionEndAt                     string                         `json:"session_end_at"`
	PriceCents                       int64                          `json:"price_cents"`
	Delivery                         PublicSessionDeliveryResponse  `json:"delivery"`
	Display                          PublicHomeDisplayResponse      `json:"display"`
	CTA                              PublicSessionDetailCTAResponse `json:"cta"`
}

type PublicSessionLeaderResponse struct {
	DisplayName string `json:"display_name"`
	RoleLabel   string `json:"role_label"`
	Headline    string `json:"headline"`
	AvatarURL   string `json:"avatar_url"`
}

type PublicPreviousReviewResponse struct {
	InstanceID  string   `json:"instance_id"`
	SessionID   string   `json:"session_id,omitempty"`
	Title       string   `json:"title"`
	CompletedAt string   `json:"completed_at"`
	ImageURLs   []string `json:"image_urls"`
}

type PublicSessionDeliveryResponse struct {
	Mode                    activity.DeliveryMode `json:"mode"`
	Area                    activity.AreaCode     `json:"area"`
	VenueName               string                `json:"venue_name,omitempty"`
	Address                 string                `json:"address,omitempty"`
	Longitude               *float64              `json:"longitude,omitempty"`
	Latitude                *float64              `json:"latitude,omitempty"`
	OnlineParticipationMode string                `json:"online_participation_mode,omitempty"`
}

type PublicSessionDetailCTAResponse struct {
	Action  activity.SessionDetailCTAAction `json:"action"`
	Label   activity.SessionDetailCTALabel  `json:"label"`
	Enabled bool                            `json:"enabled"`
}

func projectPublicSessionDetailResponse(
	page PublicSessionDetailPage,
) PublicSessionDetailResponse {
	detail := page.Detail
	quickTags := append([]string(nil), detail.QuickTagCodes...)
	if quickTags == nil {
		quickTags = []string{}
	}
	quickTagLabels := make([]PublicHomeQuickTagResponse, 0, len(page.QuickTags))
	for _, tag := range page.QuickTags {
		quickTagLabels = append(quickTagLabels, PublicHomeQuickTagResponse{
			Code:  tag.Code,
			Label: tag.Label,
		})
	}
	contentBlocks := append([]activity.DetailBlock(nil), detail.ContentBlocks...)
	if contentBlocks == nil {
		contentBlocks = []activity.DetailBlock{}
	}
	leaders := make([]PublicSessionLeaderResponse, 0, len(page.Leaders))
	for _, leader := range page.Leaders {
		leaders = append(leaders, PublicSessionLeaderResponse{
			DisplayName: leader.DisplayName,
			RoleLabel:   leader.RoleLabel,
			Headline:    leader.Headline,
			AvatarURL:   leader.AvatarURL,
		})
	}
	return PublicSessionDetailResponse{
		BrandStatus:                      page.BrandStatus,
		SeriesID:                         detail.SeriesID.String(),
		InstanceID:                       detail.InstanceID.String(),
		SessionID:                        detail.SessionID.String(),
		PublicationVersion:               detail.PublicationVersion,
		SuccessfulPublishedInstanceCount: detail.SuccessfulPublishedInstanceCount,
		HistoricalRegistrationCount:      detail.HistoricalRegistrationCount,
		InstanceTitle:                    detail.InstanceTitle,
		SessionTitle:                     detail.SessionTitle,
		ActivityType:                     detail.ActivityType,
		QuickTagCodes:                    quickTags,
		QuickTags:                        quickTagLabels,
		CoverImageURL:                    detail.CoverImageURL,
		ContentBlocks:                    contentBlocks,
		Leaders:                          leaders,
		PreviousReview:                   projectPublicPreviousReview(page.PreviousReview),
		RegistrationStartAt:              publicSessionTime(detail.RegistrationStartAt),
		RegistrationEndAt:                publicSessionTime(detail.RegistrationEndAt),
		SessionStartAt:                   publicSessionTime(detail.SessionStartAt),
		SessionEndAt:                     publicSessionTime(detail.SessionEndAt),
		PriceCents:                       detail.PriceCents,
		Delivery: PublicSessionDeliveryResponse{
			Mode:                    detail.DeliveryMode,
			Area:                    detail.Area,
			VenueName:               detail.VenueName,
			Address:                 detail.Address,
			Longitude:               clonePublicFloat(detail.Longitude),
			Latitude:                clonePublicFloat(detail.Latitude),
			OnlineParticipationMode: detail.OnlineParticipationMode,
		},
		Display: PublicHomeDisplayResponse{
			State:                      detail.Display.State,
			RegistrationAllowed:        detail.CTA.Enabled,
			Capacity:                   detail.Display.Capacity,
			ConfirmedRegistrationCount: detail.Display.ConfirmedRegistrationCount,
			SellableCapacity:           detail.Display.SellableCapacity,
			NeededToReachGroupMinimum:  detail.Display.NeededToReachGroupMinimum,
		},
		CTA: PublicSessionDetailCTAResponse{
			Action:  detail.CTA.Action,
			Label:   detail.CTA.Label,
			Enabled: detail.CTA.Enabled,
		},
	}
}

func publicSessionTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

// projectPublicPreviousReview renders the previous-Instance review preview;
// every image becomes the product-local media path the anonymous media route
// re-authenticates on each read.
func projectPublicPreviousReview(
	review *resource.PreviousInstanceReview,
) *PublicPreviousReviewResponse {
	if review == nil {
		return nil
	}
	imageURLs := make([]string, 0, len(review.Images))
	for _, image := range review.Images {
		if image.ExternalURL != "" {
			imageURLs = append(imageURLs, image.ExternalURL)
			continue
		}
		imageURLs = append(imageURLs, PublicMediaPath(
			image.RelationID,
			image.BlockID,
			image.FileID,
		))
	}
	result := &PublicPreviousReviewResponse{
		InstanceID:  review.InstanceID.String(),
		Title:       review.Title,
		CompletedAt: publicSessionTime(review.CompletedAt),
		ImageURLs:   imageURLs,
	}
	if review.SessionID != nil {
		result.SessionID = review.SessionID.String()
	}
	return result
}

func clonePublicFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
