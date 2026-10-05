package postgres

import (
	"context"
	"database/sql"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

func newAdminSessionCancellationHooks(tenantID uuid.UUID, authorize activitypostgres.InstanceCancellationAuthorization) *activitypostgres.SessionCancellationHooks {
	return &activitypostgres.SessionCancellationHooks{
		Authorize: authorize,
		Previewed: func(ctx context.Context, tx *sql.Tx, command activitypostgres.PreviewSessionCancellationCommand, preview activity.SessionCancellationPreview) error {
			digest, err := commandDigest(struct{ SessionID uuid.UUID }{command.SessionID})
			if err != nil {
				return err
			}
			return writeOperationAndAudit(ctx, tx, tenantID, command.RequestedBy, command.IdentityLinkID, uuid.MustParse(command.IdempotencyKey), "session.cancellation_preview", digest, preview, command.SessionID, preview.ExpectedSessionVersion, command.RequestID, preview.CreatedAt)
		},
		Cancelled: func(ctx context.Context, tx *sql.Tx, command activitypostgres.SessionCancellationCommand, result activitypostgres.SessionCancellationResult) error {
			digest, err := commandDigest(struct {
				SessionID       uuid.UUID
				PreviewID       uuid.UUID
				ExpectedVersion int64
				Reason          string
			}{command.SessionID, command.PreviewID, command.ExpectedSessionVersion, command.Reason})
			if err != nil {
				return err
			}
			return writeOperationAndAudit(ctx, tx, tenantID, command.ActorID, command.IdentityLinkID, uuid.MustParse(command.IdempotencyKey), "session.cancel", digest, result, command.SessionID, result.Session.Version, command.RequestID, result.Receipt.CreatedAt)
		},
	}
}

func (catalog *Catalog) PreviewSessionCancellation(ctx context.Context, command xiangwanadmin.PreviewSessionCancellationCommand) (activity.SessionCancellationPreview, error) {
	if !catalog.valid(ctx) || catalog.sessionCancellationPreviewer == nil || command.SessionID == uuid.Nil || !validWriteIdentity(command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID) {
		return activity.SessionCancellationPreview{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if err := catalog.authorizeCancellation(ctx, command.ActorID, command.IdentityLinkID); err != nil {
		return activity.SessionCancellationPreview{}, err
	}
	return catalog.sessionCancellationPreviewer.Preview(ctx, activitypostgres.PreviewSessionCancellationCommand{TenantID: catalog.tenantID, SessionID: command.SessionID, RequestedBy: command.ActorID, IdentityLinkID: command.IdentityLinkID, RequestID: command.RequestID, IdempotencyKey: command.OperationID.String()})
}

func (catalog *Catalog) CancelSession(ctx context.Context, command xiangwanadmin.CancelSessionCommand) (xiangwanadmin.SessionCancellationResult, error) {
	command.Reason = strings.TrimSpace(command.Reason)
	if !catalog.valid(ctx) || catalog.sessionCanceller == nil || command.SessionID == uuid.Nil || command.PreviewID == uuid.Nil || command.ExpectedSessionVersion < 1 || command.Reason == "" || len([]rune(command.Reason)) > 500 || !validWriteIdentity(command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID) {
		return xiangwanadmin.SessionCancellationResult{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if err := catalog.authorizeCancellation(ctx, command.ActorID, command.IdentityLinkID); err != nil {
		return xiangwanadmin.SessionCancellationResult{}, err
	}
	result, err := catalog.sessionCanceller.Cancel(ctx, activitypostgres.SessionCancellationCommand{TenantID: catalog.tenantID, SessionID: command.SessionID, PreviewID: command.PreviewID, ActorID: command.ActorID, IdentityLinkID: command.IdentityLinkID, RequestID: command.RequestID, ExpectedSessionVersion: command.ExpectedSessionVersion, IdempotencyKey: command.OperationID.String(), Reason: command.Reason, NotificationStrategy: activity.CancellationNotificationManualRequired})
	if err != nil {
		return xiangwanadmin.SessionCancellationResult{}, err
	}
	return xiangwanadmin.SessionCancellationResult{Session: result.Session, Receipt: result.Receipt}, nil
}
