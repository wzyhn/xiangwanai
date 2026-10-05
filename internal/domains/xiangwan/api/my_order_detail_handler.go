package xiangwanapi

import (
	"context"
	"errors"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type myOrderDetailApplication interface {
	Read(
		context.Context,
		uuid.UUID,
		uuid.UUID,
	) (booking.MyOrderDetail, error)
}

type MyOrderDetailHandler struct {
	service   myOrderDetailApplication
	principal PrincipalResolver
}

func NewMyOrderDetailHandler(
	service myOrderDetailApplication,
	principal PrincipalResolver,
) *MyOrderDetailHandler {
	return &MyOrderDetailHandler{service: service, principal: principal}
}

func (handler *MyOrderDetailHandler) RegisterRoutes(group *gin.RouterGroup) {
	group.GET("/me/orders/:order_id", handler.GetMyOrderDetail)
}

// GetMyOrderDetail godoc
// @Summary Read one authenticated consumer Xiangwan order
// @Description Returns one exact owner-fenced Order with its Registration, Session, hold, payment-confirmation, and Refund facts.
// @Tags xiangwan
// @Produce json
// @Param order_id path string true "Order ID"
// @Security BearerAuth
// @Success 200 {object} MyOrderDetailResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 500 {object} response.Body
// @Router /xiangwan/me/orders/{order_id} [get]
func (handler *MyOrderDetailHandler) GetMyOrderDetail(c *gin.Context) {
	if handler == nil || handler.service == nil || handler.principal == nil {
		writeError(c, errx.NewInternal("xiangwan My Order detail is unavailable"))
		return
	}
	if len(c.Request.URL.Query()) != 0 {
		writeError(c, errx.NewBadRequest("invalid My Order detail query"))
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
	detail, err := handler.service.Read(
		c.Request.Context(),
		principalID,
		orderID,
	)
	if err != nil {
		writeMyOrderDetailError(c, err)
		return
	}
	response.OK(c, projectMyOrderDetailResponse(detail))
}

func writeMyOrderDetailError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidMyOrderDetailRequest),
		errors.Is(err, booking.ErrInvalidMyOrderIdentity):
		writeError(c, errx.NewBadRequest("invalid My Order detail request"))
	case errors.Is(err, booking.ErrMyOrderNotFound):
		writeError(c, errx.NewNotFound("Order not found"))
	default:
		_ = c.Error(err)
		writeError(c, errx.NewInternal("xiangwan My Order detail failed"))
	}
}

type MyOrderDetailResponse struct {
	Order MyOrderItemResponse `json:"order"`
	AsOf  string              `json:"as_of"`
}

func projectMyOrderDetailResponse(
	detail booking.MyOrderDetail,
) MyOrderDetailResponse {
	return MyOrderDetailResponse{
		Order: projectMyOrderItem(detail.Item),
		AsOf:  formatMyRegistrationTime(detail.AsOf),
	}
}
