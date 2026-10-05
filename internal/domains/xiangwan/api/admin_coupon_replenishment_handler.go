package xiangwanapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type couponReplenisher interface {
	Replenish(context.Context, couponpostgres.ManualReplenishmentCommand) (couponpostgres.GrantResult, error)
}

type ReplenishCouponRequest struct {
	SourceCouponID string `json:"source_coupon_id"`
	OperationID    string `json:"operation_id"`
	Reason         string `json:"reason"`
	Context        string `json:"context"`
}

type ReplenishCouponResponse struct {
	SourceCouponID string   `json:"source_coupon_id"`
	BusinessKey    string   `json:"business_key"`
	CouponIDs      []string `json:"coupon_ids"`
	GrantedAt      string   `json:"granted_at"`
	Duplicate      bool     `json:"duplicate"`
}

// ReplenishCoupon records a five-Coupon batch for the owner of an existing
// tenant Coupon. The caller cannot choose an owner, face value, validity, or
// scope. The signed live policy and zero available balance are checked inside
// the serializable grant transaction.
func (handler *AdminCatalogHandler) ReplenishCoupon(c *gin.Context) {
	c.Header("Cache-Control", "private, no-store")
	principal, ok := handler.principal(c)
	if !ok || !validAdminWriteRequest(c) {
		return
	}
	if handler.couponReplenisher == nil || handler.couponTenantID == uuid.Nil {
		writeError(c, errx.NewInternal("Coupon replenishment is unavailable"))
		return
	}
	var request ReplenishCouponRequest
	if err := decodeAdminCatalogJSON(c, &request); err != nil {
		writeError(c, err)
		return
	}
	sourceID, err := uuid.Parse(request.SourceCouponID)
	if err != nil || sourceID == uuid.Nil {
		writeError(c, errx.NewBadRequest("invalid source Coupon"))
		return
	}
	operationID, err := uuid.Parse(request.OperationID)
	if err != nil || operationID == uuid.Nil ||
		strings.TrimSpace(request.Reason) == "" || len(request.Reason) > 500 ||
		strings.TrimSpace(request.Context) == "" || len(request.Context) > 128 {
		writeError(c, errx.NewBadRequest("invalid Coupon replenishment request"))
		return
	}
	result, err := handler.couponReplenisher.Replenish(c.Request.Context(),
		couponpostgres.ManualReplenishmentCommand{
			TenantID: handler.couponTenantID, SourceCouponID: sourceID,
			ActorID: principal.PrincipalID, IdentityLinkID: principal.IdentityLinkID,
			BusinessKey: "manual:" + sourceID.String() + ":" + operationID.String(),
			Reason:      strings.TrimSpace(request.Reason), Context: strings.TrimSpace(request.Context),
		})
	if err != nil {
		switch {
		case errors.Is(err, couponpostgres.ErrInvalidGrantCommand):
			writeError(c, errx.NewBadRequest("invalid Coupon replenishment request"))
		case errors.Is(err, couponpostgres.ErrGrantSourceNotFound):
			writeError(c, errx.New(errx.CodeXiangwanAdminTargetNotFound, "source Coupon not found"))
		case errors.Is(err, couponpostgres.ErrManualGrantForbidden):
			writeError(c, errx.NewConflict("Coupon balance or history does not allow replenishment"))
		case errors.Is(err, couponpostgres.ErrGrantPolicyUnavailable),
			errors.Is(err, couponpostgres.ErrCouponGenerationInactive):
			writeError(c, errx.NewConflict("Coupon grant policy or runtime generation is unavailable"))
		case errors.Is(err, couponpostgres.ErrGrantFactsConflict),
			errors.Is(err, couponpostgres.ErrGrantTransactionConflict):
			writeError(c, errx.NewConflict("Coupon replenishment conflicts with current ledger facts"))
		default:
			writeAdminCatalogError(c, err)
		}
		return
	}
	if len(result.Grant.Coupons) != coupon.GrantQuantity {
		writeError(c, errx.NewInternal("Coupon replenishment receipt is incomplete"))
		return
	}
	couponIDs := make([]string, 0, len(result.Grant.Coupons))
	for _, instrument := range result.Grant.Coupons {
		couponIDs = append(couponIDs, instrument.ID.String())
	}
	response.OK(c, ReplenishCouponResponse{
		SourceCouponID: sourceID.String(), BusinessKey: result.Grant.BusinessKey,
		CouponIDs: couponIDs,
		GrantedAt: result.Grant.Coupons[0].GrantedAt.UTC().Format(time.RFC3339Nano),
		Duplicate: result.Duplicate,
	})
}
