package xiangwanapi

import (
	"fmt"
	"strconv"
	"strings"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// TransitionRefund godoc
// @Summary Record a manual Xiangwan Refund transition
// @Description ADMIN OP-KEY command. Finance may start or fail manual work; a tenant super administrator may reject or confirm completion. Completion requires a different prior processing actor, the exact amount, official external Refund ID and evidence reference. The transaction rechecks live identity and Grant and appends an immutable Refund event; it never calls the payment provider.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param case_id path string true "Refund case UUID"
// @Param Idempotency-Key header string true "Canonical lowercase UUIDv4 operation key"
// @Param request body AdminRefundActionRequest true "Exact action and version"
// @Success 200 {object} AdminRefundActionResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 404 {object} response.Body
// @Failure 409 {object} response.Body
// @Router /xiangwan/admin/refund-cases/{case_id}/actions [post]
func (handler *AdminCatalogHandler) TransitionRefund(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.refundOperator == nil {
		writeError(c, fmt.Errorf("administrator refund operator is unavailable"))
		return
	}
	operationID, err := parseOperationKey(c)
	if err != nil {
		writeError(c, err)
		return
	}
	caseID, err := parseCanonicalUUID(c.Param("case_id"))
	if err != nil {
		writeError(c, errx.NewBadRequest("invalid Refund case ID"))
		return
	}
	var payload AdminRefundActionRequest
	if err := decodeAdminCatalogJSON(c, &payload); err != nil {
		writeError(c, err)
		return
	}
	amount := int64(0)
	if payload.SuccessfulRefundCents != "" {
		amount, err = strconv.ParseInt(payload.SuccessfulRefundCents, 10, 64)
		if err != nil || amount < 0 {
			writeError(c, errx.NewBadRequest("invalid successful Refund cents"))
			return
		}
	}
	result, err := handler.refundOperator.TransitionRefund(c.Request.Context(),
		xiangwanadmin.RefundActionCommand{
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			OperationID: operationID, CaseID: caseID,
			ExpectedVersion:       payload.ExpectedVersion,
			Action:                xiangwanadmin.RefundAction(strings.TrimSpace(payload.Action)),
			SuccessfulRefundCents: amount,
			ExternalRefundID:      payload.ExternalRefundID,
			EvidenceReference:     payload.EvidenceReference,
			FailureReason:         payload.FailureReason,
			OperatorNote:          payload.OperatorNote,
		})
	if err != nil {
		writeAdminCatalogError(c, err)
		return
	}
	response.OK(c, AdminRefundActionResponse{
		CaseID: result.CaseID.String(), Status: string(result.Status),
		Version:               result.Version,
		SuccessfulRefundCents: strconv.FormatInt(result.SuccessfulRefundCents, 10),
		EventSequence:         result.EventSequence,
	})
}

type AdminRefundActionRequest struct {
	Action                string `json:"action"`
	ExpectedVersion       int64  `json:"expected_version"`
	SuccessfulRefundCents string `json:"successful_refund_cents,omitempty"`
	ExternalRefundID      string `json:"external_refund_id,omitempty"`
	EvidenceReference     string `json:"evidence_reference,omitempty"`
	FailureReason         string `json:"failure_reason,omitempty"`
	OperatorNote          string `json:"operator_note,omitempty"`
}

type AdminRefundActionResponse struct {
	CaseID                string `json:"case_id"`
	Status                string `json:"status"`
	Version               int64  `json:"version"`
	SuccessfulRefundCents string `json:"successful_refund_cents"`
	EventSequence         int64  `json:"event_sequence"`
}
