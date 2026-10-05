package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

// ArchiveInstance moves only a completed Instance to the explicit archived
// lifecycle. It never rewrites publication, completion, registration, order,
// refund, or Session history.
func (catalog *Catalog) ArchiveInstance(
	ctx context.Context,
	command xiangwanadmin.ArchiveInstanceCommand,
) (result activity.Instance, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"instance.archive", "instance", command.InstanceID, command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil || command.ExpectedInstanceVersion < 1 {
		return activity.Instance{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		InstanceID              uuid.UUID `json:"instance_id"`
		ExpectedInstanceVersion int64     `json:"expected_instance_version"`
	}{command.InstanceID, command.ExpectedInstanceVersion})
	if err != nil {
		return activity.Instance{}, err
	}
	tx, err := catalog.beginActivityWrite(ctx, command.ActorID, command.IdentityLinkID, command.OperationID)
	if err != nil {
		return activity.Instance{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[activity.Instance]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID, "instance.archive", digest,
	); err != nil {
		return activity.Instance{}, err
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.TenantID != catalog.tenantID ||
			receipt.Value.Status != activity.InstanceStatusArchived || receipt.Value.Version < 1 {
			return activity.Instance{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.Instance{}, fmt.Errorf("commit admin Instance archive replay: %w", err)
		}
		return receipt.Value, nil
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
		return activity.Instance{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return activity.Instance{}, fmt.Errorf("lock admin Instance for archive: %w", err)
	}
	if version != command.ExpectedInstanceVersion {
		return activity.Instance{}, xiangwanadmin.ErrVersionConflict
	}
	if status != activity.InstanceStatusCompleted {
		return activity.Instance{}, xiangwanadmin.ErrArchiveNotAllowed
	}
	now := catalog.now().UTC()
	update, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_instances
SET status = 'archived', version = version + 1, updated_at = $3
WHERE tenant_id = $1 AND id = $2 AND status = 'completed' AND version = $4
`, catalog.tenantID, command.InstanceID, now, command.ExpectedInstanceVersion)
	if err != nil {
		return activity.Instance{}, fmt.Errorf("archive admin Instance: %w", err)
	}
	if rows, err := update.RowsAffected(); err != nil {
		return activity.Instance{}, fmt.Errorf("read archived admin Instance count: %w", err)
	} else if rows != 1 {
		return activity.Instance{}, xiangwanadmin.ErrVersionConflict
	}
	value, err := activitypostgres.NewRepository(tx).GetInstance(ctx, catalog.tenantID, command.InstanceID)
	if err != nil {
		return activity.Instance{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "instance.archive", digest,
		operationResult[activity.Instance]{Value: value}, value.ID, value.Version,
		command.RequestID, now,
	); err != nil {
		return activity.Instance{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.Instance{}, fmt.Errorf("commit admin Instance archive: %w", err)
	}
	return value, nil
}

// ArchiveSession moves only an ended Session to archived. Cancelled Sessions
// retain their cancellation receipt and remain immutable; operators archive
// the completed parent Instance when they need to close the whole period.
func (catalog *Catalog) ArchiveSession(
	ctx context.Context,
	command xiangwanadmin.ArchiveSessionCommand,
) (result activity.Session, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"session.archive", "session", command.SessionID, command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.SessionID == uuid.Nil || command.ExpectedSessionVersion < 1 {
		return activity.Session{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		SessionID              uuid.UUID `json:"session_id"`
		ExpectedSessionVersion int64     `json:"expected_session_version"`
	}{command.SessionID, command.ExpectedSessionVersion})
	if err != nil {
		return activity.Session{}, err
	}
	tx, err := catalog.beginActivityWrite(ctx, command.ActorID, command.IdentityLinkID, command.OperationID)
	if err != nil {
		return activity.Session{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[activity.Session]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID, "session.archive", digest,
	); err != nil {
		return activity.Session{}, err
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.TenantID != catalog.tenantID ||
			receipt.Value.Status != activity.SessionStatusArchived || receipt.Value.Version < 1 {
			return activity.Session{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.Session{}, fmt.Errorf("commit admin Session archive replay: %w", err)
		}
		return receipt.Value, nil
	}
	var status activity.SessionStatus
	var version int64
	err = tx.QueryRowContext(ctx, `
SELECT status, version
FROM xiangwan_activity_sessions
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.SessionID).Scan(&status, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Session{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return activity.Session{}, fmt.Errorf("lock admin Session for archive: %w", err)
	}
	if version != command.ExpectedSessionVersion {
		return activity.Session{}, xiangwanadmin.ErrVersionConflict
	}
	if status != activity.SessionStatusEnded {
		return activity.Session{}, xiangwanadmin.ErrArchiveNotAllowed
	}
	now := catalog.now().UTC()
	update, err := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_sessions
SET status = 'archived', version = version + 1, updated_at = $3
WHERE tenant_id = $1 AND id = $2 AND status = 'ended' AND version = $4
`, catalog.tenantID, command.SessionID, now, command.ExpectedSessionVersion)
	if err != nil {
		return activity.Session{}, fmt.Errorf("archive admin Session: %w", err)
	}
	if rows, err := update.RowsAffected(); err != nil {
		return activity.Session{}, fmt.Errorf("read archived admin Session count: %w", err)
	} else if rows != 1 {
		return activity.Session{}, xiangwanadmin.ErrVersionConflict
	}
	value, err := activitypostgres.NewRepository(tx).GetSession(ctx, catalog.tenantID, command.SessionID)
	if err != nil {
		return activity.Session{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "session.archive", digest,
		operationResult[activity.Session]{Value: value}, value.ID, value.Version,
		command.RequestID, now,
	); err != nil {
		return activity.Session{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.Session{}, fmt.Errorf("commit admin Session archive: %w", err)
	}
	return value, nil
}
