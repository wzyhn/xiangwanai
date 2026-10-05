package xiangwanapi

import (
	"context"
	"errors"
	"strconv"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type myOrdersApplication interface {
	Read(
		context.Context,
		uuid.UUID,
		MyOrdersRequest,
	) (booking.MyOrdersPage, error)
}

type MyOrdersHandler struct {
	service   myOrdersApplication
	principal PrincipalResolver
}

func NewMyOrdersHandler(
	service myOrdersApplication,
	principal PrincipalResolver,
) *MyOrdersHandler {
	return &MyOrdersHandler{service: service, principal: principal}
}

func (handler *MyOrdersHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/me/orders", handler.GetMyOrders)
}

// GetMyOrders godoc
// @Summary List the authenticated consumer's Xiangwan orders
// @Description Returns only the token principal's exact Order, Registration, Session, hold, payment-confirmation, and Refund facts from PostgreSQL.
// @Tags xiangwan
// @Produce json
// @Param state query string false "Order view state" Enums(all, pending_payment, refund_processing, paid, refunded, closed)
// @Param cursor query string false "Opaque owner-bound stable cursor"
// @Param limit query int false "Page size (1..100)" minimum(1) maximum(100) default(20)
// @Security BearerAuth
// @Success 200 {object} MyOrdersResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/orders [get]
func (handler *MyOrdersHandler) GetMyOrders(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan My Orders is unavailable"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request, err := parseMyOrdersRequest(c)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid My Orders query"))
		return
	}
	page, err := handler.service.Read(
		c.Request.Context(),
		principalID,
		request,
	)
	if err != nil {
		writeMyOrdersError(c, err)
		return
	}
	response.OK(c, projectMyOrdersResponse(page))
}

func parseMyOrdersRequest(c *gin.Context) (MyOrdersRequest, error) {
	query := c.Request.URL.Query()
	allowed := map[string]struct{}{
		"state":  {},
		"cursor": {},
		"limit":  {},
	}
	for key := range query {
		if _, exists := allowed[key]; !exists {
			return MyOrdersRequest{}, ErrInvalidMyOrdersRequest
		}
	}
	for _, key := range []string{"state", "cursor", "limit"} {
		if values, exists := query[key]; exists &&
			(len(values) != 1 || !canonicalHomeQueryValue(values[0])) {
			return MyOrdersRequest{}, ErrInvalidMyOrdersRequest
		}
	}
	request := MyOrdersRequest{}
	if values, exists := query["state"]; exists {
		request.State = booking.MyOrderState(values[0])
		if !booking.ValidMyOrderState(request.State) {
			return MyOrdersRequest{}, ErrInvalidMyOrdersRequest
		}
	}
	if values, exists := query["cursor"]; exists {
		if len(values[0]) > 2048 {
			return MyOrdersRequest{}, ErrInvalidMyOrdersRequest
		}
		request.Cursor = values[0]
	}
	if values, exists := query["limit"]; exists {
		limit, err := strconv.Atoi(values[0])
		if err != nil || strconv.Itoa(limit) != values[0] || limit < 1 ||
			limit > booking.MaxMyOrdersLimit {
			return MyOrdersRequest{}, ErrInvalidMyOrdersRequest
		}
		request.Limit = limit
	}
	return request, nil
}

func writeMyOrdersError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidMyOrdersRequest),
		errors.Is(err, booking.ErrInvalidMyOrdersFilter),
		errors.Is(err, booking.ErrInvalidMyOrdersCursor):
		writeError(c, errx.NewBadRequest("invalid My Orders request"))
	case errors.Is(err, booking.ErrStaleMyOrdersCursor):
		writeError(c, errx.NewConflict("My Orders changed; restart pagination"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan My Orders failed"))
	}
}

type MyOrdersResponse struct {
	ActiveState booking.MyOrderState  `json:"active_state"`
	Items       []MyOrderItemResponse `json:"items"`
	AsOf        string                `json:"as_of"`
	NextCursor  string                `json:"next_cursor,omitempty"`
	EmptyState  string                `json:"empty_state,omitempty"`
}

type MyOrderItemResponse struct {
	OrderID                    string                           `json:"order_id"`
	OrderVersion               int64                            `json:"order_version"`
	RegistrationID             string                           `json:"registration_id"`
	RegistrationVersion        int64                            `json:"registration_version"`
	SeriesID                   string                           `json:"series_id"`
	SeriesTitle                string                           `json:"series_title"`
	InstanceID                 string                           `json:"instance_id"`
	InstanceTitle              string                           `json:"instance_title"`
	SessionID                  string                           `json:"session_id"`
	SessionTitle               string                           `json:"session_title"`
	SessionStartAt             string                           `json:"session_start_at"`
	SessionEndAt               string                           `json:"session_end_at"`
	ParticipationStatus        registration.ParticipationStatus `json:"participation_status"`
	ReservationState           booking.MyRegistrationState      `json:"reservation_state"`
	ReservationHasActiveAccess bool                             `json:"reservation_has_active_access"`
	PaymentStatus              payment.OrderStatus              `json:"payment_status"`
	PaymentConfirmationPending bool                             `json:"payment_confirmation_pending"`
	OriginalPriceCents         int64                            `json:"original_price_cents"`
	DiscountCents              int64                            `json:"discount_cents"`
	PayableCents               int64                            `json:"payable_cents"`
	ActualPaidCents            *int64                           `json:"actual_paid_cents,omitempty"`
	Currency                   string                           `json:"currency"`
	PaidAt                     string                           `json:"paid_at,omitempty"`
	ClosedAt                   string                           `json:"closed_at,omitempty"`
	HoldStatus                 payment.CapacityHoldStatus       `json:"hold_status"`
	HoldExpiresAt              string                           `json:"hold_expires_at"`
	HoldVersion                int64                            `json:"hold_version"`
	CanContinuePayment         bool                             `json:"can_continue_payment"`
	Refund                     *MyRegistrationRefundResponse    `json:"refund,omitempty"`
	State                      booking.MyOrderState             `json:"state"`
	Outcome                    booking.MyOrderOutcome           `json:"outcome"`
	LastBusinessAt             string                           `json:"last_business_at"`
}

func projectMyOrdersResponse(page booking.MyOrdersPage) MyOrdersResponse {
	result := MyOrdersResponse{
		ActiveState: page.ActiveState,
		Items:       make([]MyOrderItemResponse, 0, len(page.Items)),
		AsOf:        formatMyRegistrationTime(page.AsOf),
		NextCursor:  page.NextCursor,
	}
	for _, item := range page.Items {
		result.Items = append(result.Items, projectMyOrderItem(item))
	}
	if len(result.Items) == 0 {
		result.EmptyState = "no_orders"
	}
	return result
}

func projectMyOrderItem(item booking.MyOrderItem) MyOrderItemResponse {
	return MyOrderItemResponse{
		OrderID:                    item.OrderID.String(),
		OrderVersion:               item.OrderVersion,
		RegistrationID:             item.RegistrationID.String(),
		RegistrationVersion:        item.RegistrationVersion,
		SeriesID:                   item.SeriesID.String(),
		SeriesTitle:                item.SeriesTitle,
		InstanceID:                 item.InstanceID.String(),
		InstanceTitle:              item.InstanceTitle,
		SessionID:                  item.SessionID.String(),
		SessionTitle:               item.SessionTitle,
		SessionStartAt:             formatMyRegistrationTime(item.SessionStartAt),
		SessionEndAt:               formatMyRegistrationTime(item.SessionEndAt),
		ParticipationStatus:        item.ParticipationStatus,
		ReservationState:           item.ReservationState,
		ReservationHasActiveAccess: item.ReservationHasActiveAccess,
		PaymentStatus:              item.PaymentStatus,
		PaymentConfirmationPending: item.PaymentConfirmationPending,
		OriginalPriceCents:         item.OriginalPriceCents,
		DiscountCents:              item.DiscountCents,
		PayableCents:               item.PayableCents,
		ActualPaidCents:            item.ActualPaidCents,
		Currency:                   item.Currency,
		PaidAt:                     formatMyRegistrationTimePointer(item.PaidAt),
		ClosedAt:                   formatMyRegistrationTimePointer(item.ClosedAt),
		HoldStatus:                 item.HoldStatus,
		HoldExpiresAt:              formatMyRegistrationTime(item.HoldExpiresAt),
		HoldVersion:                item.HoldVersion,
		CanContinuePayment:         item.CanContinuePayment,
		Refund:                     projectMyOrderRefund(item.Refund),
		State:                      item.State,
		Outcome:                    item.Outcome,
		LastBusinessAt:             formatMyRegistrationTime(item.LastBusinessAt),
	}
}

func projectMyOrderRefund(
	value *booking.MyRegistrationRefundSummary,
) *MyRegistrationRefundResponse {
	if value == nil {
		return nil
	}
	return &MyRegistrationRefundResponse{
		RefundCaseID:          value.RefundCaseID.String(),
		RefundStatus:          value.RefundStatus,
		ReasonCode:            value.ReasonCode,
		RequestedRefundCents:  value.RequestedRefundCents,
		SuccessfulRefundCents: value.SuccessfulRefundCents,
		ResolvedAt:            formatMyRegistrationTimePointer(value.ResolvedAt),
		Version:               value.Version,
		UpdatedAt:             formatMyRegistrationTime(value.UpdatedAt),
	}
}
