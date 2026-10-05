package xiangwanapi

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type publicHomeApplication interface {
	ReadHome(
		context.Context,
		activity.HomeFilter,
	) (PublicHomePage, error)
}

type PublicHomeHandler struct {
	service publicHomeApplication
}

func NewPublicHomeHandler(service publicHomeApplication) *PublicHomeHandler {
	return &PublicHomeHandler{service: service}
}

func (handler *PublicHomeHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/home-sessions", handler.GetHomeSessions)
}

// GetHomeSessions godoc
// @Summary Read the published Xiangwan home catalog
// @Description Anonymous, side-effect-free Session cards from the selected BrandProfile and current PG publication facts.
// @Tags xiangwan
// @Produce json
// @Param activity_type query string false "Activity type" Enums(all, ai_roundtable, special_event, course, competition, custom)
// @Param area query string false "Session area" Enums(all, heping, hexi, hebei, nankai, hongqiao, hedong, wuqing, dongli, binhai, online)
// @Param time_window query string false "Business-time window" Enums(all, this_week, next_week, local_date)
// @Param local_date query string false "Asia/Shanghai date (YYYY-MM-DD)"
// @Param quick_tag query []string false "Published quick-tag code; repeat up to five times" collectionFormat(multi)
// @Param cursor query string false "Opaque stable cursor"
// @Param limit query int false "Page size (1..50)" minimum(1) maximum(50) default(20)
// @Success 200 {object} PublicHomeResponse
// @Failure 400 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/home-sessions [get]
func (handler *PublicHomeHandler) GetHomeSessions(c *gin.Context) {
	if handler == nil || handler.service == nil {
		writeError(c, errx.NewInternal("xiangwan public home is unavailable"))
		return
	}
	filter, err := parsePublicHomeFilter(c)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid home query"))
		return
	}
	page, err := handler.service.ReadHome(c.Request.Context(), filter)
	if err != nil {
		writePublicHomeError(c, err)
		return
	}
	response.OK(c, projectPublicHomeResponse(page))
}

func parsePublicHomeFilter(c *gin.Context) (activity.HomeFilter, error) {
	query := c.Request.URL.Query()
	allowed := map[string]struct{}{
		"activity_type": {},
		"area":          {},
		"time_window":   {},
		"local_date":    {},
		"quick_tag":     {},
		"cursor":        {},
		"limit":         {},
	}
	for key := range query {
		if _, ok := allowed[key]; !ok {
			return activity.HomeFilter{}, ErrInvalidPublicHomeRequest
		}
	}
	for _, key := range []string{
		"activity_type",
		"area",
		"time_window",
		"local_date",
		"cursor",
		"limit",
	} {
		if values, exists := query[key]; exists &&
			(len(values) != 1 || !canonicalHomeQueryValue(values[0])) {
			return activity.HomeFilter{}, ErrInvalidPublicHomeRequest
		}
	}

	filter := activity.HomeFilter{}
	if value, exists := oneHomeQueryValue(query, "activity_type"); exists {
		filter.ActivityType = activity.ActivityType(value)
	}
	if value, exists := oneHomeQueryValue(query, "area"); exists {
		filter.Area = activity.AreaCode(value)
	}
	if value, exists := oneHomeQueryValue(query, "time_window"); exists {
		filter.TimeWindow = activity.HomeTimeWindow(value)
	}
	if value, exists := oneHomeQueryValue(query, "local_date"); exists {
		filter.LocalDate = value
	}
	if value, exists := oneHomeQueryValue(query, "cursor"); exists {
		if len(value) > 4096 {
			return activity.HomeFilter{}, ErrInvalidPublicHomeRequest
		}
		filter.Cursor = value
	}
	if value, exists := oneHomeQueryValue(query, "limit"); exists {
		limit, err := strconv.Atoi(value)
		if err != nil || strconv.Itoa(limit) != value {
			return activity.HomeFilter{}, ErrInvalidPublicHomeRequest
		}
		filter.Limit = limit
	}
	if values, exists := query["quick_tag"]; exists {
		if len(values) == 0 || len(values) > activity.MaxHomeQuickTags {
			return activity.HomeFilter{}, ErrInvalidPublicHomeRequest
		}
		filter.QuickTags = make([]string, 0, len(values))
		for _, value := range values {
			if !canonicalHomeQueryValue(value) {
				return activity.HomeFilter{}, ErrInvalidPublicHomeRequest
			}
			filter.QuickTags = append(filter.QuickTags, value)
		}
	}
	return filter, nil
}

func canonicalHomeQueryValue(value string) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		!strings.ContainsAny(value, "\r\n\x00")
}

func oneHomeQueryValue(
	query map[string][]string,
	key string,
) (string, bool) {
	values, exists := query[key]
	if !exists {
		return "", false
	}
	return values[0], true
}

func writePublicHomeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidPublicHomeRequest),
		errors.Is(err, activity.ErrInvalidHomeFilter),
		errors.Is(err, activity.ErrInvalidHomeCursor):
		writeError(c, errx.NewBadRequest("invalid home request"))
	case errors.Is(err, activity.ErrStaleHomeCursor):
		writeError(c, errx.NewConflict("home catalog changed; restart pagination"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan public home failed"))
	}
}

type PublicHomeResponse struct {
	CommunityName                string                        `json:"community_name"`
	BrandIntro                   string                        `json:"brand_intro"`
	HeroMode                     activity.HomeHeroMode         `json:"hero_mode"`
	HeroEyebrow                  string                        `json:"hero_eyebrow"`
	HeroSubtitle                 string                        `json:"hero_subtitle"`
	HeroImageURL                 string                        `json:"hero_image_url"`
	HeroImageAlt                 string                        `json:"hero_image_alt"`
	BrandStatus                  activity.BrandLifecycleStatus `json:"brand_status"`
	BrandPublicationVersion      int64                         `json:"brand_publication_version"`
	BrandPublishedAt             string                        `json:"brand_published_at"`
	AvailableQuickTags           []PublicHomeQuickTagResponse  `json:"available_quick_tags"`
	ActiveFilters                PublicHomeFilterResponse      `json:"active_filters"`
	BusinessTimezone             string                        `json:"business_timezone"`
	OpenRegistrationSessionCount int                           `json:"open_registration_session_count"`
	Cards                        []PublicHomeCardResponse      `json:"cards"`
	AsOf                         string                        `json:"as_of"`
	NextCursor                   string                        `json:"next_cursor,omitempty"`
	EmptyState                   string                        `json:"empty_state,omitempty"`
}

type PublicHomeQuickTagResponse struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

type PublicHomeFilterResponse struct {
	ActivityType activity.ActivityType   `json:"activity_type"`
	Area         activity.AreaCode       `json:"area"`
	TimeWindow   activity.HomeTimeWindow `json:"time_window"`
	LocalDate    string                  `json:"local_date,omitempty"`
	QuickTags    []string                `json:"quick_tags"`
	Limit        int                     `json:"limit"`
}

type PublicHomeCardResponse struct {
	SeriesID                string                    `json:"series_id"`
	InstanceID              string                    `json:"instance_id"`
	SessionID               string                    `json:"session_id"`
	PublicationVersion      int64                     `json:"publication_version"`
	InstanceTitle           string                    `json:"instance_title"`
	SessionTitle            string                    `json:"session_title"`
	ActivityType            activity.ActivityType     `json:"activity_type"`
	QuickTagCodes           []string                  `json:"quick_tag_codes"`
	CoverImageURL           string                    `json:"cover_image_url"`
	ParticipantAvatars      []string                  `json:"participant_avatars"`
	FavoriteAvatars         []string                  `json:"favorite_avatars"`
	SessionStartAt          string                    `json:"session_start_at"`
	SessionEndAt            string                    `json:"session_end_at"`
	PriceCents              int64                     `json:"price_cents"`
	DeliveryMode            activity.DeliveryMode     `json:"delivery_mode"`
	Area                    activity.AreaCode         `json:"area"`
	VenueName               string                    `json:"venue_name,omitempty"`
	OnlineParticipationMode string                    `json:"online_participation_mode,omitempty"`
	Display                 PublicHomeDisplayResponse `json:"display"`
	HomeGroup               string                    `json:"home_group"`
	HeatCount               int64                     `json:"heat_count"`
	CurrentFavoriteUsers    int64                     `json:"current_favorite_users"`
	CTAAction               activity.HomeCTAAction    `json:"cta_action"`
	CTALabel                activity.HomeCTALabel     `json:"cta_label"`
}

type PublicHomeDisplayResponse struct {
	State                      activity.DisplayState `json:"state"`
	RegistrationAllowed        bool                  `json:"registration_allowed"`
	Capacity                   int                   `json:"capacity"`
	ConfirmedRegistrationCount int                   `json:"confirmed_registration_count"`
	SellableCapacity           int                   `json:"sellable_capacity"`
	NeededToReachGroupMinimum  int                   `json:"needed_to_reach_group_minimum"`
}

func projectPublicHomeResponse(page PublicHomePage) PublicHomeResponse {
	catalog := page.Catalog
	result := PublicHomeResponse{
		CommunityName:                page.Profile.CommunityName,
		BrandIntro:                   page.Profile.BrandIntro,
		HeroMode:                     page.Profile.HeroMode,
		HeroEyebrow:                  page.Profile.HeroEyebrow,
		HeroSubtitle:                 page.Profile.HeroSubtitle,
		HeroImageURL:                 page.Profile.HeroImageURL,
		HeroImageAlt:                 page.Profile.HeroImageAlt,
		BrandStatus:                  page.Profile.LifecycleStatus,
		BrandPublicationVersion:      page.Profile.PublicationVersion,
		BrandPublishedAt:             formatPublicHomeTime(page.Profile.PublishedAt),
		AvailableQuickTags:           make([]PublicHomeQuickTagResponse, 0, len(catalog.AvailableQuickTags)),
		ActiveFilters:                projectPublicHomeFilter(catalog.ActiveFilter),
		BusinessTimezone:             catalog.BusinessTimezone,
		OpenRegistrationSessionCount: catalog.OpenRegistrationSessionCount,
		Cards:                        make([]PublicHomeCardResponse, 0, len(catalog.Cards)),
		AsOf:                         formatPublicHomeTime(catalog.AsOf),
		NextCursor:                   catalog.NextCursor,
	}
	for _, tag := range catalog.AvailableQuickTags {
		result.AvailableQuickTags = append(
			result.AvailableQuickTags,
			PublicHomeQuickTagResponse{Code: tag.Code, Label: tag.Label},
		)
	}
	for _, card := range catalog.Cards {
		quickTagCodes := append([]string(nil), card.QuickTagCodes...)
		if quickTagCodes == nil {
			quickTagCodes = []string{}
		}
		participantAvatars := append([]string(nil), card.ParticipantAvatars...)
		if participantAvatars == nil {
			participantAvatars = []string{}
		}
		favoriteAvatars := append([]string(nil), card.FavoriteAvatars...)
		if favoriteAvatars == nil {
			favoriteAvatars = []string{}
		}
		result.Cards = append(result.Cards, PublicHomeCardResponse{
			SeriesID:                card.SeriesID.String(),
			InstanceID:              card.InstanceID.String(),
			SessionID:               card.SessionID.String(),
			PublicationVersion:      card.PublicationVersion,
			InstanceTitle:           card.InstanceTitle,
			SessionTitle:            card.SessionTitle,
			ActivityType:            card.ActivityType,
			QuickTagCodes:           quickTagCodes,
			CoverImageURL:           card.CoverImageURL,
			ParticipantAvatars:      participantAvatars,
			FavoriteAvatars:         favoriteAvatars,
			SessionStartAt:          formatPublicHomeTime(card.SessionStartAt),
			SessionEndAt:            formatPublicHomeTime(card.SessionEndAt),
			PriceCents:              card.PriceCents,
			DeliveryMode:            card.DeliveryMode,
			Area:                    card.Area,
			VenueName:               card.VenueName,
			OnlineParticipationMode: card.OnlineParticipationMode,
			Display: PublicHomeDisplayResponse{
				State:                      card.Display.State,
				RegistrationAllowed:        card.Display.State.RegistrationAllowed(),
				Capacity:                   card.Display.Capacity,
				ConfirmedRegistrationCount: card.Display.ConfirmedRegistrationCount,
				SellableCapacity:           card.Display.SellableCapacity,
				NeededToReachGroupMinimum:  card.Display.NeededToReachGroupMinimum,
			},
			HomeGroup:            publicHomeGroup(card.HomeGroup),
			HeatCount:            card.HeatCount,
			CurrentFavoriteUsers: card.CurrentFavoriteUsers,
			CTAAction:            card.CTAAction,
			CTALabel:             card.CTALabel,
		})
	}
	if len(result.Cards) == 0 {
		result.EmptyState = "no_matching_sessions"
	}
	return result
}

func projectPublicHomeFilter(filter activity.HomeFilter) PublicHomeFilterResponse {
	quickTags := append([]string(nil), filter.QuickTags...)
	if quickTags == nil {
		quickTags = []string{}
	}
	return PublicHomeFilterResponse{
		ActivityType: filter.ActivityType,
		Area:         filter.Area,
		TimeWindow:   filter.TimeWindow,
		LocalDate:    filter.LocalDate,
		QuickTags:    quickTags,
		Limit:        filter.Limit,
	}
}

func publicHomeGroup(group activity.HomeGroup) string {
	switch group {
	case activity.HomeGroupOpen:
		return "open"
	case activity.HomeGroupFutureUnavailable:
		return "future_unavailable"
	case activity.HomeGroupInProgress:
		return "in_progress"
	case activity.HomeGroupRecurringGap:
		return "recurring_gap"
	case activity.HomeGroupEnded:
		return "ended"
	case activity.HomeGroupCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func formatPublicHomeTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
