package xiangwanruntime

import (
	"context"
	"errors"
	"testing"

	contributionpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution/postgres"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/google/uuid"
)

type couponWorkerGenerationProbe struct {
	calls int
	err   error
}

func (probe *couponWorkerGenerationProbe) CheckActiveGeneration(context.Context, uuid.UUID, uuid.UUID) error {
	probe.calls++
	return probe.err
}

type couponWorkerSchemaProbe struct {
	calls int
	err   error
}

func (probe *couponWorkerSchemaProbe) CheckCouponReconciliationSchema(context.Context) error {
	probe.calls++
	return probe.err
}

type couponGrantProbe struct {
	calls int
	err   error
}

func (probe *couponGrantProbe) ReconcilePending(context.Context, uuid.UUID, int) ([]couponpostgres.GrantResult, error) {
	probe.calls++
	return nil, probe.err
}

type couponCorrectionProbe struct {
	calls int
	err   error
}

type contributionWorkerProbe struct {
	calls int
	err   error
}

func (p *contributionWorkerProbe) ReconcilePending(context.Context, uuid.UUID, int) ([]contributionpostgres.ReconcileResult, error) {
	p.calls++
	return nil, p.err
}

type checkinTaskWorkerProbe struct {
	calls int
	err   error
}

func (p *checkinTaskWorkerProbe) ProcessPending(context.Context, int) (int, error) {
	p.calls++
	return 0, p.err
}

func (probe *couponCorrectionProbe) ReconcilePending(context.Context, uuid.UUID, int) ([]couponpostgres.CorrectionResult, error) {
	probe.calls++
	return nil, probe.err
}

func TestCouponWorkerChecksGenerationAndSchemaBeforeReconciliation(t *testing.T) {
	gate := &couponWorkerGenerationProbe{}
	schema := &couponWorkerSchemaProbe{}
	grants := &couponGrantProbe{err: couponpostgres.ErrCouponGenerationInactive}
	corrections := &couponCorrectionProbe{}
	contributions := &contributionWorkerProbe{}
	tasks := &checkinTaskWorkerProbe{}
	worker := &CouponReconciliationWorker{
		grants: grants, corrections: corrections,
		contributions: contributions, checkinTasks: tasks,
		generationGate: gate, schemaGate: schema,
		tenantID: uuid.New(), generationID: uuid.New(),
		pollInterval: CouponReconciliationPollInterval,
	}
	if err := worker.Run(context.Background()); err != nil ||
		gate.calls != 1 || schema.calls != 1 ||
		corrections.calls != 1 || grants.calls != 1 || contributions.calls != 1 || tasks.calls != 1 {
		t.Fatalf("worker run = %v gate=%d schema=%d correction=%d grant=%d",
			err, gate.calls, schema.calls, corrections.calls, grants.calls)
	}
	gate.err = ErrGenerationInactive
	if err := worker.Run(context.Background()); err != nil || corrections.calls != 1 || grants.calls != 1 {
		t.Fatalf("stale worker wrote after cutover: %v", err)
	}
}

func TestCouponWorkerFailsClosedBeforeGrantIfCorrectionFails(t *testing.T) {
	grants := &couponGrantProbe{}
	corrections := &couponCorrectionProbe{err: errors.New("synthetic database failure")}
	worker := &CouponReconciliationWorker{
		grants: grants, corrections: corrections,
		contributions: &contributionWorkerProbe{}, checkinTasks: &checkinTaskWorkerProbe{},
		generationGate: &couponWorkerGenerationProbe{}, schemaGate: &couponWorkerSchemaProbe{},
		tenantID: uuid.New(), generationID: uuid.New(),
		pollInterval: CouponReconciliationPollInterval,
	}
	if err := worker.Run(context.Background()); err == nil || grants.calls != 0 {
		t.Fatalf("correction failure allowed grant: %v, grant calls=%d", err, grants.calls)
	}
}

func TestCouponWorkerStopsBeforeGrantWhenAttendanceCorrectionFails(t *testing.T) {
	grants, corrections := &couponGrantProbe{}, &couponCorrectionProbe{}
	tasks := &checkinTaskWorkerProbe{err: errors.New("synthetic correction failure")}
	contributions := &contributionWorkerProbe{}
	worker := &CouponReconciliationWorker{grants: grants, corrections: corrections, contributions: contributions, checkinTasks: tasks, generationGate: &couponWorkerGenerationProbe{}, schemaGate: &couponWorkerSchemaProbe{}, tenantID: uuid.New(), generationID: uuid.New(), pollInterval: CouponReconciliationPollInterval}
	if err := worker.Run(context.Background()); err == nil || contributions.calls != 0 || corrections.calls != 0 || grants.calls != 0 {
		t.Fatalf("failed task allowed new grants: %v", err)
	}
}

func TestCouponWorkerConfigNeedsOnlyDedicatedDatabaseIdentity(t *testing.T) {
	values := map[string]string{
		CouponReconciliationDatabaseDSNEnv: "postgres://coupon:synthetic@db/xiangwan",
		TenantIDEnv:                        uuid.NewString(), GenerationIDEnv: uuid.NewString(),
	}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	config, err := LoadCouponReconciliationWorkerConfig(lookup)
	if err != nil || config.DatabaseDSN != values[CouponReconciliationDatabaseDSNEnv] {
		t.Fatalf("worker config = %+v, %v", config, err)
	}
	for _, name := range []string{CouponReconciliationDatabaseDSNEnv, TenantIDEnv, GenerationIDEnv} {
		_, err := LoadCouponReconciliationWorkerConfig(func(key string) (string, bool) {
			if key == name {
				return "", false
			}
			return lookup(key)
		})
		if err == nil {
			t.Fatalf("missing %s accepted", name)
		}
	}
}
