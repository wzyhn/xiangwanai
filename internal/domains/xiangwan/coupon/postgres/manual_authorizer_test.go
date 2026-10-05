package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestManualGrantAuthorizationStopsBeforeIdentityOnStaleGeneration(t *testing.T) {
	probe := &grantPolicyProbe{}
	database := sql.OpenDB(probe)
	defer func() { _ = database.Close() }()
	tenantID, generationID := uuid.New(), uuid.New()
	called := false
	authorizer := GenerationBoundManualGrantAuthorizer{
		TenantID: tenantID, GenerationID: generationID,
		RequireSuperAdmin: func(context.Context, CouponAuthorizationQuery, uuid.UUID, uuid.UUID) error {
			called = true
			return nil
		},
	}
	if err := authorizer.AuthorizeManualCouponGrant(context.Background(), database,
		uuid.New(), uuid.New(), uuid.New()); !errors.Is(err, ErrManualGrantForbidden) || probe.query != "" {
		t.Fatalf("cross-tenant manual grant = %v query=%q", err, probe.query)
	}
	if err := authorizer.AuthorizeManualCouponGrant(context.Background(), database,
		tenantID, uuid.New(), uuid.New()); !errors.Is(err, ErrCouponGenerationInactive) ||
		probe.query == "" || called {
		t.Fatalf("stale manual grant = %v query=%q called=%v", err, probe.query, called)
	}
}
