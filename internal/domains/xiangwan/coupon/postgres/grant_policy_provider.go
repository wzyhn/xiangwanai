package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

// PostgresGrantPolicyProvider reads the latest policy effective at the grant
// source time inside the Grantor's transaction. Migration 819
// deliberately seeds no row; issuance remains unavailable until the customer
// supplies signed face, expiry and scope decisions.
type PostgresGrantPolicyProvider struct{}

var _ GrantPolicyProvider = PostgresGrantPolicyProvider{}

func (PostgresGrantPolicyProvider) CouponGrantPolicyAt(
	ctx context.Context, query CouponAuthorizationQuery, tenantID uuid.UUID, at time.Time,
) (coupon.GrantPolicy, error) {
	if ctx == nil || query == nil || tenantID == uuid.Nil || at.IsZero() {
		return coupon.GrantPolicy{}, ErrGrantPolicyUnavailable
	}
	var version string
	var enabled bool
	var face, validitySeconds, minimumOrder sql.NullInt64
	var scopeType, scopeActivity sql.NullString
	var scopeSeries uuid.NullUUID
	err := query.QueryRowContext(ctx, `
SELECT policy_version, enabled, face_value_cents, validity_seconds,
       scope_type, scope_activity_type, scope_series_id, minimum_order_cents
FROM xiangwan_coupon_grant_policy_versions
WHERE tenant_id = $1 AND effective_at <= $2
ORDER BY effective_at DESC
LIMIT 1
FOR SHARE
`, tenantID, at.UTC()).Scan(&version, &enabled, &face, &validitySeconds,
		&scopeType, &scopeActivity, &scopeSeries, &minimumOrder)
	if errors.Is(err, sql.ErrNoRows) {
		return coupon.GrantPolicy{}, ErrGrantPolicyUnavailable
	}
	if err != nil {
		return coupon.GrantPolicy{}, fmt.Errorf("read xiangwan Coupon grant policy: %w", err)
	}
	if !enabled || !face.Valid || !validitySeconds.Valid ||
		!scopeType.Valid || !minimumOrder.Valid ||
		validitySeconds.Int64 <= 0 ||
		validitySeconds.Int64 > int64(coupon.MaxPolicyValidity/time.Second) {
		return coupon.GrantPolicy{}, ErrGrantPolicyUnavailable
	}
	policy := coupon.GrantPolicy{
		Configured: true, PolicyVersion: version,
		FaceValueCents:    face.Int64,
		Validity:          time.Duration(validitySeconds.Int64) * time.Second,
		ScopeType:         coupon.ScopeType(scopeType.String),
		MinimumOrderCents: minimumOrder.Int64,
	}
	if scopeActivity.Valid {
		value := activity.ActivityType(scopeActivity.String)
		policy.ScopeActivityType = &value
	}
	if scopeSeries.Valid {
		value := scopeSeries.UUID
		policy.ScopeSeriesID = &value
	}
	if coupon.ValidateGrantPolicy(policy) != nil {
		return coupon.GrantPolicy{}, ErrGrantPolicyUnavailable
	}
	return policy, nil
}
