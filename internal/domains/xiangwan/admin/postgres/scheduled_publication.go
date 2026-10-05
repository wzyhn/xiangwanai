package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func (catalog *Catalog) ScheduleInstancePublication(
	ctx context.Context,
	command xiangwanadmin.SchedulePublicationCommand,
) (result xiangwanadmin.ScheduledPublication, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"instance.publish.schedule", "instance", command.InstanceID,
			command.RequestID, resultErr,
		)
	}()
	now := catalog.now().UTC()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.ExpectedInstanceVersion < 1 ||
		command.ScheduledAt.IsZero() {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		InstanceID              uuid.UUID `json:"instance_id"`
		ExpectedInstanceVersion int64     `json:"expected_instance_version"`
		ScheduledAt             time.Time `json:"scheduled_at"`
	}{command.InstanceID, command.ExpectedInstanceVersion, command.ScheduledAt.UTC()})
	if err != nil {
		return result, err
	}
	tx, err := catalog.beginActivityWrite(ctx, command.ActorID, command.IdentityLinkID, command.OperationID)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, readErr := readOperation[operationResult[xiangwanadmin.ScheduledPublication]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"instance.publish.schedule", digest,
	); readErr != nil {
		return result, readErr
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.InstanceID != command.InstanceID {
			return result, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return result, fmt.Errorf("commit scheduled publication replay: %w", err)
		}
		return receipt.Value, nil
	}
	if !command.ScheduledAt.UTC().After(now) {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	var status activity.InstanceStatus
	var version int64
	err = tx.QueryRowContext(ctx, `
SELECT status, version
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.InstanceID).Scan(&status, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return result, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return result, fmt.Errorf("lock Instance for scheduled publication: %w", err)
	}
	if (status != activity.InstanceStatusDraft && status != activity.InstanceStatusPendingPublish) ||
		version != command.ExpectedInstanceVersion {
		return result, xiangwanadmin.ErrVersionConflict
	}
	scheduledAt := command.ScheduledAt.UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_instances
SET status = 'pending_publish', scheduled_at = $3, version = version + 1, updated_at = $3
WHERE tenant_id = $1 AND id = $2 AND version = $4
`, catalog.tenantID, command.InstanceID, scheduledAt, command.ExpectedInstanceVersion); err != nil {
		return result, fmt.Errorf("mark Instance scheduled for publication: %w", err)
	}
	var leaseExpires sql.NullTime
	err = tx.QueryRowContext(ctx, `
INSERT INTO xiangwan_scheduled_publications (
    tenant_id, instance_id, actor_id, identity_link_id,
    expected_instance_version, scheduled_at, status, attempt_count,
    next_attempt_at, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, 'pending', 0, $6, $7, $7)
RETURNING id, status, attempt_count, lease_expires_at, completed_at, version
`, catalog.tenantID, command.InstanceID, command.ActorID, command.IdentityLinkID,
		command.ExpectedInstanceVersion+1, scheduledAt, now).Scan(
		&result.ID, &result.Status, &result.AttemptCount, &leaseExpires,
		&result.CompletedAt, &result.Version,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return result, xiangwanadmin.ErrVersionConflict
		}
		return result, fmt.Errorf("insert scheduled publication: %w", err)
	}
	result.InstanceID = command.InstanceID
	result.ExpectedInstanceVersion = command.ExpectedInstanceVersion + 1
	result.ScheduledAt = scheduledAt
	if leaseExpires.Valid {
		value := leaseExpires.Time
		result.LeaseExpiresAt = &value
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "instance.publish.schedule", digest,
		operationResult[xiangwanadmin.ScheduledPublication]{Value: result},
		command.InstanceID, result.Version, command.RequestID, now,
	); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit scheduled publication: %w", err)
	}
	return result, nil
}

func (catalog *Catalog) ListInstancePublicationSchedules(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	instanceID uuid.UUID,
) (result []xiangwanadmin.ScheduledPublication, resultErr error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || instanceID == uuid.Nil {
		return nil, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM xiangwan_activity_instances
    WHERE tenant_id = $1 AND id = $2
)
`, catalog.tenantID, instanceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check scheduled publication instance: %w", err)
	}
	if !exists {
		return nil, xiangwanadmin.ErrTargetNotFound
	}
	rows, err := tx.QueryContext(ctx, `
SELECT id, instance_id, expected_instance_version, scheduled_at, status,
       attempt_count, lease_expires_at, completed_at, last_error, version
FROM xiangwan_scheduled_publications
WHERE tenant_id = $1 AND instance_id = $2
ORDER BY created_at DESC, id DESC
`, catalog.tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list scheduled publications: %w", err)
	}
	defer rows.Close()
	result = make([]xiangwanadmin.ScheduledPublication, 0)
	for rows.Next() {
		var value xiangwanadmin.ScheduledPublication
		var leaseExpires, completed sql.NullTime
		if err := rows.Scan(
			&value.ID, &value.InstanceID, &value.ExpectedInstanceVersion,
			&value.ScheduledAt, &value.Status, &value.AttemptCount,
			&leaseExpires, &completed, &value.LastError, &value.Version,
		); err != nil {
			return nil, fmt.Errorf("scan scheduled publication: %w", err)
		}
		if leaseExpires.Valid {
			value.LeaseExpiresAt = &leaseExpires.Time
		}
		if completed.Valid {
			value.CompletedAt = &completed.Time
		}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled publications: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit scheduled publication read: %w", err)
	}
	return result, nil
}

func (catalog *Catalog) CancelScheduledPublication(
	ctx context.Context,
	command xiangwanadmin.CancelScheduledPublicationCommand,
) (result xiangwanadmin.ScheduledPublication, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"instance.publish.schedule.cancel", "instance", command.InstanceID,
			command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.ScheduleID == uuid.Nil {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		InstanceID uuid.UUID `json:"instance_id"`
		ScheduleID uuid.UUID `json:"schedule_id"`
	}{command.InstanceID, command.ScheduleID})
	if err != nil {
		return result, err
	}
	tx, err := catalog.beginActivityWrite(ctx, command.ActorID, command.IdentityLinkID, command.OperationID)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, readErr := readOperation[operationResult[xiangwanadmin.ScheduledPublication]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"instance.publish.schedule.cancel", digest,
	); readErr != nil {
		return result, readErr
	} else if replay {
		if receipt.Value.ID != command.ScheduleID || receipt.Value.InstanceID != command.InstanceID ||
			receipt.Value.Status != "cancelled" {
			return result, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return result, fmt.Errorf("commit scheduled publication cancellation replay: %w", err)
		}
		return receipt.Value, nil
	}
	var (
		instanceID, actorID, identityLinkID uuid.UUID
		expectedVersion, taskVersion        int64
		scheduledAt                         time.Time
		status                              string
		attemptCount                        int
		leaseExpires, completed             sql.NullTime
		lastError                           string
	)
	err = tx.QueryRowContext(ctx, `
SELECT instance_id, actor_id, identity_link_id, expected_instance_version,
       scheduled_at, status, attempt_count, lease_expires_at, completed_at,
       last_error, version
FROM xiangwan_scheduled_publications
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.ScheduleID).Scan(
		&instanceID, &actorID, &identityLinkID, &expectedVersion, &scheduledAt,
		&status, &attemptCount, &leaseExpires, &completed, &lastError, &taskVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return result, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return result, fmt.Errorf("lock scheduled publication for cancellation: %w", err)
	}
	if instanceID != command.InstanceID || status != "pending" {
		return result, xiangwanadmin.ErrVersionConflict
	}
	var instanceStatus activity.InstanceStatus
	var instanceScheduledAt sql.NullTime
	var instanceVersion int64
	err = tx.QueryRowContext(ctx, `
SELECT status, scheduled_at, version
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.InstanceID).Scan(&instanceStatus, &instanceScheduledAt, &instanceVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return result, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return result, fmt.Errorf("lock instance for scheduled publication cancellation: %w", err)
	}
	if instanceStatus != activity.InstanceStatusPendingPublish || !instanceScheduledAt.Valid ||
		!instanceScheduledAt.Time.Equal(scheduledAt) || instanceVersion != expectedVersion {
		return result, xiangwanadmin.ErrVersionConflict
	}
	now := catalog.now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE xiangwan_scheduled_publications
SET status = 'cancelled', next_attempt_at = $3, last_error = $4,
    lease_token = NULL, lease_expires_at = NULL, updated_at = $3,
    version = version + 1
WHERE tenant_id = $1 AND id = $2 AND status = 'pending'
`, catalog.tenantID, command.ScheduleID, now, "cancelled by administrator"); err != nil {
		return result, fmt.Errorf("cancel scheduled publication: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_instances
SET scheduled_at = NULL, version = version + 1, updated_at = $3
WHERE tenant_id = $1 AND id = $2 AND status = 'pending_publish' AND version = $4
`, catalog.tenantID, command.InstanceID, now, instanceVersion); err != nil {
		return result, fmt.Errorf("clear instance publication schedule: %w", err)
	}
	result = xiangwanadmin.ScheduledPublication{
		ID: command.ScheduleID, InstanceID: command.InstanceID,
		ExpectedInstanceVersion: expectedVersion, ScheduledAt: scheduledAt,
		Status: "cancelled", AttemptCount: attemptCount,
		Version: taskVersion + 1, LastError: "cancelled by administrator",
	}
	if leaseExpires.Valid {
		value := leaseExpires.Time
		result.LeaseExpiresAt = &value
	}
	if completed.Valid {
		value := completed.Time
		result.CompletedAt = &value
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "instance.publish.schedule.cancel", digest,
		operationResult[xiangwanadmin.ScheduledPublication]{Value: result},
		command.InstanceID, instanceVersion+1, command.RequestID, now,
	); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("commit scheduled publication cancellation: %w", err)
	}
	return result, nil
}
