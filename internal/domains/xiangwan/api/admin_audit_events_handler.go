package xiangwanapi

import (
	"fmt"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ListAuditEvents godoc
// @Summary List Xiangwan administrator audit event metadata
// @Description Requires a live tenant super_admin Grant. Details JSONB and sensitive values are never returned. The audit read itself is audited. Pass the returned as_of value when requesting later pages to keep the timeline stable.
// @Tags xiangwan-admin
// @Produce json
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param as_of query string false "UTC snapshot boundary from the first page (required after page one)"
// @Success 200 {object} AdminAuditEventPageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/audit-events [get]
func (handler *AdminCatalogHandler) ListAuditEvents(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c, "page", "page_size", "as_of") {
		return
	}
	catalog, ok := handler.catalog.(xiangwanadmin.AuditEventCatalog)
	if !ok {
		writeError(c, fmt.Errorf("administrator audit catalog is unavailable"))
		return
	}
	page, pageSize, ok := parseAdminPage(c)
	if !ok {
		return
	}
	var asOf *time.Time
	if raw := c.Query("as_of"); raw != "" {
		parsed, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || parsed.IsZero() {
			writeError(c, errx.NewBadRequest("invalid audit snapshot boundary"))
			return
		}
		asOf = &parsed
	}
	if page > 1 && asOf == nil {
		writeError(c, errx.NewBadRequest("audit snapshot boundary is required after page one"))
		return
	}
	value, err := catalog.ListAuditEvents(
		c.Request.Context(), principal,
		xiangwanadmin.AuditEventFilter{Page: page, PageSize: pageSize, AsOf: asOf},
	)
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminAuditEventResponse, 0, len(value.Items))
	for _, item := range value.Items {
		items = append(items, AdminAuditEventResponse{
			ID: item.ID.String(), ActorID: item.ActorID.String(),
			ActionCode: item.ActionCode, TargetType: item.TargetType,
			TargetID:   item.TargetID.String(),
			OccurredAt: item.OccurredAt.UTC().Format(time.RFC3339Nano),
		})
	}
	response.OK(c, AdminAuditEventPageResponse{
		Items: items, Page: value.Page, PageSize: value.PageSize,
		Total: value.Total, AsOf: value.AsOf.UTC().Format(time.RFC3339Nano),
	})
}

type AdminAuditEventResponse struct {
	ID         string `json:"id"`
	ActorID    string `json:"actor_id"`
	ActionCode string `json:"action_code"`
	TargetType string `json:"target_type"`
	TargetID   string `json:"target_id"`
	OccurredAt string `json:"occurred_at"`
}

type AdminAuditEventPageResponse struct {
	Items    []AdminAuditEventResponse `json:"items"`
	Page     int                       `json:"page"`
	PageSize int                       `json:"page_size"`
	Total    int64                     `json:"total"`
	AsOf     string                    `json:"as_of"`
}
