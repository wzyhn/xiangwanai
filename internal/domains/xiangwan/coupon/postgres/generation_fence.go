package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

var ErrCouponGenerationInactive = errors.New("xiangwan Coupon worker generation is inactive")

// GenerationBoundGrantPolicyProvider fences each newly issued grant in the
// same transaction as the Checkin source, Coupon ledger and policy snapshot.
// A worker's outer readiness check alone cannot prevent a cutover race.
type GenerationBoundGrantPolicyProvider struct {
	TenantID     uuid.UUID
	GenerationID uuid.UUID
}

var _ GrantPolicyProvider = GenerationBoundGrantPolicyProvider{}

func (provider GenerationBoundGrantPolicyProvider) CouponGrantPolicyAt(
	ctx context.Context, query CouponAuthorizationQuery, tenantID uuid.UUID, at time.Time,
) (coupon.GrantPolicy, error) {
	if provider.TenantID == uuid.Nil || provider.GenerationID == uuid.Nil ||
		tenantID != provider.TenantID {
		return coupon.GrantPolicy{}, ErrCouponGenerationInactive
	}
	if err := requireActiveCouponGeneration(ctx, query, tenantID, provider.GenerationID); err != nil {
		return coupon.GrantPolicy{}, err
	}
	return (PostgresGrantPolicyProvider{}).CouponGrantPolicyAt(ctx, query, tenantID, at)
}

func requireActiveCouponGeneration(
	ctx context.Context, query CouponAuthorizationQuery,
	tenantID, generationID uuid.UUID,
) error {
	if ctx == nil || query == nil || tenantID == uuid.Nil || generationID == uuid.Nil {
		return ErrCouponGenerationInactive
	}
	var active bool
	err := query.QueryRowContext(ctx, `
SELECT TRUE FROM xiangwan_runtime_generations
WHERE singleton_id = 1 AND scope_key = 'wq-xiangwan'
  AND tenant_id = $1 AND active_generation_id = $2
  AND write_epoch > 0 AND bootstrap_completed_at IS NOT NULL
FOR SHARE
`, tenantID, generationID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCouponGenerationInactive
	}
	if err != nil {
		return fmt.Errorf("fence Coupon worker generation: %w", err)
	}
	if !active {
		return ErrCouponGenerationInactive
	}
	return nil
}
