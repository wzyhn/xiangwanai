package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	contributionpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/contribution/postgres"
	couponpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon/postgres"
	"github.com/google/uuid"
)

var ErrCheckinCorrectionLeaseLost = errors.New("xiangwan Checkin correction lease is no longer current")

// CheckinCorrectionProcessor acknowledges an outbox task only after both
// append-only ledgers converge. Crashes leave a bounded lease; retries use the
// immutable Checkin source and cannot duplicate earnings or reversals.
type CheckinCorrectionProcessor struct {
	db                     *sql.DB
	tenantID, generationID uuid.UUID
	contributions          *contributionpostgres.Reconciler
	coupons                *couponpostgres.CorrectionReconciler
}
type checkinCorrectionLease struct {
	id, checkinID, token  uuid.UUID
	attempts, maxAttempts int
}

func NewCheckinCorrectionProcessor(db *sql.DB, tenantID, generationID uuid.UUID) (*CheckinCorrectionProcessor, error) {
	contributions, err := contributionpostgres.NewReconcilerWithGeneration(db, tenantID, generationID)
	if err != nil {
		return nil, err
	}
	coupons, err := couponpostgres.NewCorrectionReconcilerWithGeneration(db, tenantID, generationID)
	if err != nil {
		return nil, err
	}
	return &CheckinCorrectionProcessor{db: db, tenantID: tenantID, generationID: generationID, contributions: contributions, coupons: coupons}, nil
}

func (p *CheckinCorrectionProcessor) ProcessPending(ctx context.Context, limit int) (int, error) {
	if p == nil || p.db == nil || p.contributions == nil || p.coupons == nil || ctx == nil || limit < 1 || limit > 100 {
		return 0, ErrInvalidCouponReconciliationWorker
	}
	handled := 0
	for handled < limit {
		lease, found, err := p.claim(ctx)
		if err != nil {
			return handled, err
		}
		if !found {
			return handled, nil
		}
		workCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, contributionErr := p.contributions.Reconcile(workCtx, contributionpostgres.ReconcileCommand{TenantID: p.tenantID, CheckinID: lease.checkinID})
		errorClass := ""
		if contributionErr != nil {
			errorClass = "contribution_correction_failed"
		} else if _, err := p.coupons.Reconcile(workCtx, couponpostgres.CorrectionCommand{TenantID: p.tenantID, CheckinID: lease.checkinID}); err != nil {
			errorClass = "coupon_correction_failed"
		}
		cancel()
		finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = p.finish(finishCtx, lease, errorClass)
		finishCancel()
		if err != nil {
			return handled, err
		}
		handled++
		if ctx.Err() != nil {
			return handled, ctx.Err()
		}
	}
	return handled, nil
}

func (p *CheckinCorrectionProcessor) begin(ctx context.Context) (*sql.Tx, error) {
	tx, err := p.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	var epoch int64
	err = tx.QueryRowContext(ctx, `SELECT write_epoch FROM xiangwan_runtime_generations WHERE singleton_id=1 AND scope_key='wq-xiangwan' AND tenant_id=$1 AND active_generation_id=$2 AND write_epoch>0 AND bootstrap_completed_at IS NOT NULL FOR SHARE`, p.tenantID, p.generationID).Scan(&epoch)
	if err != nil {
		_ = tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrGenerationInactive
		}
		return nil, err
	}
	return tx, nil
}

func (p *CheckinCorrectionProcessor) claim(ctx context.Context) (checkinCorrectionLease, bool, error) {
	tx, err := p.begin(ctx)
	if err != nil {
		return checkinCorrectionLease{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Expired processing leases transition through pending before being claimed
	// again, respecting the existing outbox progress guard and attempt limit.
	if _, err := tx.ExecContext(ctx, `UPDATE xiangwan_checkin_correction_outbox SET outbox_status=CASE WHEN attempt_count>=max_attempts THEN 'dead_letter' ELSE 'pending' END, lease_token=NULL,lease_expires_at=NULL,last_error_class='lease_expired',available_at=clock_timestamp()+INTERVAL '1 minute',version=version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND outbox_status='processing' AND lease_expires_at<=clock_timestamp()`, p.tenantID); err != nil {
		return checkinCorrectionLease{}, false, err
	}
	lease := checkinCorrectionLease{token: uuid.New()}
	err = tx.QueryRowContext(ctx, `WITH candidate AS (SELECT id FROM xiangwan_checkin_correction_outbox WHERE tenant_id=$1 AND outbox_status='pending' AND available_at<=clock_timestamp() AND attempt_count<max_attempts ORDER BY available_at,created_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE xiangwan_checkin_correction_outbox AS task SET outbox_status='processing',attempt_count=attempt_count+1,lease_token=$2,lease_expires_at=clock_timestamp()+INTERVAL '5 minutes',last_error_class=NULL,version=version+1,updated_at=clock_timestamp() FROM candidate WHERE task.id=candidate.id AND task.tenant_id=$1 RETURNING task.id,task.checkin_id,task.attempt_count,task.max_attempts`, p.tenantID, lease.token).Scan(&lease.id, &lease.checkinID, &lease.attempts, &lease.maxAttempts)
	found := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return checkinCorrectionLease{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return checkinCorrectionLease{}, false, err
	}
	return lease, found, nil
}

func (p *CheckinCorrectionProcessor) finish(ctx context.Context, lease checkinCorrectionLease, errorClass string) error {
	tx, err := p.begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	status := "completed"
	if errorClass != "" {
		status = "pending"
		if lease.attempts >= lease.maxAttempts {
			status = "dead_letter"
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE xiangwan_checkin_correction_outbox SET outbox_status=$4::varchar,lease_token=NULL,lease_expires_at=NULL,last_error_class=NULLIF($5,''),completed_at=CASE WHEN $4::varchar='completed' THEN clock_timestamp() ELSE NULL END,available_at=CASE WHEN $4::varchar='pending' THEN clock_timestamp()+make_interval(secs=>LEAST(3600,power(2,LEAST(attempt_count,12)))::int) ELSE available_at END,version=version+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND id=$2 AND outbox_status='processing' AND lease_token=$3 AND lease_expires_at>clock_timestamp()`, p.tenantID, lease.id, lease.token, status, errorClass)
	if err != nil {
		return fmt.Errorf("finish Checkin correction task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrCheckinCorrectionLeaseLost
	}
	return tx.Commit()
}
