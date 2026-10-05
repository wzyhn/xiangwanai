package xiangwanruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

const (
	DefaultScheduledPublicationLease       = 2 * time.Minute
	DefaultScheduledPublicationMaxAttempts = 25
)

// ScheduledPublicationTask contains only the immutable facts captured by the
// admin schedule command. The publisher callback must use these exact facts;
// it must not re-read an operator or infer a new expected version.
type ScheduledPublicationTask struct {
	ID                      uuid.UUID
	TenantID                uuid.UUID
	InstanceID              uuid.UUID
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	ExpectedInstanceVersion int64
	ScheduledAt             time.Time
	AttemptCount            int
	LeaseToken              uuid.UUID
}

// PublishScheduledPublication supplies the canonical command mapping used by
// the worker adapter. A deterministic operation key makes a lease retry safe
// even if the publisher committed immediately before the worker lost its
// response.
func PublishScheduledPublication(
	ctx context.Context,
	task ScheduledPublicationTask,
	publish func(context.Context, xiangwanadmin.PublishInstanceCommand) error,
) error {
	if publish == nil || task.ID == uuid.Nil || task.ActorID == uuid.Nil ||
		task.IdentityLinkID == uuid.Nil || task.InstanceID == uuid.Nil ||
		task.ExpectedInstanceVersion < 1 || task.AttemptCount < 1 {
		return errors.New("invalid scheduled publication task")
	}
	// The operation key is task-scoped, not attempt-scoped: a worker can lose
	// the response after PublishInstance commits, and the next lease must read
	// that exact receipt instead of issuing a second publication command.
	operationID := uuid.NewSHA1(task.ID, []byte("publication"))
	return publish(ctx, xiangwanadmin.PublishInstanceCommand{
		ActorID: task.ActorID, IdentityLinkID: task.IdentityLinkID,
		OperationID: operationID,
		RequestID:   fmt.Sprintf("scheduled-publication:%s", task.ID),
		InstanceID:  task.InstanceID, ExpectedInstanceVersion: task.ExpectedInstanceVersion,
	})
}

type ScheduledPublicationWorker struct {
	DB           *sql.DB
	TenantID     uuid.UUID
	GenerationID uuid.UUID
	Lease        time.Duration
	MaxAttempts  int
	Now          func() time.Time
	GenerationOK func(context.Context) error
}

func (worker ScheduledPublicationWorker) valid() bool {
	return worker.DB != nil && worker.TenantID != uuid.Nil
}

// RunOnce claims at most one due task, applies the normal publication command
// outside the queue transaction, then records completion or a bounded retry.
// Expired processing leases are reclaimed by the same claim query.
func (worker ScheduledPublicationWorker) RunOnce(
	ctx context.Context,
	apply func(context.Context, ScheduledPublicationTask) error,
) (bool, error) {
	if !worker.valid() || apply == nil {
		return false, errors.New("invalid scheduled publication worker")
	}
	if worker.Now == nil {
		worker.Now = time.Now
	}
	if worker.Lease <= 0 {
		worker.Lease = DefaultScheduledPublicationLease
	}
	if worker.MaxAttempts <= 0 {
		worker.MaxAttempts = DefaultScheduledPublicationMaxAttempts
	}
	if worker.GenerationOK != nil {
		if err := worker.GenerationOK(ctx); err != nil {
			return false, err
		}
	}
	now := worker.Now().UTC()
	tx, err := worker.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, fmt.Errorf("begin scheduled publication claim: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	leaseToken := uuid.New()
	leaseExpires := now.Add(worker.Lease)
	var task ScheduledPublicationTask
	err = tx.QueryRowContext(ctx, `
WITH candidate AS (
    SELECT id
    FROM xiangwan_scheduled_publications
    WHERE tenant_id = $1
      AND (
          (status = 'pending' AND next_attempt_at <= $2)
          OR (status = 'processing' AND lease_expires_at <= $2)
      )
    ORDER BY scheduled_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE xiangwan_scheduled_publications AS scheduled
SET status = 'processing', lease_token = $3, lease_expires_at = $4,
    attempt_count = scheduled.attempt_count + 1,
    version = scheduled.version + 1, updated_at = $2
FROM candidate
WHERE scheduled.id = candidate.id
RETURNING scheduled.id, scheduled.tenant_id, scheduled.instance_id,
          scheduled.actor_id, scheduled.identity_link_id,
          scheduled.expected_instance_version, scheduled.scheduled_at,
          scheduled.attempt_count, scheduled.lease_token
`, worker.TenantID, now, leaseToken, leaseExpires).Scan(
		&task.ID, &task.TenantID, &task.InstanceID, &task.ActorID,
		&task.IdentityLinkID, &task.ExpectedInstanceVersion, &task.ScheduledAt,
		&task.AttemptCount, &task.LeaseToken,
	)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim scheduled publication: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit scheduled publication claim: %w", err)
	}
	applyErr := apply(ctx, task)
	if applyErr == nil {
		if err := worker.finish(ctx, task, now); err != nil {
			return true, err
		}
		return true, nil
	}
	if err := worker.fail(ctx, task, now, applyErr); err != nil {
		return true, errors.Join(applyErr, err)
	}
	return true, applyErr
}

func (worker ScheduledPublicationWorker) finish(
	ctx context.Context, task ScheduledPublicationTask, now time.Time,
) error {
	tx, err := worker.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin scheduled publication completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := worker.checkGenerationTx(ctx, tx); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE xiangwan_scheduled_publications
SET status = 'completed', completed_at = $4, lease_token = NULL,
    lease_expires_at = NULL, updated_at = $4, version = version + 1
WHERE tenant_id = $1 AND id = $2 AND status = 'processing' AND lease_token = $3
`, worker.TenantID, task.ID, task.LeaseToken, now.UTC())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit scheduled publication completion: %w", err)
	}
	return nil
}

func (worker ScheduledPublicationWorker) fail(
	ctx context.Context, task ScheduledPublicationTask, now time.Time, applyErr error,
) error {
	message := strings.TrimSpace(applyErr.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	terminal := task.AttemptCount >= worker.MaxAttempts
	nextStatus := "pending"
	var nextAt any = now.UTC().Add(retryDelay(task.AttemptCount))
	if terminal {
		nextStatus = "failed"
		nextAt = now.UTC()
	}
	tx, err := worker.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin scheduled publication failure: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := worker.checkGenerationTx(ctx, tx); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
UPDATE xiangwan_scheduled_publications
SET status = $4, next_attempt_at = $5, last_error = $6,
    lease_token = NULL, lease_expires_at = NULL, updated_at = $7,
    version = version + 1
WHERE tenant_id = $1 AND id = $2 AND status = 'processing' AND lease_token = $3
`, worker.TenantID, task.ID, task.LeaseToken, nextStatus, nextAt, message, now.UTC())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit scheduled publication failure: %w", err)
	}
	return nil
}

func (worker ScheduledPublicationWorker) checkGenerationTx(ctx context.Context, tx *sql.Tx) error {
	if worker.GenerationID == uuid.Nil {
		return nil
	}
	var active bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM xiangwan_runtime_generations
    WHERE singleton_id = 1 AND scope_key = 'wq-xiangwan'
      AND tenant_id = $1 AND active_generation_id = $2
      AND write_epoch > 0 AND bootstrap_completed_at IS NOT NULL
    FOR SHARE
)
`, worker.TenantID, worker.GenerationID).Scan(&active); err != nil {
		return fmt.Errorf("check scheduled publication generation: %w", err)
	}
	if !active {
		return fmt.Errorf("scheduled publication worker generation is no longer active")
	}
	return nil
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	return time.Duration(1<<uint(attempt-1)) * time.Minute
}
