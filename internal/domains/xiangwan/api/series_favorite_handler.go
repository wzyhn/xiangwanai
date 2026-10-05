package xiangwanapi

import (
	"context"
	"errors"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type seriesFavoriteApplication interface {
	Set(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		bool,
	) (activity.SeriesFavoriteState, error)
}

type SeriesFavoriteHandler struct {
	service   seriesFavoriteApplication
	principal PrincipalResolver
}

func NewSeriesFavoriteHandler(
	service seriesFavoriteApplication,
	principal PrincipalResolver,
) *SeriesFavoriteHandler {
	return &SeriesFavoriteHandler{service: service, principal: principal}
}

func (handler *SeriesFavoriteHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.PUT("/series/:series_id/favorite", handler.Favorite)
	group.DELETE("/series/:series_id/favorite", handler.Unfavorite)
}

// Favorite godoc
// @Summary Mark one Xiangwan Series as wanted
// @Description BUSINESS-STATE target command. Tenant and Principal are server-derived. PostgreSQL atomically creates the unique owner/Series relation and increments the Series counter; repeats return the current target state without moving the counter.
// @Tags xiangwan
// @Produce json
// @Param series_id path string true "Series ID"
// @Security BearerAuth
// @Success 200 {object} SeriesFavoriteResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/series/{series_id}/favorite [put]
func (handler *SeriesFavoriteHandler) Favorite(c *gin.Context) {
	handler.set(c, true)
}

// Unfavorite godoc
// @Summary Remove the authenticated consumer's wanted state for one Series
// @Description BUSINESS-STATE target command. PostgreSQL atomically removes the owner/Series relation and decrements the Series counter; repeats return the already-absent target state without moving the counter.
// @Tags xiangwan
// @Produce json
// @Param series_id path string true "Series ID"
// @Security BearerAuth
// @Success 200 {object} SeriesFavoriteResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/series/{series_id}/favorite [delete]
func (handler *SeriesFavoriteHandler) Unfavorite(c *gin.Context) {
	handler.set(c, false)
}

func (handler *SeriesFavoriteHandler) set(c *gin.Context, favorited bool) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan Series favorite is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.Request.ContentLength != 0 ||
		len(c.Request.TransferEncoding) != 0 ||
		!validSeriesFavoriteContentType(c.GetHeader("Content-Type")) ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid Series favorite request"))
		return
	}
	seriesID, err := parseCanonicalUUID(c.Param("series_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Series id"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	state, err := handler.service.Set(
		c.Request.Context(),
		principalID,
		seriesID,
		favorited,
	)
	if err != nil {
		writeSeriesFavoriteError(c, err)
		return
	}
	response.OK(c, projectSeriesFavoriteResponse(state))
}

func validSeriesFavoriteContentType(value string) bool {
	return value == "" || value == "application/json"
}

func writeSeriesFavoriteError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidSeriesFavoriteRequest),
		errors.Is(err, activitypostgres.ErrInvalidSeriesFavoriteCommand),
		errors.Is(err, activity.ErrInvalidSeriesFavoriteState):
		writeError(c, errx.NewBadRequest("invalid Series favorite request"))
	case errors.Is(err, activitypostgres.ErrSeriesFavoritePrincipalUnavailable):
		writeError(c, errx.NewForbidden("Series favorite is unavailable"))
	case errors.Is(err, activitypostgres.ErrSeriesFavoriteUnavailable):
		writeError(c, errx.NewNotFound("Series not found"))
	case errors.Is(err, activitypostgres.ErrSeriesFavoriteTransactionConflict):
		writeError(c, errx.NewConflict("Series favorite changed; refresh and retry"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan Series favorite failed"))
	}
}

type SeriesFavoriteResponse struct {
	SeriesID      string `json:"series_id"`
	Favorited     bool   `json:"favorited"`
	Changed       bool   `json:"changed"`
	FavoriteCount int64  `json:"favorite_count"`
	SeriesVersion int64  `json:"series_version"`
	OccurredAt    string `json:"occurred_at"`
}

func projectSeriesFavoriteResponse(
	state activity.SeriesFavoriteState,
) SeriesFavoriteResponse {
	return SeriesFavoriteResponse{
		SeriesID:      state.SeriesID.String(),
		Favorited:     state.Favorited,
		Changed:       state.Changed,
		FavoriteCount: state.FavoriteCount,
		SeriesVersion: state.SeriesVersion,
		OccurredAt:    formatMyRegistrationTime(state.OccurredAt),
	}
}
