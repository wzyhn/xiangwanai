package xiangwanapi

import (
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type AdminSessionCancellationPreviewResponse struct {
	ID                          string                                    `json:"id"`
	SessionID                   string                                    `json:"session_id"`
	ExpectedSessionVersion      int64                                     `json:"expected_session_version"`
	CancelledRegistrationCount  int                                       `json:"cancelled_registration_count"`
	ConfirmedRegistrationCount  int                                       `json:"confirmed_registration_count"`
	ActiveHoldCount             int                                       `json:"active_hold_count"`
	FreeRegistrationCount       int                                       `json:"free_registration_count"`
	PaidRefundRegistrationCount int                                       `json:"paid_refund_registration_count"`
	PendingOrderCount           int                                       `json:"pending_order_count"`
	UnknownPaymentCount         int                                       `json:"unknown_payment_count"`
	RefundCaseCount             int                                       `json:"refund_case_count"`
	RequestedRefundCents        int64                                     `json:"requested_refund_cents"`
	CouponAdjustmentCount       int                                       `json:"coupon_adjustment_count"`
	NotificationStrategy        activity.CancellationNotificationStrategy `json:"notification_strategy"`
	ExpiresAt                   string                                    `json:"expires_at"`
}

type AdminSessionCancellationRequest struct {
	PreviewID              string `json:"preview_id"`
	ExpectedSessionVersion int64  `json:"expected_session_version"`
	Reason                 string `json:"reason"`
}

type AdminSessionCancellationResponse struct {
	Session     AdminSessionResponse `json:"session"`
	ReceiptID   string               `json:"receipt_id"`
	CancelledAt string               `json:"cancelled_at"`
}

// PreviewSessionCancellation godoc
// @Summary Preview cancellation of exactly one published Xiangwan Session
// @Description ADMIN OP-KEY. Snapshot includes registrations, holds, pending and unknown payment facts, refunds and coupons. The instance and sibling sessions remain unchanged.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param session_id path string true "Session ID"
// @Param Idempotency-Key header string true "Canonical UUIDv4 operation key"
// @Success 200 {object} AdminSessionCancellationPreviewResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/sessions/{session_id}/cancellation-previews [post]
func (handler *AdminCatalogHandler) PreviewSessionCancellation(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.sessionCancellation == nil {
		writeError(c, errx.NewInternal("administrator Session cancellation is unavailable"))
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	sessionID, err := parseCanonicalUUID(c.Param("session_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Session id"))
		return
	}
	var payload struct{}
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	value, err := handler.sessionCancellation.PreviewSessionCancellation(c.Request.Context(), xiangwanadmin.PreviewSessionCancellationCommand{ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID, OperationID: operationID, RequestID: adminRequestID(c), SessionID: sessionID})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, AdminSessionCancellationPreviewResponse{
		ID: value.ID.String(), SessionID: value.SessionID.String(), ExpectedSessionVersion: value.ExpectedSessionVersion,
		CancelledRegistrationCount: value.CancelledRegistrationCount, ConfirmedRegistrationCount: value.ConfirmedRegistrationCount,
		ActiveHoldCount: value.ActiveHoldCount, FreeRegistrationCount: value.FreeRegistrationCount, PaidRefundRegistrationCount: value.PaidRefundRegistrationCount,
		PendingOrderCount: value.PendingOrderCount, UnknownPaymentCount: value.UnknownPaymentCount, RefundCaseCount: value.RefundCaseCount,
		RequestedRefundCents: value.RequestedRefundCents, CouponAdjustmentCount: value.CouponAdjustmentCount, NotificationStrategy: value.NotificationStrategy,
		ExpiresAt: value.ExpiresAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
	})
}

// CancelSession godoc
// @Summary Cancel exactly one Xiangwan Session after its impact preview
// @Description ADMIN OP-KEY. Rechecks live generation, administrator identity/grant, version and preview facts in the same serializable transaction. Closes participation and holds, preserves payment and refund history, records audit, and leaves sibling sessions and instance status unchanged.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param session_id path string true "Session ID"
// @Param Idempotency-Key header string true "Canonical UUIDv4 operation key"
// @Param request body AdminSessionCancellationRequest true "Preview confirmation and required reason"
// @Success 200 {object} AdminSessionCancellationResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/sessions/{session_id}/cancellation [post]
func (handler *AdminCatalogHandler) CancelSession(c *gin.Context) {
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.sessionCancellation == nil {
		writeError(c, errx.NewInternal("administrator Session cancellation is unavailable"))
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	sessionID, err := parseCanonicalUUID(c.Param("session_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Session id"))
		return
	}
	var payload AdminSessionCancellationRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	previewID, err := parseCanonicalUUID(payload.PreviewID)
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid cancellation preview id"))
		return
	}
	value, err := handler.sessionCancellation.CancelSession(c.Request.Context(), xiangwanadmin.CancelSessionCommand{ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID, OperationID: operationID, RequestID: adminRequestID(c), SessionID: sessionID, PreviewID: previewID, ExpectedSessionVersion: payload.ExpectedSessionVersion, Reason: payload.Reason})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, AdminSessionCancellationResponse{Session: projectAdminSession(value.Session), ReceiptID: value.Receipt.ID.String(), CancelledAt: value.Receipt.CancelledAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00")})
}
