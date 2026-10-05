package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	checkinpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin/postgres"
	"github.com/google/uuid"
)

type adminCheckinRevocationAuthorizer struct {
	tenantID   uuid.UUID
	authorizer *GrantAuthorizer
}

func (a adminCheckinRevocationAuthorizer) AuthorizeRevokeCheckin(ctx context.Context, query checkinpostgres.OperatorAuthorizationQuery, request checkinpostgres.RevokeCheckinAuthorization) error {
	if request.TenantID != a.tenantID || request.IdentityLinkID == uuid.Nil {
		return checkinpostgres.ErrCheckinRevocationForbidden
	}
	err := a.authorizer.RequireSuperAdminIdentity(ctx, query, request.ActorID, request.IdentityLinkID)
	if errors.Is(err, xiangwanadmin.ErrScopeForbidden) {
		return checkinpostgres.ErrCheckinRevocationForbidden
	}
	return err
}

func newAdminCheckinRevoker(db *sql.DB, tenantID, generationID uuid.UUID, authorizer *GrantAuthorizer) *checkinpostgres.Revoker {
	return checkinpostgres.NewRevokerWithAudit(db, adminCheckinRevocationAuthorizer{tenantID, authorizer}, generationID,
		func(ctx context.Context, tx *sql.Tx, command checkinpostgres.RevokeCheckinCommand, result checkinpostgres.RevokeCheckinResult) error {
			operationID, err := uuid.Parse(command.IdempotencyKey)
			if err != nil {
				return xiangwanadmin.ErrInvalidCatalogRequest
			}
			digest, err := commandDigest(struct {
				RegistrationID, CheckinID uuid.UUID
				ExpectedVersion           int64
				Reason                    string
			}{command.RegistrationID, command.CheckinID, command.ExpectedVersion, command.Reason})
			if err != nil {
				return err
			}
			return writeOperationAndAudit(ctx, tx, tenantID, command.ActorID, command.IdentityLinkID, operationID, "checkin.revoke", digest, xiangwanadmin.CheckinRevocationResult{Checkin: result.Checkin, EventID: result.Event.ID}, command.CheckinID, result.Checkin.Version, command.RequestID, result.Event.OccurredAt)
		})
}

func (catalog *Catalog) GetRegistrationCheckin(ctx context.Context, principal xiangwanadmin.Principal, registrationID uuid.UUID, requestID string) (checkin.Checkin, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil || principal.IdentityLinkID == uuid.Nil || registrationID == uuid.Nil || requestID == "" {
		return checkin.Checkin{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return checkin.Checkin{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.RequireSuperAdminIdentity(ctx, tx, principal.PrincipalID, principal.IdentityLinkID); err != nil {
		return checkin.Checkin{}, err
	}
	value, err := checkinpostgres.NewRepository(tx).GetByRegistration(ctx, catalog.tenantID, registrationID)
	if errors.Is(err, checkinpostgres.ErrCheckinNotFound) {
		return checkin.Checkin{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return checkin.Checkin{}, err
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `INSERT INTO xiangwan_admin_audit_events (id,tenant_id,actor_id,action_code,target_type,target_id,request_id,details,occurred_at,created_at) VALUES ($1,$2,$3,'checkin.admin_read','checkin',$4,$5,jsonb_build_object('identity_link_id',$6::text),$7,$7)`, uuid.New(), catalog.tenantID, principal.PrincipalID, value.ID, requestID, principal.IdentityLinkID.String(), now); err != nil {
		return checkin.Checkin{}, err
	}
	if err := tx.Commit(); err != nil {
		return checkin.Checkin{}, err
	}
	return value, nil
}

func (catalog *Catalog) RevokeRegistrationCheckin(ctx context.Context, command xiangwanadmin.RevokeCheckinCommand) (xiangwanadmin.CheckinRevocationResult, error) {
	command.Reason = strings.TrimSpace(command.Reason)
	if !catalog.valid(ctx) || catalog.checkinRevoker == nil || command.RegistrationID == uuid.Nil || command.CheckinID == uuid.Nil || command.ExpectedVersion < 1 || command.Reason == "" || len([]rune(command.Reason)) > 500 || !validWriteIdentity(command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID) {
		return xiangwanadmin.CheckinRevocationResult{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	// Resolve immutable scope from an authorized server read, never client hierarchy IDs.
	current, err := catalog.GetRegistrationCheckin(ctx, xiangwanadmin.Principal{PrincipalID: command.ActorID, IdentityLinkID: command.IdentityLinkID}, command.RegistrationID, command.RequestID)
	if err != nil {
		return xiangwanadmin.CheckinRevocationResult{}, err
	}
	if current.ID != command.CheckinID {
		return xiangwanadmin.CheckinRevocationResult{}, xiangwanadmin.ErrVersionConflict
	}
	result, err := catalog.checkinRevoker.Revoke(ctx, checkinpostgres.RevokeCheckinCommand{TenantID: catalog.tenantID, SeriesID: current.SeriesID, InstanceID: current.InstanceID, SessionID: current.SessionID, RegistrationID: current.RegistrationID, CheckinID: current.ID, ActorID: command.ActorID, IdentityLinkID: command.IdentityLinkID, RequestID: command.RequestID, ExpectedVersion: command.ExpectedVersion, Reason: command.Reason, IdempotencyKey: command.OperationID.String()})
	if err != nil {
		return xiangwanadmin.CheckinRevocationResult{}, err
	}
	return xiangwanadmin.CheckinRevocationResult{Checkin: result.Checkin, EventID: result.Event.ID, Duplicate: result.Duplicate}, nil
}
