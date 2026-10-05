package xiangwanapi

import (
	"context"
	"errors"
	"strconv"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type myCouponsApplication interface {
	Read(
		context.Context,
		uuid.UUID,
		MyCouponsRequest,
	) (coupon.MyCouponsPage, error)
}

type MyCouponsHandler struct {
	service   myCouponsApplication
	principal PrincipalResolver
}

func NewMyCouponsHandler(
	service myCouponsApplication,
	principal PrincipalResolver,
) *MyCouponsHandler {
	return &MyCouponsHandler{service: service, principal: principal}
}

func (handler *MyCouponsHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/me/coupons", handler.GetMyCoupons)
}

// GetMyCoupons godoc
// @Summary List the authenticated consumer's Xiangwan Coupons
// @Description Returns only the token principal's Coupon value, validity, applicability, consumer state, active reservation links, and safe refund adjustment from PostgreSQL. Ledger evidence and operator identities are never exposed.
// @Tags xiangwan
// @Produce json
// @Param state query string false "Coupon view state" Enums(all, available, held, correction_required, expired, redeemed, invalidated)
// @Param cursor query string false "Opaque owner-bound stable cursor"
// @Param limit query int false "Page size (1..100)" minimum(1) maximum(100) default(20)
// @Security BearerAuth
// @Success 200 {object} MyCouponsResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/coupons [get]
func (handler *MyCouponsHandler) GetMyCoupons(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan My Coupons is unavailable"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request, err := parseMyCouponsRequest(c)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid My Coupons query"))
		return
	}
	page, err := handler.service.Read(c.Request.Context(), principalID, request)
	if err != nil {
		writeMyCouponsError(c, err)
		return
	}
	response.OK(c, projectMyCouponsResponse(page))
}

func parseMyCouponsRequest(c *gin.Context) (MyCouponsRequest, error) {
	query := c.Request.URL.Query()
	allowed := map[string]struct{}{
		"state":  {},
		"cursor": {},
		"limit":  {},
	}
	for key := range query {
		if _, exists := allowed[key]; !exists {
			return MyCouponsRequest{}, ErrInvalidMyCouponsRequest
		}
	}
	for _, key := range []string{"state", "cursor", "limit"} {
		if values, exists := query[key]; exists &&
			(len(values) != 1 || !canonicalHomeQueryValue(values[0])) {
			return MyCouponsRequest{}, ErrInvalidMyCouponsRequest
		}
	}
	request := MyCouponsRequest{}
	if values, exists := query["state"]; exists {
		request.State = coupon.MyCouponState(values[0])
		if !coupon.ValidMyCouponState(request.State) {
			return MyCouponsRequest{}, ErrInvalidMyCouponsRequest
		}
	}
	if values, exists := query["cursor"]; exists {
		if len(values[0]) > 2048 {
			return MyCouponsRequest{}, ErrInvalidMyCouponsRequest
		}
		request.Cursor = values[0]
	}
	if values, exists := query["limit"]; exists {
		limit, err := strconv.Atoi(values[0])
		if err != nil || strconv.Itoa(limit) != values[0] || limit < 1 ||
			limit > coupon.MaxMyCouponsLimit {
			return MyCouponsRequest{}, ErrInvalidMyCouponsRequest
		}
		request.Limit = limit
	}
	return request, nil
}

func writeMyCouponsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidMyCouponsRequest),
		errors.Is(err, couponpostgres.ErrInvalidMyCouponsFilter),
		errors.Is(err, couponpostgres.ErrInvalidMyCouponsCursor):
		writeError(c, errx.NewBadRequest("invalid My Coupons request"))
	case errors.Is(err, couponpostgres.ErrStaleMyCouponsCursor):
		writeError(c, errx.NewConflict("My Coupons changed; restart pagination"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan My Coupons failed"))
	}
}

type MyCouponsResponse struct {
	ActiveState coupon.MyCouponState `json:"active_state"`
	Items       []MyCouponResponse   `json:"items"`
	AsOf        string               `json:"as_of"`
	NextCursor  string               `json:"next_cursor,omitempty"`
	EmptyState  string               `json:"empty_state,omitempty"`
}

type MyCouponResponse struct {
	CouponID             string                        `json:"coupon_id"`
	FaceValueCents       int64                         `json:"face_value_cents"`
	Currency             string                        `json:"currency"`
	MinimumOrderCents    int64                         `json:"minimum_order_cents"`
	ValidFrom            string                        `json:"valid_from"`
	ExpiresAt            string                        `json:"expires_at"`
	GrantKind            coupon.GrantKind              `json:"grant_kind"`
	Applicability        MyCouponApplicabilityResponse `json:"applicability"`
	State                coupon.MyCouponState          `json:"state"`
	Usable               bool                          `json:"usable"`
	CorrectionRequired   bool                          `json:"correction_required"`
	ActiveOrderID        string                        `json:"active_order_id,omitempty"`
	ActiveRegistrationID string                        `json:"active_registration_id,omitempty"`
	LatestAdjustment     *MyCouponAdjustmentResponse   `json:"latest_adjustment,omitempty"`
}

type MyCouponApplicabilityResponse struct {
	ScopeType    coupon.ScopeType       `json:"scope_type"`
	ActivityType *activity.ActivityType `json:"activity_type,omitempty"`
	SeriesID     string                 `json:"series_id,omitempty"`
}

type MyCouponAdjustmentResponse struct {
	Disposition coupon.EntryType `json:"disposition"`
	OccurredAt  string           `json:"occurred_at"`
}

func projectMyCouponsResponse(page coupon.MyCouponsPage) MyCouponsResponse {
	result := MyCouponsResponse{
		ActiveState: page.ActiveState,
		Items:       make([]MyCouponResponse, 0, len(page.Items)),
		AsOf:        formatMyRegistrationTime(page.AsOf),
		NextCursor:  page.NextCursor,
	}
	for _, item := range page.Items {
		result.Items = append(result.Items, projectMyCouponResponse(item))
	}
	if len(result.Items) == 0 {
		result.EmptyState = "no_coupons"
	}
	return result
}

func projectMyCouponResponse(item coupon.MyCouponItem) MyCouponResponse {
	result := MyCouponResponse{
		CouponID:           item.CouponID.String(),
		FaceValueCents:     item.FaceValueCents,
		Currency:           item.Currency,
		MinimumOrderCents:  item.MinimumOrderCents,
		ValidFrom:          formatMyRegistrationTime(item.ValidFrom),
		ExpiresAt:          formatMyRegistrationTime(item.ExpiresAt),
		GrantKind:          item.GrantKind,
		State:              item.State,
		Usable:             item.Usable,
		CorrectionRequired: item.CorrectionRequired,
		Applicability: MyCouponApplicabilityResponse{
			ScopeType:    item.ApplicabilityTarget.ScopeType,
			ActivityType: item.ApplicabilityTarget.ActivityType,
		},
	}
	if item.ApplicabilityTarget.SeriesID != nil {
		result.Applicability.SeriesID = item.ApplicabilityTarget.SeriesID.String()
	}
	if item.ActiveOrderID != nil {
		result.ActiveOrderID = item.ActiveOrderID.String()
	}
	if item.ActiveRegistrationID != nil {
		result.ActiveRegistrationID = item.ActiveRegistrationID.String()
	}
	if item.LatestAdjustment != nil {
		result.LatestAdjustment = &MyCouponAdjustmentResponse{
			Disposition: item.LatestAdjustment.EntryType,
			OccurredAt: formatMyRegistrationTime(
				item.LatestAdjustment.OccurredAt,
			),
		}
	}
	return result
}
