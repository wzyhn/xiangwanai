package xiangwanapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	paymentpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxWeChatPrepayBodyBytes = 4 * 1024

type weChatPrepayApplication interface {
	Create(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		WeChatPrepayRequest,
	) (payment.PrepayAttemptResult, error)
}

type WeChatPrepayHandler struct {
	service   weChatPrepayApplication
	principal PrincipalResolver
}

func NewWeChatPrepayHandler(
	service weChatPrepayApplication,
	principal PrincipalResolver,
) *WeChatPrepayHandler {
	return &WeChatPrepayHandler{service: service, principal: principal}
}

func (handler *WeChatPrepayHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.POST(
		"/orders/:order_id/wechat-prepay-attempts",
		handler.CreateWeChatPrepayAttempt,
	)
}

// CreateWeChatPrepayAttempt godoc
// @Summary Create or replay one recoverable Xiangwan WeChat prepay attempt
// @Description OP-KEY command. Only the authenticated owner's pending positive-price Order with an active PostgreSQL Hold can acquire the single provider lease. AppID, merchant ID, out_trade_no, OpenID, amount, description, callback URL, and time_expire are server-owned. A provider timeout becomes confirmation-pending and never writes paid.
// @Tags xiangwan
// @Accept json
// @Produce json
// @Param order_id path string true "Order ID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body WeChatPrepayHTTPRequest true "Displayed Order confirmation"
// @Security BearerAuth
// @Success 200 {object} WeChatPrepayResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 413 {object} response.Body
// @Failure 500 {object} response.Body
// @Failure 503 {object} response.Body
// @Router /xiangwan/orders/{order_id}/wechat-prepay-attempts [post]
func (handler *WeChatPrepayHandler) CreateWeChatPrepayAttempt(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan WeChat prepay is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" {
		writeError(c, errx.NewBadRequest("invalid WeChat prepay request"))
		return
	}
	orderID, err := parseCanonicalUUID(c.Param("order_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Order id"))
		return
	}
	idempotencyKey, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request, err := decodeWeChatPrepayRequest(c)
	if err != nil {
		writeError(c, err)
		return
	}
	request.IdempotencyKey = idempotencyKey
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	result, err := handler.service.Create(
		c.Request.Context(),
		principalID,
		orderID,
		request,
	)
	if err != nil {
		writeWeChatPrepayError(c, err)
		return
	}
	response.OK(c, projectWeChatPrepayResponse(result))
}

type WeChatPrepayHTTPRequest struct {
	OrderVersion *int64 `json:"order_version" binding:"required"`
	PayableCents *int64 `json:"payable_cents" binding:"required"`
}

func decodeWeChatPrepayRequest(c *gin.Context) (WeChatPrepayRequest, error) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		maxWeChatPrepayBodyBytes,
	)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var payload WeChatPrepayHTTPRequest
	if err := decoder.Decode(&payload); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			return WeChatPrepayRequest{}, errx.New(
				errx.CodeFileTooLarge,
				"WeChat prepay request is too large",
			)
		}
		return WeChatPrepayRequest{}, errx.NewBadRequest(
			"invalid WeChat prepay request body",
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return WeChatPrepayRequest{}, errx.NewBadRequest(
			"invalid WeChat prepay request body",
		)
	}
	if payload.OrderVersion == nil || *payload.OrderVersion < 1 ||
		payload.PayableCents == nil || *payload.PayableCents <= 0 {
		return WeChatPrepayRequest{}, errx.NewBadRequest(
			"invalid WeChat prepay confirmation facts",
		)
	}
	return WeChatPrepayRequest{
		ExpectedOrderVersion: *payload.OrderVersion,
		ExpectedPayableCents: *payload.PayableCents,
	}, nil
}

func writeWeChatPrepayError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, payment.ErrPrepayRejected):
		writeError(c, errx.NewConflict(prepayRejectedCustomerMessage))
	case errors.Is(err, ErrInvalidWeChatPrepayRequest),
		errors.Is(err, payment.ErrInvalidPrepayAttempt):
		writeError(c, errx.NewBadRequest("invalid WeChat prepay request"))
	case errors.Is(err, paymentpostgres.ErrPrepayOrderNotFound):
		writeError(c, errx.NewNotFound("Order not found"))
	case errors.Is(err, ErrWeChatPrepayDisabled):
		writeError(c, errx.NewConflict("WeChat payment is unavailable"))
	case errors.Is(err, paymentpostgres.ErrPrepayOpenIDUnavailable):
		writeError(c, errx.NewConflict("WeChat payment identity is unavailable"))
	case errors.Is(err, paymentpostgres.ErrPrepayConfirmationConflict):
		writeError(c, errx.NewConflict("Order facts changed; review and retry"))
	case errors.Is(err, paymentpostgres.ErrPrepayIdempotencyConflict):
		writeError(c, errx.NewConflict("Idempotency-Key conflicts with another prepay request"))
	case errors.Is(err, paymentpostgres.ErrPrepayUnavailable),
		errors.Is(err, paymentpostgres.ErrPrepayTransactionConflict):
		writeError(c, errx.NewConflict("Order is not available for payment"))
	case errors.Is(err, paymentpostgres.ErrPrepayGenerationInactive):
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
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan WeChat prepay failed"))
	}
}

type WeChatPrepayResponse struct {
	AttemptID         string                                `json:"attempt_id"`
	OrderID           string                                `json:"order_id"`
	AttemptStatus     payment.PrepayAttemptStatus           `json:"attempt_status"`
	HoldExpiresAt     string                                `json:"hold_expires_at"`
	NextAction        string                                `json:"next_action"`
	PaymentParameters *MiniProgramPaymentParametersResponse `json:"payment_parameters,omitempty"`
}

type MiniProgramPaymentParametersResponse struct {
	TimeStamp string `json:"timeStamp"`
	NonceStr  string `json:"nonceStr"`
	Package   string `json:"package"`
	SignType  string `json:"signType"`
	PaySign   string `json:"paySign"`
}

func projectWeChatPrepayResponse(
	result payment.PrepayAttemptResult,
) WeChatPrepayResponse {
	projected := WeChatPrepayResponse{
		AttemptID:     result.Attempt.ID.String(),
		OrderID:       result.Attempt.OrderID.String(),
		AttemptStatus: result.Attempt.AttemptStatus,
		HoldExpiresAt: result.HoldExpiresAt.UTC().Format(time.RFC3339Nano),
		NextAction:    "payment_confirmation_pending",
	}
	if result.Parameters != nil &&
		result.Attempt.AttemptStatus == payment.PrepayAttemptStatusReady {
		projected.PaymentParameters = &MiniProgramPaymentParametersResponse{
			TimeStamp: result.Parameters.TimeStamp,
			NonceStr:  result.Parameters.NonceStr,
			Package:   result.Parameters.Package,
			SignType:  result.Parameters.SignType,
			PaySign:   result.Parameters.PaySign,
		}
		projected.NextAction = "invoke_wechat_payment"
	}
	return projected
}
