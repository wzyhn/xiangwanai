package xiangwanapi

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type myRegistrationsApplication interface {
	Read(
		context.Context,
		uuid.UUID,
		MyRegistrationsRequest,
	) (booking.MyRegistrationsPage, error)
}

type PrincipalResolver func(*gin.Context) (uuid.UUID, error)

type MyRegistrationsHandler struct {
	service   myRegistrationsApplication
	principal PrincipalResolver
}

func NewMyRegistrationsHandler(
	service myRegistrationsApplication,
	principal PrincipalResolver,
) *MyRegistrationsHandler {
	return &MyRegistrationsHandler{service: service, principal: principal}
}

func (handler *MyRegistrationsHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/me/registrations", handler.GetMyRegistrations)
}

// GetMyRegistrations godoc
// @Summary List the authenticated consumer's Xiangwan registrations
// @Description Returns only the token principal's exact Registration, Session, payment, Refund, and check-in facts from PostgreSQL.
// @Tags xiangwan
// @Produce json
// @Param state query string false "Registration view state" Enums(all, pending_payment, registered, cancelled, refund_processing, refunded, ended)
// @Param cursor query string false "Opaque owner-bound stable cursor"
// @Param limit query int false "Page size (1..100)" minimum(1) maximum(100) default(20)
// @Security BearerAuth
// @Success 200 {object} MyRegistrationsResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/registrations [get]
func (handler *MyRegistrationsHandler) GetMyRegistrations(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan My Registrations is unavailable"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request, err := parseMyRegistrationsRequest(c)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid My Registrations query"))
		return
	}
	page, err := handler.service.Read(
		c.Request.Context(),
		principalID,
		request,
	)
	if err != nil {
		writeMyRegistrationsError(c, err)
		return
	}
	response.OK(c, projectMyRegistrationsResponse(page))
}

func parseMyRegistrationsRequest(
	c *gin.Context,
) (MyRegistrationsRequest, error) {
	query := c.Request.URL.Query()
	allowed := map[string]struct{}{
		"state":  {},
		"cursor": {},
		"limit":  {},
	}
	for key := range query {
		if _, exists := allowed[key]; !exists {
			return MyRegistrationsRequest{}, ErrInvalidMyRegistrationsRequest
		}
	}
	for _, key := range []string{"state", "cursor", "limit"} {
		if values, exists := query[key]; exists &&
			(len(values) != 1 || !canonicalHomeQueryValue(values[0])) {
			return MyRegistrationsRequest{}, ErrInvalidMyRegistrationsRequest
		}
	}
	request := MyRegistrationsRequest{}
	if values, exists := query["state"]; exists {
		request.State = booking.MyRegistrationState(values[0])
		if !booking.ValidMyRegistrationState(request.State) {
			return MyRegistrationsRequest{}, ErrInvalidMyRegistrationsRequest
		}
	}
	if values, exists := query["cursor"]; exists {
		if len(values[0]) > 2048 {
			return MyRegistrationsRequest{}, ErrInvalidMyRegistrationsRequest
		}
		request.Cursor = values[0]
	}
	if values, exists := query["limit"]; exists {
		limit, err := strconv.Atoi(values[0])
		if err != nil || strconv.Itoa(limit) != values[0] || limit < 1 ||
			limit > booking.MaxMyRegistrationsLimit {
			return MyRegistrationsRequest{}, ErrInvalidMyRegistrationsRequest
		}
		request.Limit = limit
	}
	return request, nil
}

func writeMyRegistrationsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidMyRegistrationsRequest),
		errors.Is(err, booking.ErrInvalidMyRegistrationsFilter),
		errors.Is(err, booking.ErrInvalidMyRegistrationsCursor):
		writeError(c, errx.NewBadRequest("invalid My Registrations request"))
	case errors.Is(err, booking.ErrStaleMyRegistrationsCursor):
		writeError(c, errx.NewConflict("My Registrations changed; restart pagination"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan My Registrations failed"))
	}
}

type MyRegistrationsResponse struct {
	ActiveState booking.MyRegistrationState  `json:"active_state"`
	Items       []MyRegistrationItemResponse `json:"items"`
	AsOf        string                       `json:"as_of"`
	NextCursor  string                       `json:"next_cursor,omitempty"`
	EmptyState  string                       `json:"empty_state,omitempty"`
}

type MyRegistrationItemResponse struct {
	RegistrationID      string                           `json:"registration_id"`
	RegistrationVersion int64                            `json:"registration_version"`
	SeriesID            string                           `json:"series_id"`
	SeriesTitle         string                           `json:"series_title"`
	InstanceID          string                           `json:"instance_id"`
	InstanceTitle       string                           `json:"instance_title"`
	InstanceStatus      activity.InstanceStatus          `json:"instance_status"`
	SessionID           string                           `json:"session_id"`
	SessionTitle        string                           `json:"session_title"`
	SessionStatus       activity.SessionStatus           `json:"session_status"`
	SessionStartAt      string                           `json:"session_start_at"`
	SessionEndAt        string                           `json:"session_end_at"`
	DeliveryMode        activity.DeliveryMode            `json:"delivery_mode"`
	Area                string                           `json:"area,omitempty"`
	VenueName           string                           `json:"venue_name,omitempty"`
	Address             string                           `json:"address,omitempty"`
	OnlineMode          string                           `json:"online_mode,omitempty"`
	ParticipationStatus registration.ParticipationStatus `json:"participation_status"`
	ConfirmedAt         string                           `json:"confirmed_at,omitempty"`
	CancelledAt         string                           `json:"cancelled_at,omitempty"`
	CancellationReason  string                           `json:"cancellation_reason,omitempty"`
	Order               *MyRegistrationOrderResponse     `json:"order,omitempty"`
	Refund              *MyRegistrationRefundResponse    `json:"refund,omitempty"`
	Checkin             MyRegistrationCheckinResponse    `json:"checkin"`
	State               booking.MyRegistrationState      `json:"state"`
	HasActiveAccess     bool                             `json:"has_active_access"`
	CanContinuePayment  bool                             `json:"can_continue_payment"`
	LastBusinessAt      string                           `json:"last_business_at"`
}

type MyRegistrationOrderResponse struct {
	OrderID            string                     `json:"order_id"`
	PaymentStatus      payment.OrderStatus        `json:"payment_status"`
	OriginalPriceCents int64                      `json:"original_price_cents"`
	DiscountCents      int64                      `json:"discount_cents"`
	PayableCents       int64                      `json:"payable_cents"`
	ActualPaidCents    *int64                     `json:"actual_paid_cents,omitempty"`
	PaidAt             string                     `json:"paid_at,omitempty"`
	ClosedAt           string                     `json:"closed_at,omitempty"`
	HoldStatus         payment.CapacityHoldStatus `json:"hold_status"`
	HoldExpiresAt      string                     `json:"hold_expires_at"`
	HoldVersion        int64                      `json:"hold_version"`
	HoldUpdatedAt      string                     `json:"hold_updated_at"`
	Version            int64                      `json:"version"`
	UpdatedAt          string                     `json:"updated_at"`
}

type MyRegistrationRefundResponse struct {
	RefundCaseID          string            `json:"refund_case_id"`
	RefundStatus          refund.Status     `json:"refund_status"`
	ReasonCode            refund.ReasonCode `json:"reason_code"`
	RequestedRefundCents  int64             `json:"requested_refund_cents"`
	SuccessfulRefundCents int64             `json:"successful_refund_cents"`
	ResolvedAt            string            `json:"resolved_at,omitempty"`
	Version               int64             `json:"version"`
	UpdatedAt             string            `json:"updated_at"`
}

type MyRegistrationCheckinResponse struct {
	Status      booking.CheckinStatus `json:"status"`
	CheckedInAt string                `json:"checked_in_at,omitempty"`
	RevokedAt   string                `json:"revoked_at,omitempty"`
}

func projectMyRegistrationsResponse(
	page booking.MyRegistrationsPage,
) MyRegistrationsResponse {
	result := MyRegistrationsResponse{
		ActiveState: page.ActiveState,
		Items:       make([]MyRegistrationItemResponse, 0, len(page.Items)),
		AsOf:        formatMyRegistrationTime(page.AsOf),
		NextCursor:  page.NextCursor,
	}
	for _, item := range page.Items {
		result.Items = append(result.Items, projectMyRegistrationItem(item))
	}
	if len(result.Items) == 0 {
		result.EmptyState = "no_registrations"
	}
	return result
}

func projectMyRegistrationItem(
	item booking.MyRegistrationItem,
) MyRegistrationItemResponse {
	result := MyRegistrationItemResponse{
		RegistrationID:      item.RegistrationID.String(),
		RegistrationVersion: item.RegistrationVersion,
		SeriesID:            item.SeriesID.String(),
		SeriesTitle:         item.SeriesTitle,
		InstanceID:          item.InstanceID.String(),
		InstanceTitle:       item.InstanceTitle,
		InstanceStatus:      item.InstanceStatus,
		SessionID:           item.SessionID.String(),
		SessionTitle:        item.SessionTitle,
		SessionStatus:       item.SessionStatus,
		SessionStartAt:      formatMyRegistrationTime(item.SessionStartAt),
		SessionEndAt:        formatMyRegistrationTime(item.SessionEndAt),
		DeliveryMode:        item.DeliveryMode,
		Area:                myRegistrationArea(item.Area),
		VenueName:           myRegistrationString(item.VenueName),
		Address:             myRegistrationString(item.Address),
		OnlineMode:          myRegistrationString(item.OnlineMode),
		ParticipationStatus: item.ParticipationStatus,
		ConfirmedAt:         formatMyRegistrationTimePointer(item.ConfirmedAt),
		CancelledAt:         formatMyRegistrationTimePointer(item.CancelledAt),
		CancellationReason:  myRegistrationString(item.CancellationReason),
		Checkin:             projectMyRegistrationCheckin(item.Checkin),
		State:               item.State,
		HasActiveAccess:     item.HasActiveAccess,
		CanContinuePayment:  item.CanContinuePayment,
		LastBusinessAt:      formatMyRegistrationTime(item.LastBusinessAt),
	}
	if item.Order != nil {
		result.Order = &MyRegistrationOrderResponse{
			OrderID:            item.Order.OrderID.String(),
			PaymentStatus:      item.Order.PaymentStatus,
			OriginalPriceCents: item.Order.OriginalPriceCents,
			DiscountCents:      item.Order.DiscountCents,
			PayableCents:       item.Order.PayableCents,
			ActualPaidCents:    item.Order.ActualPaidCents,
			PaidAt:             formatMyRegistrationTimePointer(item.Order.PaidAt),
			ClosedAt:           formatMyRegistrationTimePointer(item.Order.ClosedAt),
			HoldStatus:         item.Order.HoldStatus,
			HoldExpiresAt:      formatMyRegistrationTime(item.Order.HoldExpiresAt),
			HoldVersion:        item.Order.HoldVersion,
			HoldUpdatedAt:      formatMyRegistrationTime(item.Order.HoldUpdatedAt),
			Version:            item.Order.Version,
			UpdatedAt:          formatMyRegistrationTime(item.Order.UpdatedAt),
		}
	}
	if item.Refund != nil {
		result.Refund = &MyRegistrationRefundResponse{
			RefundCaseID:          item.Refund.RefundCaseID.String(),
			RefundStatus:          item.Refund.RefundStatus,
			ReasonCode:            item.Refund.ReasonCode,
			RequestedRefundCents:  item.Refund.RequestedRefundCents,
			SuccessfulRefundCents: item.Refund.SuccessfulRefundCents,
			ResolvedAt:            formatMyRegistrationTimePointer(item.Refund.ResolvedAt),
			Version:               item.Refund.Version,
			UpdatedAt:             formatMyRegistrationTime(item.Refund.UpdatedAt),
		}
	}
	return result
}

func projectMyRegistrationCheckin(
	checkin booking.CheckinSummary,
) MyRegistrationCheckinResponse {
	return MyRegistrationCheckinResponse{
		Status:      checkin.Status,
		CheckedInAt: formatMyRegistrationTimePointer(checkin.CheckedInAt),
		RevokedAt:   formatMyRegistrationTimePointer(checkin.RevokedAt),
	}
}

func formatMyRegistrationTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatMyRegistrationTimePointer(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatMyRegistrationTime(*value)
}

func myRegistrationString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func myRegistrationArea(value *activity.AreaCode) string {
	if value == nil {
		return ""
	}
	return string(*value)
}
