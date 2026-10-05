package xiangwanapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type weChatPaymentQueryApplication interface {
	Query(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (payment.PaymentQueryResult, error)
}

type WeChatPaymentQueryHandler struct {
	service   weChatPaymentQueryApplication
	principal PrincipalResolver
}

func NewWeChatPaymentQueryHandler(
	service weChatPaymentQueryApplication,
	principal PrincipalResolver,
) *WeChatPaymentQueryHandler {
	return &WeChatPaymentQueryHandler{service: service, principal: principal}
}

func (handler *WeChatPaymentQueryHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST(
		"/orders/:order_id/payment-queries",
		handler.QueryWeChatPayment,
	)
}

// QueryWeChatPayment godoc
// @Summary Reconcile one owned Xiangwan Order from a trusted WeChat merchant query
// @Description BUSINESS-STATE/PROVIDER-KEY command. The authenticated client supplies no payment fact and no Idempotency-Key. PostgreSQL grants one short provider lease per Order. Only the signed and verified provider response can atomically confirm payment, finalize an authoritative unpaid terminal state, or create the unique late-payment refund case.
// @Tags xiangwan
// @Produce json
// @Param order_id path string true "Order ID"
// @Security BearerAuth
// @Success 200 {object} WeChatPaymentQueryResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 500 {object} response.Body
// @Failure 503 {object} response.Body
// @Router /xiangwan/orders/{order_id}/payment-queries [post]
func (handler *WeChatPaymentQueryHandler) QueryWeChatPayment(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan WeChat payment query is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.Request.ContentLength != 0 ||
		len(c.Request.TransferEncoding) != 0 ||
		!validEmptyPaymentQueryContentType(c.GetHeader("Content-Type")) ||
		len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid WeChat payment query"))
		return
	}
	orderID, err := parseCanonicalUUID(c.Param("order_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Order id"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Query(c.Request.Context(), principalID, orderID)
	if err != nil {
		writeWeChatPaymentQueryError(c, err)
		return
	}
	response.OK(c, projectWeChatPaymentQueryResponse(result))
}

func validEmptyPaymentQueryContentType(value string) bool {
	return value == "" || value == "application/json"
}

type WeChatPaymentQueryResponse struct {
	OrderID             string                                  `json:"order_id"`
	PaymentStatus       payment.OrderStatus                     `json:"payment_status"`
	QueryStatus         payment.PaymentQueryStatus              `json:"query_status"`
	ConfirmationPending bool                                    `json:"confirmation_pending"`
	RetryPaymentAllowed bool                                    `json:"retry_payment_allowed"`
	NextAction          string                                  `json:"next_action"`
	Disposition         *payment.PaymentConfirmationDisposition `json:"disposition,omitempty"`
	NextQueryAt         *string                                 `json:"next_query_at,omitempty"`
}

func projectWeChatPaymentQueryResponse(
	result payment.PaymentQueryResult,
) WeChatPaymentQueryResponse {
	projected := WeChatPaymentQueryResponse{
		OrderID:             result.Order.ID.String(),
		PaymentStatus:       result.Order.PaymentStatus,
		QueryStatus:         result.QueryStatus,
		RetryPaymentAllowed: result.RetryPaymentAllowed,
		ConfirmationPending: result.Order.PaymentStatus == payment.OrderStatusPending ||
			result.Order.PaymentStatus == payment.OrderStatusUnknown,
		NextAction: "retry_payment_query",
	}
	if result.NextQueryAt != nil {
		formatted := result.NextQueryAt.UTC().Format(time.RFC3339Nano)
		projected.NextQueryAt = &formatted
	}
	if result.Disposition != payment.PaymentConfirmationDispositionNone {
		disposition := result.Disposition
		projected.Disposition = &disposition
	}
	switch {
	case result.Disposition == payment.PaymentConfirmationDispositionRefundRequired:
		projected.NextAction = "refund_processing"
	case result.Order.PaymentStatus == payment.OrderStatusPaidConfirmed:
		projected.NextAction = "payment_confirmed"
	case result.Order.PaymentStatus == payment.OrderStatusClosedUnpaid:
		projected.NextAction = "payment_closed"
	case result.QueryStatus == payment.PaymentQueryStatusInProgress:
		projected.NextAction = "wait_for_payment_confirmation"
	}
	return projected
}

func writeWeChatPaymentQueryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidWeChatPaymentQuery),
		errors.Is(err, payment.ErrInvalidPaymentQuery):
		writeError(c, errx.NewBadRequest("invalid WeChat payment query"))
	case errors.Is(err, paymentpostgres.ErrPaymentQueryOrderNotFound):
		writeError(c, errx.NewNotFound("Order not found"))
	case errors.Is(err, ErrWeChatPaymentQueryDisabled):
		writeError(c, errx.NewConflict("WeChat payment is unavailable"))
	case errors.Is(err, paymentpostgres.ErrPaymentQueryUnavailable):
		writeError(c, errx.NewConflict("Order is not available for payment confirmation"))
	case errors.Is(err, paymentpostgres.ErrPaymentQueryGenerationInactive):
		_ = c.Error(err)
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	case errors.Is(err, paymentpostgres.ErrPaymentMerchantConfigUnavailable):
		_ = c.Error(err)
		c.PureJSON(http.StatusServiceUnavailable, response.Body{
			Code:    int(errx.CodeInternal),
			Message: "service unavailable",
		})
	case errors.Is(err, paymentpostgres.ErrPaymentConfirmationConflict),
		errors.Is(err, paymentpostgres.ErrInvalidPaymentConfirmationCommand),
		errors.Is(err, paymentpostgres.ErrPaymentConfirmationTransaction),
		errors.Is(err, paymentpostgres.ErrPaymentQueryTransactionConflict),
		errors.Is(err, payment.ErrPaymentQueryLeaseLost):
		writeError(c, errx.NewConflict("payment confirmation requires retry"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan WeChat payment query failed"))
	}
}
