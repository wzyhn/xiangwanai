package xiangwanapi

import (
	"fmt"
	"strconv"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ListRefundQueue godoc
// @Summary List actionable Xiangwan manual Refund cases
// @Description Requires a live tenant finance or super-admin Grant. Returns only case routing, amount and status metadata for one status tab. A content-free read audit commits before the response.
// @Tags xiangwan-admin
// @Produce json
// @Param status query string false "Actionable Refund status" Enums(pending_manual,processing,failed)
// @Param limit query int false "Page size" minimum(1) maximum(100)
// @Param cursor query string false "Opaque status-bound next cursor"
// @Success 200 {object} AdminRefundQueuePageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/refund-cases [get]
func (handler *AdminCatalogHandler) ListRefundQueue(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c, "status", "limit", "cursor") {
		return
	}
	catalog, ok := handler.catalog.(xiangwanadmin.RefundQueueCatalog)
	if !ok {
		writeError(c, fmt.Errorf("administrator refund queue catalog is unavailable"))
		return
	}
	status := refund.Status(c.Query("status"))
	if status != "" && status != refund.StatusPendingManual &&
		status != refund.StatusProcessing && status != refund.StatusFailed {
		writeError(c, errx.NewBadRequest("invalid Refund status"))
		return
	}
	limit, ok := parseOptionalAdminInt(c, "limit")
	if !ok {
		return
	}
	if limit > refund.MaxQueueLimit || len(c.Query("cursor")) > 1024 {
		writeError(c, errx.NewBadRequest("invalid Refund queue pagination"))
		return
	}
	value, err := catalog.ListRefundQueue(c.Request.Context(), principal,
		xiangwanadmin.RefundQueueFilter{
			Status: status, Limit: limit, Cursor: c.Query("cursor"),
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminRefundQueueItemResponse, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, projectAdminRefundItem(item))
	}
	response.OK(c, AdminRefundQueuePageResponse{
		Items: items, Status: string(value.Status), NextCursor: value.NextCursor,
	})
}

// GetRefundCase godoc
// @Summary Read one Xiangwan Refund case status timeline
// @Description Requires a live tenant finance or super-admin Grant. Returns no operator notes, provider references, evidence, actor identities, or idempotency keys. A content-free read audit commits before the response.
// @Tags xiangwan-admin
// @Produce json
// @Param case_id path string true "Refund case UUID"
// @Success 200 {object} AdminRefundCaseDetailResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Router /xiangwan/admin/refund-cases/{case_id} [get]
func (handler *AdminCatalogHandler) GetRefundCase(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c) {
		return
	}
	catalog, ok := handler.catalog.(xiangwanadmin.RefundCaseCatalog)
	if !ok {
		writeError(c, fmt.Errorf("administrator refund case catalog is unavailable"))
		return
	}
	caseID, err := uuid.Parse(c.Param("case_id"))
	if err != nil || caseID == uuid.Nil {
		writeError(c, errx.NewBadRequest("invalid Refund case ID"))
		return
	}
	value, err := catalog.GetRefundCase(c.Request.Context(), principal, caseID)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	events := make([]AdminRefundCaseEventResponse, 0, len(value.Events))
	for _, item := range value.Events {
		events = append(events, AdminRefundCaseEventResponse{
			Sequence: item.Sequence, Type: string(item.Type),
			FromStatus: string(item.FromStatus), ToStatus: string(item.ToStatus),
			SuccessfulRefundCents:  strconv.FormatInt(item.SuccessfulRefundCents, 10),
			ResultingRefundVersion: item.ResultingRefundVersion,
			OccurredAt:             item.OccurredAt.UTC().Format(time.RFC3339Nano),
		})
	}
	response.OK(c, AdminRefundCaseDetailResponse{
		Case: projectAdminRefundItem(value.Case), Events: events,
	})
}

func projectAdminRefundItem(item xiangwanadmin.RefundQueueItem) AdminRefundQueueItemResponse {
	return AdminRefundQueueItemResponse{
		CaseID: item.CaseID.String(), OrderID: item.OrderID.String(),
		RegistrationID: item.RegistrationID.String(),
		InstanceID:     item.InstanceID.String(), SessionID: item.SessionID.String(),
		SeriesTitle: item.SeriesTitle, InstanceTitle: item.InstanceTitle,
		SessionTitle: item.SessionTitle, Status: string(item.Status),
		ReasonCode:            string(item.ReasonCode),
		RequestedRefundCents:  strconv.FormatInt(item.RequestedRefundCents, 10),
		SuccessfulRefundCents: strconv.FormatInt(item.SuccessfulRefundCents, 10),
		Version:               item.Version,
		CreatedAt:             item.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:             item.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}

type AdminRefundQueueItemResponse struct {
	CaseID                string `json:"case_id"`
	OrderID               string `json:"order_id"`
	RegistrationID        string `json:"registration_id"`
	InstanceID            string `json:"instance_id"`
	SessionID             string `json:"session_id"`
	SeriesTitle           string `json:"series_title"`
	InstanceTitle         string `json:"instance_title"`
	SessionTitle          string `json:"session_title"`
	Status                string `json:"status"`
	ReasonCode            string `json:"reason_code"`
	RequestedRefundCents  string `json:"requested_refund_cents"`
	SuccessfulRefundCents string `json:"successful_refund_cents"`
	Version               int64  `json:"version"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
}

type AdminRefundQueuePageResponse struct {
	Items      []AdminRefundQueueItemResponse `json:"items"`
	Status     string                         `json:"status"`
	NextCursor string                         `json:"next_cursor"`
}

type AdminRefundCaseEventResponse struct {
	Sequence               int64  `json:"sequence"`
	Type                   string `json:"type"`
	FromStatus             string `json:"from_status"`
	ToStatus               string `json:"to_status"`
	SuccessfulRefundCents  string `json:"successful_refund_cents"`
	ResultingRefundVersion int64  `json:"resulting_refund_version"`
	OccurredAt             string `json:"occurred_at"`
}

type AdminRefundCaseDetailResponse struct {
	Case   AdminRefundQueueItemResponse   `json:"case"`
	Events []AdminRefundCaseEventResponse `json:"events"`
}
