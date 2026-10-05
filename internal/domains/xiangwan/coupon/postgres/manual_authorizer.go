package couponpostgres

import (
	"context"

	"github.com/google/uuid"
)

type ExactSuperAdminGrant func(context.Context, CouponAuthorizationQuery, uuid.UUID, uuid.UUID) error

// GenerationBoundManualGrantAuthorizer repeats the generation and exact
// identity/Grant checks inside the serializable ledger transaction.
type GenerationBoundManualGrantAuthorizer struct {
	TenantID          uuid.UUID
	GenerationID      uuid.UUID
	RequireSuperAdmin ExactSuperAdminGrant
}

func (authorizer GenerationBoundManualGrantAuthorizer) AuthorizeManualCouponGrant(
	ctx context.Context, query CouponAuthorizationQuery,
	tenantID, actorID, identityLinkID uuid.UUID,
) error {
	if tenantID == uuid.Nil || tenantID != authorizer.TenantID ||
		authorizer.GenerationID == uuid.Nil || actorID == uuid.Nil ||
		identityLinkID == uuid.Nil || authorizer.RequireSuperAdmin == nil {
		return ErrManualGrantForbidden
	}
	if err := requireActiveCouponGeneration(ctx, query, tenantID, authorizer.GenerationID); err != nil {
		return err
	}
	return authorizer.RequireSuperAdmin(ctx, query, actorID, identityLinkID)
}
