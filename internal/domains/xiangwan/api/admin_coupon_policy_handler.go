package xiangwanapi

import (
	"encoding/base64"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type ActivateCouponGrantPolicyRequest struct {
	PayloadBase64   string `json:"payload_base64"`
	SignatureBase64 string `json:"signature_base64"`
}

type ActivateCouponGrantPolicyResponse struct {
	PolicyVersion string `json:"policy_version"`
	Enabled       bool   `json:"enabled"`
	EffectiveAt   string `json:"effective_at"`
	RecordedAt    string `json:"recorded_at"`
	Duplicate     bool   `json:"duplicate"`
}

// ActivateCouponGrantPolicy godoc
// @Summary Record one customer-signed future Coupon grant policy version
// @Description Only registered with a customer verification key and transaction-bound super-admin authorizer. Monetary and scope values come solely from the signed canonical payload. No policy is seeded or enabled by this handler's existence.
// @Tags xiangwan-admin
// @Accept json
// @Produce json
// @Param request body ActivateCouponGrantPolicyRequest true "Canonical signed customer policy"
// @Success 200 {object} ActivateCouponGrantPolicyResponse
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 403 {object} response.Body
// @Failure 409 {object} response.Body
func (handler *AdminCatalogHandler) ActivateCouponGrantPolicy(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.couponPolicy == nil {
		writeError(c, errx.NewInternal("Coupon grant policy activation unavailable"))
		return
	}
	var request ActivateCouponGrantPolicyRequest
	if err := decodeAdminCatalogJSON(c, &request); err != nil {
		writeError(c, err)
		return
	}
	payload, payloadErr := base64.StdEncoding.Strict().DecodeString(request.PayloadBase64)
	signature, signatureErr := base64.StdEncoding.Strict().DecodeString(request.SignatureBase64)
	if payloadErr != nil || signatureErr != nil || len(payload) == 0 || len(payload) > 4096 {
		writeError(c, errx.NewBadRequest("invalid signed Coupon grant policy"))
		return
	}
	receipt, err := handler.couponPolicy.Activate(c.Request.Context(), coupon.PolicyActivationCommand{
		ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
		Payload: payload, Signature: signature,
	})
	if err != nil {
		switch {
		case errors.Is(err, coupon.ErrInvalidPolicyActivation),
			errors.Is(err, coupon.ErrInvalidSignedGrantPolicy):
			writeError(c, errx.NewBadRequest("invalid signed Coupon grant policy"))
		case errors.Is(err, coupon.ErrPolicyActivationConflict):
			writeError(c, errx.NewConflict("Coupon grant policy version conflicts with an existing fact"))
		default:
			writeAdminCatalogError(c, err)
		}
		return
	}
	response.OK(c, ActivateCouponGrantPolicyResponse{
		PolicyVersion: receipt.PolicyVersion, Enabled: receipt.Enabled,
		EffectiveAt: receipt.EffectiveAt.UTC().Format(time.RFC3339Nano),
		RecordedAt:  receipt.RecordedAt.UTC().Format(time.RFC3339Nano),
		Duplicate:   receipt.Duplicate,
	})
}
