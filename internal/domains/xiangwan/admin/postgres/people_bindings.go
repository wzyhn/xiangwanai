package postgres

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"
)

// The existing customer session key is purpose-separated. Only the authorized
// response derives plaintext. Issued codes remain bounded by expiry/revocation;
// after key rotation the old plaintext cannot be regenerated from its receipt.
func (catalog *Catalog) SetPeopleBindingInvitationKey(key []byte) error {
	if catalog == nil || len(key) < 32 {
		return xiangwanadmin.ErrInvalidCatalogRequest
	}
	catalog.bindingInvitationKey = append([]byte(nil), key...)
	return nil
}
func (catalog *Catalog) peopleInvitationCode(id uuid.UUID) string {
	mac := hmac.New(sha256.New, catalog.bindingInvitationKey)
	_, _ = mac.Write([]byte("xiangwan:people-binding-invitation:v1:" + catalog.tenantID.String() + ":" + id.String()))
	return id.String() + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (catalog *Catalog) CreatePeopleBindingInvitation(ctx context.Context, c xiangwanadmin.CreatePeopleBindingInvitationCommand) (result xiangwanadmin.PeopleBindingInvitation, resultErr error) {
	c.Reason = strings.TrimSpace(c.Reason)
	defer func() {
		resultErr = catalog.auditRejectedCommand(ctx, c.ActorID, c.IdentityLinkID, c.OperationID, "people_binding.invite", "people_profile", c.PeopleProfileID, c.RequestID, resultErr)
	}()
	if !catalog.valid(ctx) || len(catalog.bindingInvitationKey) < 32 || !validWriteIdentity(c.ActorID, c.IdentityLinkID, c.OperationID, c.RequestID) || c.PeopleProfileID == uuid.Nil || c.ExpectedVersion < 1 || c.Reason == "" || len([]rune(c.Reason)) > 500 {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		ProfileID uuid.UUID
		Version   int64
		Reason    string
	}{c.PeopleProfileID, c.ExpectedVersion, c.Reason})
	if err != nil {
		return result, err
	}
	tx, err := catalog.beginActivityWrite(ctx, c.ActorID, c.IdentityLinkID, c.OperationID)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.RequireSuperAdminIdentity(ctx, tx.Tx, c.ActorID, c.IdentityLinkID); err != nil {
		return result, err
	}
	code := catalog.peopleInvitationCode(c.OperationID)
	codeDigest := sha256.Sum256([]byte(code))
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.PeopleBindingInvitation]](ctx, tx, catalog.tenantID, c.ActorID, c.OperationID, "people_binding.invite", digest); err != nil {
		return result, err
	} else if replay {
		result = receipt.Value
		var storedDigest []byte
		if err := tx.QueryRowContext(ctx, `SELECT invitation_status,version,code_digest FROM xiangwan_people_binding_invitations WHERE tenant_id=$1 AND id=$2`, catalog.tenantID, result.ID).Scan(&result.Status, &result.Version, &storedDigest); err != nil {
			return result, err
		}
		if result.Status == "pending" && catalog.now().UTC().Before(result.ExpiresAt) {
			if !hmac.Equal(storedDigest, codeDigest[:]) {
				return result, xiangwanadmin.ErrOperationConflict
			}
			result.Code = code
		}
		return result, tx.Commit()
	}
	profile, err := peoplepostgres.NewRepository(tx).GetProfileForUpdate(ctx, catalog.tenantID, c.PeopleProfileID)
	if errors.Is(err, peoplepostgres.ErrProfileNotFound) {
		return result, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return result, err
	}
	if profile.Version != c.ExpectedVersion || string(profile.ProfileStatus) != "published" || string(profile.ModerationStatus) != "approved" {
		return result, xiangwanadmin.ErrVersionConflict
	}
	if _, err := peoplepostgres.NewRepository(tx).GetActiveBindingByProfileForUpdate(ctx, catalog.tenantID, c.PeopleProfileID); err == nil {
		return result, xiangwanadmin.ErrVersionConflict
	} else if !errors.Is(err, peoplepostgres.ErrBindingNotFound) {
		return result, err
	}
	now := catalog.now().UTC()
	if _, err := tx.ExecContext(ctx, `UPDATE xiangwan_people_binding_invitations SET invitation_status='revoked',revoked_by=$3,revocation_reason='被新的本人确认邀请替代',revoked_at=$4,version=2,updated_at=$4 WHERE tenant_id=$1 AND people_profile_id=$2 AND invitation_status='pending'`, catalog.tenantID, c.PeopleProfileID, c.ActorID, now); err != nil {
		return result, err
	}
	result = xiangwanadmin.PeopleBindingInvitation{ID: c.OperationID, PeopleProfileID: c.PeopleProfileID, ProfileVersion: profile.Version, Status: "pending", Version: 1, ExpiresAt: now.Add(24 * time.Hour)}
	if _, err := tx.ExecContext(ctx, `INSERT INTO xiangwan_people_binding_invitations(id,tenant_id,people_profile_id,profile_version,code_digest,invited_by,identity_link_id,reason,expires_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, result.ID, catalog.tenantID, c.PeopleProfileID, profile.Version, codeDigest[:], c.ActorID, c.IdentityLinkID, c.Reason, result.ExpiresAt, now); err != nil {
		return result, err
	}
	if err := writeOperationAndAudit(ctx, tx, catalog.tenantID, c.ActorID, c.IdentityLinkID, c.OperationID, "people_binding.invite", digest, operationResult[xiangwanadmin.PeopleBindingInvitation]{Value: result}, result.ID, result.Version, c.RequestID, now); err != nil {
		return result, err
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	result.Code = code
	return result, nil
}
func (catalog *Catalog) GetPeopleBinding(ctx context.Context, p xiangwanadmin.Principal, profileID uuid.UUID, requestID string) (result xiangwanadmin.PeopleBindingDetail, err error) {
	if !catalog.valid(ctx) || profileID == uuid.Nil || requestID == "" {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, p)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.RequireSuperAdminIdentity(ctx, tx, p.PrincipalID, p.IdentityLinkID); err != nil {
		return result, err
	}
	b, err := peoplepostgres.NewRepository(tx).GetActiveBindingByProfile(ctx, catalog.tenantID, profileID)
	if errors.Is(err, peoplepostgres.ErrBindingNotFound) {
		return result, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return result, err
	}
	result = xiangwanadmin.PeopleBindingDetail{ID: b.ID, PeopleProfileID: b.PeopleProfileID, Status: string(b.BindingStatus), Version: b.Version}
	if _, err := tx.ExecContext(ctx, `INSERT INTO xiangwan_admin_audit_events(id,tenant_id,actor_id,action_code,target_type,target_id,request_id,details,occurred_at,created_at) VALUES($1,$2,$3,'people_binding.read','people_binding',$4,$5,'{}',clock_timestamp(),clock_timestamp())`, uuid.New(), catalog.tenantID, p.PrincipalID, b.ID, requestID); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (catalog *Catalog) RevokePeopleBinding(ctx context.Context, c xiangwanadmin.RevokePeopleBindingCommand) (result xiangwanadmin.PeopleBindingDetail, resultErr error) {
	c.Reason = strings.TrimSpace(c.Reason)
	defer func() {
		resultErr = catalog.auditRejectedCommand(ctx, c.ActorID, c.IdentityLinkID, c.OperationID, "people_binding.revoke", "people_profile", c.PeopleProfileID, c.RequestID, resultErr)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(c.ActorID, c.IdentityLinkID, c.OperationID, c.RequestID) || c.PeopleProfileID == uuid.Nil || c.BindingID == uuid.Nil || c.ExpectedVersion < 1 || c.Reason == "" || len([]rune(c.Reason)) > 500 {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		ProfileID, BindingID uuid.UUID
		Version              int64
		Reason               string
	}{c.PeopleProfileID, c.BindingID, c.ExpectedVersion, c.Reason})
	if err != nil {
		return result, err
	}
	tx, err := catalog.beginActivityWrite(ctx, c.ActorID, c.IdentityLinkID, c.OperationID)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.RequireSuperAdminIdentity(ctx, tx.Tx, c.ActorID, c.IdentityLinkID); err != nil {
		return result, err
	}
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.PeopleBindingDetail]](ctx, tx, catalog.tenantID, c.ActorID, c.OperationID, "people_binding.revoke", digest); err != nil {
		return result, err
	} else if replay {
		return receipt.Value, tx.Commit()
	}
	b, err := peoplepostgres.NewRepository(tx).GetActiveBindingByProfileForUpdate(ctx, catalog.tenantID, c.PeopleProfileID)
	if errors.Is(err, peoplepostgres.ErrBindingNotFound) {
		return result, xiangwanadmin.ErrVersionConflict
	}
	if err != nil {
		return result, err
	}
	if b.ID != c.BindingID || b.Version != c.ExpectedVersion {
		return result, xiangwanadmin.ErrVersionConflict
	}
	now := catalog.now().UTC()
	updated, err := people.RevokeBinding(b, people.RevokeBindingCommand{ActorID: c.ActorID, Reason: c.Reason, At: now})
	if err != nil {
		return result, err
	}
	updated, err = peoplepostgres.NewRepository(tx).RevokeBinding(ctx, updated, b.Version)
	if err != nil {
		return result, err
	}
	result = xiangwanadmin.PeopleBindingDetail{ID: updated.ID, PeopleProfileID: updated.PeopleProfileID, Status: string(updated.BindingStatus), Version: updated.Version}
	if err := writeOperationAndAudit(ctx, tx, catalog.tenantID, c.ActorID, c.IdentityLinkID, c.OperationID, "people_binding.revoke", digest, operationResult[xiangwanadmin.PeopleBindingDetail]{Value: result}, result.ID, result.Version, c.RequestID, now); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

var _ xiangwanadmin.PeopleBindingAdministration = (*Catalog)(nil)

// BindingID names the exact invitation for this command; it never selects a user.
func (catalog *Catalog) RevokePeopleBindingInvitation(ctx context.Context, c xiangwanadmin.RevokePeopleBindingCommand) (result xiangwanadmin.PeopleBindingInvitation, resultErr error) {
	c.Reason = strings.TrimSpace(c.Reason)
	defer func() {
		resultErr = catalog.auditRejectedCommand(ctx, c.ActorID, c.IdentityLinkID, c.OperationID, "people_binding.invitation_revoke", "people_profile", c.PeopleProfileID, c.RequestID, resultErr)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(c.ActorID, c.IdentityLinkID, c.OperationID, c.RequestID) || c.PeopleProfileID == uuid.Nil || c.BindingID == uuid.Nil || c.ExpectedVersion != 1 || c.Reason == "" || len([]rune(c.Reason)) > 500 {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		ProfileID, InvitationID uuid.UUID
		Version                 int64
		Reason                  string
	}{c.PeopleProfileID, c.BindingID, c.ExpectedVersion, c.Reason})
	if err != nil {
		return result, err
	}
	tx, err := catalog.beginActivityWrite(ctx, c.ActorID, c.IdentityLinkID, c.OperationID)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.RequireSuperAdminIdentity(ctx, tx.Tx, c.ActorID, c.IdentityLinkID); err != nil {
		return result, err
	}
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.PeopleBindingInvitation]](ctx, tx, catalog.tenantID, c.ActorID, c.OperationID, "people_binding.invitation_revoke", digest); err != nil {
		return result, err
	} else if replay {
		return receipt.Value, tx.Commit()
	}
	result.ID = c.BindingID
	result.PeopleProfileID = c.PeopleProfileID
	now := catalog.now().UTC()
	if err := tx.QueryRowContext(ctx, `UPDATE xiangwan_people_binding_invitations SET invitation_status='revoked',revoked_by=$3,revocation_reason=$4,revoked_at=$5,version=2,updated_at=$5 WHERE tenant_id=$1 AND id=$2 AND people_profile_id=$6 AND version=$7 AND invitation_status='pending' RETURNING profile_version,invitation_status,version,expires_at`, catalog.tenantID, c.BindingID, c.ActorID, c.Reason, now, c.PeopleProfileID, c.ExpectedVersion).Scan(&result.ProfileVersion, &result.Status, &result.Version, &result.ExpiresAt); errors.Is(err, sql.ErrNoRows) {
		return result, xiangwanadmin.ErrVersionConflict
	} else if err != nil {
		return result, err
	}
	if err := writeOperationAndAudit(ctx, tx, catalog.tenantID, c.ActorID, c.IdentityLinkID, c.OperationID, "people_binding.invitation_revoke", digest, operationResult[xiangwanadmin.PeopleBindingInvitation]{Value: result}, result.ID, result.Version, c.RequestID, now); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
