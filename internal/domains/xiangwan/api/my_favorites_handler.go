package xiangwanapi

import (
	"context"
	"errors"
	"strconv"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type myFavoritesApplication interface {
	Read(
		context.Context,
		uuid.UUID,
		MyFavoritesRequest,
	) (activity.MyFavoritesPage, error)
}

type MyFavoritesHandler struct {
	service   myFavoritesApplication
	principal PrincipalResolver
}

func NewMyFavoritesHandler(
	service myFavoritesApplication,
	principal PrincipalResolver,
) *MyFavoritesHandler {
	return &MyFavoritesHandler{service: service, principal: principal}
}

func (handler *MyFavoritesHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/me/favorites", handler.GetMyFavorites)
}

// GetMyFavorites godoc
// @Summary List the authenticated consumer's favorite Xiangwan Series
// @Description Returns only safe Series display facts from the token principal's PostgreSQL favorite relations. Internal relation, tenant, principal, and current Instance identities are never exposed.
// @Tags xiangwan
// @Produce json
// @Param cursor query string false "Opaque owner-bound stable cursor"
// @Param limit query int false "Page size (1..100)" minimum(1) maximum(100) default(20)
// @Security BearerAuth
// @Success 200 {object} MyFavoritesResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/favorites [get]
func (handler *MyFavoritesHandler) GetMyFavorites(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan My Favorites is unavailable"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request, err := parseMyFavoritesRequest(c)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid My Favorites query"))
		return
	}
	page, err := handler.service.Read(c.Request.Context(), principalID, request)
	if err != nil {
		writeMyFavoritesError(c, err)
		return
	}
	response.OK(c, projectMyFavoritesResponse(page))
}

func parseMyFavoritesRequest(c *gin.Context) (MyFavoritesRequest, error) {
	query := c.Request.URL.Query()
	allowed := map[string]struct{}{
		"cursor": {},
		"limit":  {},
	}
	for key := range query {
		if _, exists := allowed[key]; !exists {
			return MyFavoritesRequest{}, ErrInvalidMyFavoritesRequest
		}
	}
	for _, key := range []string{"cursor", "limit"} {
		if values, exists := query[key]; exists &&
			(len(values) != 1 || !canonicalHomeQueryValue(values[0])) {
			return MyFavoritesRequest{}, ErrInvalidMyFavoritesRequest
		}
	}
	request := MyFavoritesRequest{}
	if values, exists := query["cursor"]; exists {
		if len(values[0]) > 2048 {
			return MyFavoritesRequest{}, ErrInvalidMyFavoritesRequest
		}
		request.Cursor = values[0]
	}
	if values, exists := query["limit"]; exists {
		limit, err := strconv.Atoi(values[0])
		if err != nil || strconv.Itoa(limit) != values[0] || limit < 1 ||
			limit > activity.MaxMyFavoritesLimit {
			return MyFavoritesRequest{}, ErrInvalidMyFavoritesRequest
		}
		request.Limit = limit
	}
	return request, nil
}

func writeMyFavoritesError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidMyFavoritesRequest),
		errors.Is(err, activitypostgres.ErrInvalidMyFavoritesFilter),
		errors.Is(err, activitypostgres.ErrInvalidMyFavoritesCursor):
		writeError(c, errx.NewBadRequest("invalid My Favorites request"))
	case errors.Is(err, activitypostgres.ErrStaleMyFavoritesCursor):
		writeError(c, errx.NewConflict("My Favorites changed; restart pagination"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan My Favorites failed"))
	}
}

type MyFavoritesResponse struct {
	Items      []MyFavoriteResponse `json:"items"`
	AsOf       string               `json:"as_of"`
	NextCursor string               `json:"next_cursor,omitempty"`
	EmptyState string               `json:"empty_state,omitempty"`
}

type MyFavoriteResponse struct {
	SeriesID          string                `json:"series_id"`
	Title             string                `json:"title"`
	SeriesStatus      activity.SeriesStatus `json:"series_status"`
	FavoriteCount     int64                 `json:"favorite_count"`
	FavoriteAvatars   []string              `json:"favorite_avatars"`
	FavoritedAt       string                `json:"favorited_at"`
	SessionsAvailable bool                  `json:"sessions_available"`
	SessionsPath      string                `json:"sessions_path,omitempty"`
}

func projectMyFavoritesResponse(
	page activity.MyFavoritesPage,
) MyFavoritesResponse {
	result := MyFavoritesResponse{
		Items:      make([]MyFavoriteResponse, 0, len(page.Items)),
		AsOf:       formatMyRegistrationTime(page.AsOf),
		NextCursor: page.NextCursor,
	}
	for _, item := range page.Items {
		favoriteAvatars := append([]string(nil), item.FavoriteAvatars...)
		if favoriteAvatars == nil {
			favoriteAvatars = []string{}
		}
		projected := MyFavoriteResponse{
			SeriesID:          item.SeriesID.String(),
			Title:             item.Title,
			SeriesStatus:      item.SeriesStatus,
			FavoriteCount:     item.FavoriteCount,
			FavoriteAvatars:   favoriteAvatars,
			FavoritedAt:       formatMyRegistrationTime(item.FavoritedAt),
			SessionsAvailable: item.SessionsAvailable,
		}
		if item.SessionsAvailable {
			projected.SessionsPath = "/api/v1/xiangwan/series/" +
				item.SeriesID.String() + "/sessions"
		}
		result.Items = append(result.Items, projected)
	}
	if len(result.Items) == 0 {
		result.EmptyState = "no_favorites"
	}
	return result
}
