package xiangwanapi

import (
	"fmt"
	"strconv"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListOrders godoc
// @Summary List Xiangwan administrator Order facts
// @Description Requires a live tenant activity-operator, finance, or super-admin Grant. Returns immutable price amounts, independent payment/Refund states and activity routing without provider identities or contact details. An audited read commits before the response.
// @Tags xiangwan-admin
// @Produce json
// @Param status query string false "Order payment status or all" Enums(all,pending,unknown,paid_confirmed,settled_zero,closed_unpaid)
// @Param limit query int false "Page size" minimum(1) maximum(100)
// @Param cursor query string false "Opaque tenant/status-bound next cursor"
// @Success 200 {object} AdminOrderListPageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/orders [get]
func (handler *AdminCatalogHandler) ListOrders(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c, "status", "limit", "cursor") {
		return
	}
	catalog, ok := handler.catalog.(xiangwanadmin.OrderListCatalog)
	if !ok {
		writeError(c, fmt.Errorf("administrator Order catalog is unavailable"))
		return
	}
	status := c.Query("status")
	switch status {
	case "", "all", string(payment.OrderStatusPending), string(payment.OrderStatusUnknown),
		string(payment.OrderStatusPaidConfirmed), string(payment.OrderStatusSettledZero),
		string(payment.OrderStatusClosedUnpaid):
	default:
		writeError(c, errx.NewBadRequest("invalid Order payment status"))
		return
	}
	limit, ok := parseOptionalAdminInt(c, "limit")
	if !ok {
		return
	}
	if limit > xiangwanadmin.MaxOrderListLimit || len(c.Query("cursor")) > 1024 {
		writeError(c, errx.NewBadRequest("invalid Order pagination"))
		return
	}
	value, err := catalog.ListOrders(c.Request.Context(), principal,
		xiangwanadmin.OrderListFilter{Status: status, Limit: limit, Cursor: c.Query("cursor")})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminOrderListItemResponse, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, projectAdminOrderItem(item))
	}
	response.OK(c, AdminOrderListPageResponse{
		Items: items, Status: value.Status, NextCursor: value.NextCursor,
	})
}

// GetOrder godoc
// @Summary Read one Xiangwan administrator Order
// @Description Requires a live tenant activity-operator, finance, or super-admin Grant. Returns exact immutable price and current payment/Refund status facts without provider identities or contact details. An audited read commits before the response.
// @Tags xiangwan-admin
// @Produce json
// @Param order_id path string true "Order UUID"
// @Success 200 {object} AdminOrderListItemResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/orders/{order_id} [get]
func (handler *AdminCatalogHandler) GetOrder(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	catalog, ok := handler.catalog.(xiangwanadmin.OrderDetailCatalog)
	if !ok {
		writeError(c, fmt.Errorf("administrator Order detail catalog is unavailable"))
		return
	}
	orderID, err := uuid.Parse(c.Param("order_id"))
	if err != nil || orderID == uuid.Nil {
		writeError(c, errx.NewBadRequest("invalid Order ID"))
		return
	}
	value, err := catalog.GetOrder(c.Request.Context(), principal, orderID)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, projectAdminOrderItem(value))
}

func projectAdminOrderItem(item xiangwanadmin.OrderListItem) AdminOrderListItemResponse {
	var actualPaid *string
	if item.ActualPaidCents != nil {
		amount := strconv.FormatInt(*item.ActualPaidCents, 10)
		actualPaid = &amount
	}
	var refundCaseID *string
	if item.RefundCaseID != nil {
		id := item.RefundCaseID.String()
		refundCaseID = &id
	}
	var paidAt, closedAt *string
	if item.PaidAt != nil {
		value := item.PaidAt.UTC().Format(time.RFC3339Nano)
		paidAt = &value
	}
	if item.ClosedAt != nil {
		value := item.ClosedAt.UTC().Format(time.RFC3339Nano)
		closedAt = &value
	}
	return AdminOrderListItemResponse{
		OrderID: item.OrderID.String(), RegistrationID: item.RegistrationID.String(),
		InstanceID: item.InstanceID.String(), SessionID: item.SessionID.String(),
		SeriesTitle: item.SeriesTitle, InstanceTitle: item.InstanceTitle,
		SessionTitle: item.SessionTitle, PaymentStatus: string(item.PaymentStatus),
		OriginalPriceCents: strconv.FormatInt(item.OriginalPriceCents, 10),
		DiscountCents:      strconv.FormatInt(item.DiscountCents, 10),
		PayableCents:       strconv.FormatInt(item.PayableCents, 10),
		ActualPaidCents:    actualPaid, RefundCaseID: refundCaseID,
		RefundStatus:          string(item.RefundStatus),
		RequestedRefundCents:  strconv.FormatInt(item.RequestedRefundCents, 10),
		SuccessfulRefundCents: strconv.FormatInt(item.SuccessfulRefundCents, 10),
		CreatedAt:             item.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:             item.UpdatedAt.UTC().Format(time.RFC3339Nano),
		PaidAt:                paidAt, ClosedAt: closedAt,
	}
}

type AdminOrderListItemResponse struct {
	OrderID               string  `json:"order_id"`
	RegistrationID        string  `json:"registration_id"`
	InstanceID            string  `json:"instance_id"`
	SessionID             string  `json:"session_id"`
	SeriesTitle           string  `json:"series_title"`
	InstanceTitle         string  `json:"instance_title"`
	SessionTitle          string  `json:"session_title"`
	PaymentStatus         string  `json:"payment_status"`
	OriginalPriceCents    string  `json:"original_price_cents"`
	DiscountCents         string  `json:"discount_cents"`
	PayableCents          string  `json:"payable_cents"`
	ActualPaidCents       *string `json:"actual_paid_cents"`
	RefundCaseID          *string `json:"refund_case_id"`
	RefundStatus          string  `json:"refund_status"`
	RequestedRefundCents  string  `json:"requested_refund_cents"`
	SuccessfulRefundCents string  `json:"successful_refund_cents"`
	CreatedAt             string  `json:"created_at"`
	UpdatedAt             string  `json:"updated_at"`
	PaidAt                *string `json:"paid_at"`
	ClosedAt              *string `json:"closed_at"`
}

type AdminOrderListPageResponse struct {
	Items      []AdminOrderListItemResponse `json:"items"`
	Status     string                       `json:"status"`
	NextCursor string                       `json:"next_cursor"`
}
