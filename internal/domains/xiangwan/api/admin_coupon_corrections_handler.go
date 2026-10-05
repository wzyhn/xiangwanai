package xiangwanapi

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ListCouponCorrections godoc
// @Summary List immutable Coupon correction markers for manual review
// @Description Requires a live tenant super_admin Grant. Returns routing IDs and manual handling state, not notes, evidence references, contacts or grant policy evidence. The read is audited. Pass as_of on later pages.
// @Tags xiangwan-admin
// @Produce json
// @Param page query int false "Page number" minimum(1)
// @Param page_size query int false "Page size" minimum(1) maximum(100)
// @Param as_of query string false "UTC snapshot boundary from the first page (required after page one)"
// @Success 200 {object} AdminCouponCorrectionPageResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Router /xiangwan/admin/coupon-corrections [get]
func (handler *AdminCatalogHandler) ListCouponCorrections(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminQuery(c, "page", "page_size", "as_of") {
		return
	}
	if handler.couponCorrections == nil {
		writeError(c, fmt.Errorf("Coupon correction list is unavailable"))
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
			writeError(c, errx.NewBadRequest("invalid Coupon correction snapshot boundary"))
			return
		}
		asOf = &parsed
	}
	if page > 1 && asOf == nil {
		writeError(c, errx.NewBadRequest("Coupon correction snapshot boundary is required after page one"))
		return
	}
	value, err := handler.couponCorrections.ListCouponCorrections(c.Request.Context(), principal,
		xiangwanadmin.CouponCorrectionFilter{Page: page, PageSize: pageSize, AsOf: asOf})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	items := make([]AdminCouponCorrectionResponse, 0, len(value.Items))
	for _, item := range value.Items {
		entry := AdminCouponCorrectionResponse{
			EntryID: item.EntryID.String(), CouponID: item.CouponID.String(),
			FaceValueCents:       strconv.FormatInt(item.FaceValueCents, 10),
			SourceCheckinEventID: item.SourceCheckinEventID.String(),
			RelatedEntryType:     item.RelatedEntryType,
			RecordedAt:           item.RecordedAt.UTC().Format(time.RFC3339Nano),
			HandlingStatus:       item.HandlingStatus,
			HandlingVersion:      item.HandlingVersion,
			EvidenceKind:         item.EvidenceKind,
			AdjustmentCents:      strconv.FormatInt(item.AdjustmentCents, 10),
		}
		if item.OrderID != nil {
			entry.OrderID = item.OrderID.String()
		}
		if item.RegistrationID != nil {
			entry.RegistrationID = item.RegistrationID.String()
		}
		items = append(items, entry)
	}
	response.OK(c, AdminCouponCorrectionPageResponse{
		Items: items, Page: value.Page, PageSize: value.PageSize,
		Total: value.Total, AsOf: value.AsOf.UTC().Format(time.RFC3339Nano),
	})
}

type AdminCouponCorrectionResponse struct {
	EntryID              string `json:"entry_id"`
	CouponID             string `json:"coupon_id"`
	FaceValueCents       string `json:"face_value_cents"`
	SourceCheckinEventID string `json:"source_checkin_event_id"`
	RelatedEntryType     string `json:"related_entry_type"`
	OrderID              string `json:"order_id,omitempty"`
	RegistrationID       string `json:"registration_id,omitempty"`
	RecordedAt           string `json:"recorded_at"`
	HandlingStatus       string `json:"handling_status"`
	HandlingVersion      int64  `json:"handling_version"`
	EvidenceKind         string `json:"evidence_kind,omitempty"`
	AdjustmentCents      string `json:"adjustment_cents"`
}

type AdminCouponCorrectionPageResponse struct {
	Items    []AdminCouponCorrectionResponse `json:"items"`
	Page     int                             `json:"page"`
	PageSize int                             `json:"page_size"`
	Total    int64                           `json:"total"`
	AsOf     string                          `json:"as_of"`
}

// TransitionCouponCorrection godoc
// @Summary Record two-person manual handling of a revoked-Checkin Coupon exception
// @Description A tenant super_admin starts the case; a different super_admin may record official external financial evidence for a redeemed Coupon, or ledger-proved invalidation for a never-redeemed Coupon. This does not call a provider or change Coupon/Order facts.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param entry_id path string true "Correction marker UUID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body AdminCouponCorrectionActionRequest true "Action and expected version"
// @Success 200 {object} AdminCouponCorrectionActionResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/coupon-corrections/{entry_id}/actions [post]
func (handler *AdminCatalogHandler) TransitionCouponCorrection(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.couponCorrectionOperator == nil {
		writeError(c, fmt.Errorf("Coupon correction operator is unavailable"))
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	entryID, err := parseCanonicalUUID(c.Param("entry_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Coupon correction entry ID"))
		return
	}
	var payload AdminCouponCorrectionActionRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	amount := int64(0)
	if payload.AdjustmentCents != "" {
		amount, err = strconv.ParseInt(payload.AdjustmentCents, 10, 64)
		if err != nil || amount < 0 {
			writeError(c, errx.NewBadRequest("invalid Coupon adjustment cents"))
			return
		}
	}
	result, err := handler.couponCorrectionOperator.TransitionCouponCorrection(
		c.Request.Context(), xiangwanadmin.CouponCorrectionActionCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID, RequestID: adminRequestID(c),
			CorrectionEntryID: entryID, ExpectedVersion: payload.ExpectedVersion,
			Action:            xiangwanadmin.CouponCorrectionAction(strings.TrimSpace(payload.Action)),
			EvidenceKind:      strings.TrimSpace(payload.EvidenceKind),
			EvidenceReference: payload.EvidenceReference,
			AdjustmentCents:   amount, OperatorNote: payload.OperatorNote,
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, AdminCouponCorrectionActionResponse{
		CorrectionEntryID: result.CorrectionEntryID.String(),
		CouponID:          result.CouponID.String(), Status: result.Status,
		Version: result.Version, EvidenceKind: result.EvidenceKind,
		AdjustmentCents: strconv.FormatInt(result.AdjustmentCents, 10),
		RecordedAt:      result.RecordedAt.UTC().Format(time.RFC3339Nano),
	})
}

type AdminCouponCorrectionActionRequest struct {
	Action            string `json:"action"`
	ExpectedVersion   int64  `json:"expected_version"`
	EvidenceKind      string `json:"evidence_kind,omitempty"`
	EvidenceReference string `json:"evidence_reference,omitempty"`
	AdjustmentCents   string `json:"adjustment_cents,omitempty"`
	OperatorNote      string `json:"operator_note"`
}

type AdminCouponCorrectionActionResponse struct {
	CorrectionEntryID string `json:"correction_entry_id"`
	CouponID          string `json:"coupon_id"`
	Status            string `json:"status"`
	Version           int64  `json:"version"`
	EvidenceKind      string `json:"evidence_kind,omitempty"`
	AdjustmentCents   string `json:"adjustment_cents"`
	RecordedAt        string `json:"recorded_at"`
}
