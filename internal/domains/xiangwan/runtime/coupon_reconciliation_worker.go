package xiangwanruntime

import (
	contributionpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution/postgres"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/google/uuid"
)

const (
	CouponReconciliationPollInterval = time.Minute
	couponReconciliationBatchLimit   = 100
)

var (
	ErrInvalidCouponReconciliationWorker     = errors.New("invalid xiangwan Coupon reconciliation worker")
	ErrCouponReconciliationSchemaUnavailable = errors.New("xiangwan Coupon reconciliation schema is unavailable")
)

type pendingCouponGrants interface {
	ReconcilePending(context.Context, uuid.UUID, int) ([]couponpostgres.GrantResult, error)
}

type pendingCouponCorrections interface {
	ReconcilePending(context.Context, uuid.UUID, int) ([]couponpostgres.CorrectionResult, error)
}

type pendingContributions interface {
	ReconcilePending(context.Context, uuid.UUID, int) ([]contributionpostgres.ReconcileResult, error)
}
type pendingCheckinCorrections interface {
	ProcessPending(context.Context, int) (int, error)
}

type couponReconciliationSchemaGate interface {
	CheckCouponReconciliationSchema(context.Context) error
}

// CouponReconciliationWorker derives work only from PostgreSQL Checkin and
// Coupon facts. Both new-grant policy lookup and correction writes lock the
// active generation inside their own transactions, not just at poll time.
type CouponReconciliationWorker struct {
	database       *sql.DB
	grants         pendingCouponGrants
	corrections    pendingCouponCorrections
	contributions  pendingContributions
	checkinTasks   pendingCheckinCorrections
	generationGate GenerationGate
	schemaGate     couponReconciliationSchemaGate
	tenantID       uuid.UUID
	generationID   uuid.UUID
	pollInterval   time.Duration
}

func NewCouponReconciliationWorker(
	ctx context.Context, config CouponReconciliationWorkerConfig,
) (*CouponReconciliationWorker, error) {
	if ctx == nil || !validCouponReconciliationWorkerConfig(config) {
		return nil, ErrInvalidCouponReconciliationWorker
	}
	database, err := sql.Open("pgx", config.DatabaseDSN)
	if err != nil {
		return nil, ErrDatabaseConfiguration
	}
	database.SetMaxOpenConns(3)
	database.SetMaxIdleConns(1)
	database.SetConnMaxIdleTime(2 * time.Minute)
	database.SetConnMaxLifetime(15 * time.Minute)
	corrections, err := couponpostgres.NewCorrectionReconcilerWithGeneration(
		database, config.TenantID, config.GenerationID,
	)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidCouponReconciliationWorker
	}
	executor := sqlGenerationExecutor{db: database}
	contributions, err := contributionpostgres.NewReconcilerWithGeneration(database, config.TenantID, config.GenerationID)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidCouponReconciliationWorker
	}
	checkinTasks, err := NewCheckinCorrectionProcessor(database, config.TenantID, config.GenerationID)
	if err != nil {
		_ = database.Close()
		return nil, ErrInvalidCouponReconciliationWorker
	}
	return &CouponReconciliationWorker{
		database: database,
		grants: couponpostgres.NewGrantor(database,
			couponpostgres.GenerationBoundGrantPolicyProvider{
				TenantID: config.TenantID, GenerationID: config.GenerationID,
			}, nil),
		corrections:    corrections,
		contributions:  contributions,
		checkinTasks:   checkinTasks,
		generationGate: &PostgresGenerationGate{db: executor},
		schemaGate:     &postgresCouponReconciliationSchemaGate{db: executor},
		tenantID:       config.TenantID, generationID: config.GenerationID,
		pollInterval: CouponReconciliationPollInterval,
	}, nil
}

func CheckCouponReconciliationWorkerReady(
	ctx context.Context, config CouponReconciliationWorkerConfig,
) error {
	worker, err := NewCouponReconciliationWorker(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = worker.Close() }()
	return worker.Ready(ctx)
}

func (worker *CouponReconciliationWorker) Ready(ctx context.Context) error {
	if worker == nil || worker.generationGate == nil || worker.schemaGate == nil ||
		ctx == nil || worker.tenantID == uuid.Nil || worker.generationID == uuid.Nil {
		return ErrInvalidCouponReconciliationWorker
	}
	if err := worker.generationGate.CheckActiveGeneration(ctx, worker.tenantID, worker.generationID); err != nil {
		return err
	}
	return worker.schemaGate.CheckCouponReconciliationSchema(ctx)
}

func (worker *CouponReconciliationWorker) Run(ctx context.Context) error {
	if worker == nil || worker.grants == nil || worker.corrections == nil || worker.contributions == nil || worker.checkinTasks == nil ||
		worker.pollInterval <= 0 || ctx == nil {
		return ErrInvalidCouponReconciliationWorker
	}
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := worker.Ready(ctx); err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrGenerationInactive) {
				return nil
			}
			return err
		}
		taskCount, err := worker.checkinTasks.ProcessPending(ctx, couponReconciliationBatchLimit)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, ErrGenerationInactive) {
				return nil
			}
			return fmt.Errorf("process xiangwan Checkin corrections: %w", err)
		}
		contributionResults, err := worker.contributions.ReconcilePending(ctx, worker.tenantID, couponReconciliationBatchLimit)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, contributionpostgres.ErrContributionGenerationInactive) {
				return nil
			}
			return fmt.Errorf("reconcile xiangwan Contributions: %w", err)
		}
		corrected, err := worker.corrections.ReconcilePending(ctx, worker.tenantID, couponReconciliationBatchLimit)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, couponpostgres.ErrCouponGenerationInactive) {
				return nil
			}
			return fmt.Errorf("reconcile xiangwan Coupon corrections: %w", err)
		}
		granted, err := worker.grants.ReconcilePending(ctx, worker.tenantID, couponReconciliationBatchLimit)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, couponpostgres.ErrCouponGenerationInactive) {
				return nil
			}
			return fmt.Errorf("reconcile xiangwan Coupon grants: %w", err)
		}
		if taskCount == couponReconciliationBatchLimit || len(contributionResults) == couponReconciliationBatchLimit || len(corrected) == couponReconciliationBatchLimit || len(granted) == couponReconciliationBatchLimit {
			continue
		}
		timer := time.NewTimer(worker.pollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}

func (worker *CouponReconciliationWorker) Close() error {
	if worker == nil || worker.database == nil {
		return nil
	}
	return worker.database.Close()
}

type postgresCouponReconciliationSchemaGate struct{ db generationQueryExecutor }

func (gate *postgresCouponReconciliationSchemaGate) CheckCouponReconciliationSchema(ctx context.Context) error {
	if gate == nil || gate.db == nil || ctx == nil {
		return ErrInvalidCouponReconciliationWorker
	}
	var ready bool
	err := gate.db.queryRowContext(ctx, `
SELECT to_regclass('public.xiangwan_checkins') IS NOT NULL
   AND to_regclass('public.xiangwan_checkin_events') IS NOT NULL
   AND to_regclass('public.xiangwan_checkin_correction_outbox') IS NOT NULL
   AND to_regclass('public.xiangwan_contribution_entries') IS NOT NULL
   AND to_regclass('public.xiangwan_coupons') IS NOT NULL
   AND to_regclass('public.xiangwan_coupon_entries') IS NOT NULL
   AND to_regclass('public.xiangwan_coupon_grant_policy_versions') IS NOT NULL
`).Scan(&ready)
	if err != nil {
		return fmt.Errorf("check xiangwan Coupon reconciliation schema: %w", err)
	}
	if !ready {
		return ErrCouponReconciliationSchemaUnavailable
	}
	return nil
}
