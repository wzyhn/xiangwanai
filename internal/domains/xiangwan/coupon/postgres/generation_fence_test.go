package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCouponGenerationBoundPolicyRejectsInactiveOrCrossTenant(t *testing.T) {
	probe := &grantPolicyProbe{}
	database := sql.OpenDB(probe)
	defer func() { _ = database.Close() }()
	tenantID, generationID := uuid.New(), uuid.New()
	provider := GenerationBoundGrantPolicyProvider{TenantID: tenantID, GenerationID: generationID}
	if _, err := provider.CouponGrantPolicyAt(context.Background(), database, uuid.New(), testCouponPolicyTime()); !errors.Is(err, ErrCouponGenerationInactive) || probe.query != "" {
		t.Fatalf("cross-tenant policy read = %v, query=%q", err, probe.query)
	}
	if _, err := provider.CouponGrantPolicyAt(context.Background(), database, tenantID, testCouponPolicyTime()); !errors.Is(err, ErrCouponGenerationInactive) || probe.query == "" {
		t.Fatalf("inactive-generation policy read = %v, query=%q", err, probe.query)
	}
}

func testCouponPolicyTime() time.Time {
	return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
}

func TestCouponCorrectionGenerationFenceRunsBeforeAnySourceWrite(t *testing.T) {
	tx := &fakeCorrectionTransaction{}
	starter := &fakeCorrectionTransactionStarter{tx: tx}
	reconciler := &CorrectionReconciler{
		transactions: starter,
		now:          time.Now,
		generationFence: func(context.Context, CouponAuthorizationQuery, uuid.UUID) error {
			return ErrCouponGenerationInactive
		},
	}
	_, err := reconciler.Reconcile(context.Background(), CorrectionCommand{
		TenantID: uuid.New(), CheckinID: uuid.New(),
	})
	if !errors.Is(err, ErrCouponGenerationInactive) || !tx.rolledBack || tx.committed {
		t.Fatalf("stale correction = %v, tx=%+v", err, tx)
	}
}
