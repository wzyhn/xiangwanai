package xiangwanapi

import (
	"context"
	"errors"
	"strconv"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type publicPeopleApplication interface {
	List(
		context.Context,
		PublicPeopleRequest,
	) (people.PublicProfilesPage, error)
	Read(context.Context, uuid.UUID) (people.Profile, error)
}

type PublicPeopleHandler struct {
	service publicPeopleApplication
}

func NewPublicPeopleHandler(
	service publicPeopleApplication,
) *PublicPeopleHandler {
	return &PublicPeopleHandler{service: service}
}

func (handler *PublicPeopleHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/people", handler.GetPeople)
	group.GET("/people/:people_id", handler.GetPerson)
}

// GetPeople godoc
// @Summary List published Xiangwan people
// @Description Anonymous stable page of moderated public PeopleProfiles. Trusted bindings and operator identities are never exposed.
// @Tags xiangwan
// @Produce json
// @Param cursor query string false "Opaque tenant-bound stable cursor"
// @Param limit query int false "Page size (1..100)" minimum(1) maximum(100) default(20)
// @Success 200 {object} PublicPeopleResponse
// @Failure 400 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/people [get]
func (handler *PublicPeopleHandler) GetPeople(c *gin.Context) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal("xiangwan public People is unavailable"))
		return
	}
	request, err := parsePublicPeopleRequest(c)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid public People query"))
		return
	}
	page, err := handler.service.List(c.Request.Context(), request)
	if err != nil {
		writePublicPeopleError(c, err)
		return
	}
	response.OK(c, projectPublicPeopleResponse(page))
}

// GetPerson godoc
// @Summary Read one published Xiangwan person
// @Description Anonymous detail for one exact moderated public PeopleProfile, including unbound profiles. Principal bindings are never exposed or inferred.
// @Tags xiangwan
// @Produce json
// @Param people_id path string true "PeopleProfile ID"
// @Success 200 {object} PublicPersonDetailResponse
// @Failure 400 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/people/{people_id} [get]
func (handler *PublicPeopleHandler) GetPerson(c *gin.Context) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal("xiangwan public Person is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid public Person query"))
		return
	}
	peopleID, err := parseCanonicalUUID(c.Param("people_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid PeopleProfile id"))
		return
	}
	profile, err := handler.service.Read(c.Request.Context(), peopleID)
	if err != nil {
		writePublicPeopleError(c, err)
		return
	}
	response.OK(c, PublicPersonDetailResponse{
		Person: projectPublicPersonResponse(profile),
	})
}

func parsePublicPeopleRequest(c *gin.Context) (PublicPeopleRequest, error) {
	query := c.Request.URL.Query()
	for key := range query {
		if key != "cursor" && key != "limit" {
			return PublicPeopleRequest{}, ErrInvalidPublicPeopleRequest
		}
	}
	for _, key := range []string{"cursor", "limit"} {
		if values, exists := query[key]; exists &&
			(len(values) != 1 || !canonicalHomeQueryValue(values[0])) {
			return PublicPeopleRequest{}, ErrInvalidPublicPeopleRequest
		}
	}
	request := PublicPeopleRequest{}
	if values, exists := query["cursor"]; exists {
		if len(values[0]) > 2048 {
			return PublicPeopleRequest{}, ErrInvalidPublicPeopleRequest
		}
		request.Cursor = values[0]
	}
	if values, exists := query["limit"]; exists {
		limit, err := strconv.Atoi(values[0])
		if err != nil || strconv.Itoa(limit) != values[0] || limit < 1 ||
			limit > people.MaxPublicProfilesLimit {
			return PublicPeopleRequest{}, ErrInvalidPublicPeopleRequest
		}
		request.Limit = limit
	}
	return request, nil
}

func writePublicPeopleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidPublicPeopleRequest),
		errors.Is(err, peoplepostgres.ErrInvalidPublicProfilesFilter),
		errors.Is(err, peoplepostgres.ErrInvalidPublicProfilesCursor):
		writeError(c, errx.NewBadRequest("invalid public People request"))
	case errors.Is(err, peoplepostgres.ErrStalePublicProfilesCursor):
		writeError(c, errx.NewConflict("public People changed; restart pagination"))
	case errors.Is(err, peoplepostgres.ErrProfileNotFound):
		writeError(c, errx.NewNotFound("PeopleProfile not found"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan public People failed"))
	}
}

type PublicPeopleResponse struct {
	Items      []PublicPersonResponse `json:"items"`
	AsOf       string                 `json:"as_of"`
	NextCursor string                 `json:"next_cursor,omitempty"`
	EmptyState string                 `json:"empty_state,omitempty"`
}

type PublicPersonDetailResponse struct {
	Person PublicPersonResponse `json:"person"`
}

type PublicPersonResponse struct {
	PeopleID     string  `json:"people_id"`
	DisplayName  string  `json:"display_name"`
	Headline     *string `json:"headline,omitempty"`
	Introduction string  `json:"introduction"`
	Version      int64   `json:"version"`
	PublishedAt  string  `json:"published_at"`
}

func projectPublicPeopleResponse(
	page people.PublicProfilesPage,
) PublicPeopleResponse {
	result := PublicPeopleResponse{
		Items:      make([]PublicPersonResponse, 0, len(page.Items)),
		AsOf:       formatMyRegistrationTime(page.AsOf),
		NextCursor: page.NextCursor,
	}
	for _, profile := range page.Items {
		result.Items = append(result.Items, projectPublicPersonResponse(profile))
	}
	if len(result.Items) == 0 {
		result.EmptyState = "no_people"
	}
	return result
}

func projectPublicPersonResponse(profile people.Profile) PublicPersonResponse {
	result := PublicPersonResponse{
		PeopleID:     profile.ID.String(),
		DisplayName:  profile.DisplayName,
		Introduction: profile.Introduction,
		Version:      profile.Version,
	}
	if profile.Headline != nil {
		headline := *profile.Headline
		result.Headline = &headline
	}
	if profile.ModeratedAt != nil {
		result.PublishedAt = formatMyRegistrationTime(*profile.ModeratedAt)
	}
	return result
}
