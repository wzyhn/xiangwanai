package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"github.com/google/uuid"
)

// CreatePeopleProfile creates one draft PeopleProfile.  This command owns
// public content only; it never creates a Principal or a PeopleBinding.
func (catalog *Catalog) CreatePeopleProfile(
	ctx context.Context,
	command xiangwanadmin.CreatePeopleProfileCommand,
) (result xiangwanadmin.Person, resultErr error) {
	command.DisplayName = strings.TrimSpace(command.DisplayName)
	command.Headline = strings.TrimSpace(command.Headline)
	command.Introduction = strings.TrimSpace(command.Introduction)
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"people_profile.create", "people_profile", result.ID,
			command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.DisplayName == "" {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		DisplayName  string `json:"display_name"`
		Headline     string `json:"headline"`
		Introduction string `json:"introduction"`
	}{command.DisplayName, command.Headline, command.Introduction})
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.Person]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"people_profile.create", digest,
	); err != nil {
		return xiangwanadmin.Person{}, err
	} else if replay {
		if err := tx.Commit(); err != nil {
			return xiangwanadmin.Person{}, fmt.Errorf("commit PeopleProfile create replay: %w", err)
		}
		return receipt.Value, nil
	}
	now := catalog.now().UTC()
	profile, err := people.NewProfile(people.NewProfileCommand{
		TenantID: catalog.tenantID, DisplayName: command.DisplayName,
		Headline: command.Headline, Introduction: command.Introduction,
		ActorID: command.ActorID, At: now,
	})
	if err != nil {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	stored, err := peoplepostgres.NewRepository(tx).CreateProfile(ctx, profile)
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	result, err = projectAdminPeopleProfileResult(ctx, tx, stored)
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "people_profile.create", digest,
		operationResult[xiangwanadmin.Person]{Value: result}, result.ID,
		result.Version, command.RequestID, now,
	); err != nil {
		return xiangwanadmin.Person{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.Person{}, fmt.Errorf("commit PeopleProfile create: %w", err)
	}
	return result, nil
}

// UpdatePeopleProfile revises public content and consequently re-enters the
// pending moderation state.  Optimistic versioning and the domain transition
// prevent an edit from mutating an already archived profile or silently
// preserving an old approval.
func (catalog *Catalog) UpdatePeopleProfile(
	ctx context.Context,
	command xiangwanadmin.UpdatePeopleProfileCommand,
) (result xiangwanadmin.Person, resultErr error) {
	command.DisplayName = strings.TrimSpace(command.DisplayName)
	command.Headline = strings.TrimSpace(command.Headline)
	command.Introduction = strings.TrimSpace(command.Introduction)
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"people_profile.update", "people_profile", command.PeopleProfileID,
			command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.PeopleProfileID == uuid.Nil || command.ExpectedVersion < 1 ||
		command.DisplayName == "" {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		PeopleProfileID uuid.UUID `json:"people_profile_id"`
		ExpectedVersion int64     `json:"expected_version"`
		DisplayName     string    `json:"display_name"`
		Headline        string    `json:"headline"`
		Introduction    string    `json:"introduction"`
	}{command.PeopleProfileID, command.ExpectedVersion, command.DisplayName, command.Headline, command.Introduction})
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.Person]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"people_profile.update", digest,
	); err != nil {
		return xiangwanadmin.Person{}, err
	} else if replay {
		if err := tx.Commit(); err != nil {
			return xiangwanadmin.Person{}, fmt.Errorf("commit PeopleProfile update replay: %w", err)
		}
		return receipt.Value, nil
	}
	repository := peoplepostgres.NewRepository(tx)
	current, err := repository.GetProfileForUpdate(ctx, catalog.tenantID, command.PeopleProfileID)
	if errors.Is(err, peoplepostgres.ErrProfileNotFound) {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	if current.Version != command.ExpectedVersion {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrVersionConflict
	}
	now := catalog.now().UTC()
	next, err := people.ReviseProfile(current, people.ReviseProfileCommand{
		DisplayName: command.DisplayName, Headline: command.Headline,
		Introduction: command.Introduction, ActorID: command.ActorID, At: now,
	})
	if err != nil {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	stored, err := repository.UpdateProfile(ctx, next, command.ExpectedVersion)
	if errors.Is(err, peoplepostgres.ErrProfileVersionConflict) {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrVersionConflict
	}
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	result, err = projectAdminPeopleProfileResult(ctx, tx, stored)
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "people_profile.update", digest,
		operationResult[xiangwanadmin.Person]{Value: result}, result.ID,
		result.Version, command.RequestID, now,
	); err != nil {
		return xiangwanadmin.Person{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.Person{}, fmt.Errorf("commit PeopleProfile update: %w", err)
	}
	return result, nil
}

// ReviewPeopleProfile is the only path that moves a draft/pending profile to
// published/approved (or leaves it as a rejected draft).  The review command
// has its own idempotency receipt and expected-version fence.
func (catalog *Catalog) ReviewPeopleProfile(
	ctx context.Context,
	command xiangwanadmin.ReviewPeopleProfileCommand,
) (result xiangwanadmin.Person, resultErr error) {
	command.Decision = strings.TrimSpace(command.Decision)
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"people_profile.review", "people_profile", command.PeopleProfileID,
			command.RequestID, resultErr,
		)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.PeopleProfileID == uuid.Nil || command.ExpectedVersion < 1 ||
		(command.Decision != string(people.ModerationStatusApproved) &&
			command.Decision != string(people.ModerationStatusRejected)) {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		PeopleProfileID uuid.UUID `json:"people_profile_id"`
		ExpectedVersion int64     `json:"expected_version"`
		Decision        string    `json:"decision"`
	}{command.PeopleProfileID, command.ExpectedVersion, command.Decision})
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.Person]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"people_profile.review", digest,
	); err != nil {
		return xiangwanadmin.Person{}, err
	} else if replay {
		if err := tx.Commit(); err != nil {
			return xiangwanadmin.Person{}, fmt.Errorf("commit PeopleProfile review replay: %w", err)
		}
		return receipt.Value, nil
	}
	repository := peoplepostgres.NewRepository(tx)
	current, err := repository.GetProfileForUpdate(ctx, catalog.tenantID, command.PeopleProfileID)
	if errors.Is(err, peoplepostgres.ErrProfileNotFound) {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	if current.Version != command.ExpectedVersion {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrVersionConflict
	}
	now := catalog.now().UTC()
	next, err := people.ReviewProfile(current, people.ReviewProfileCommand{
		Decision: people.ModerationStatus(command.Decision),
		ActorID:  command.ActorID, At: now,
	})
	if err != nil {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	stored, err := repository.UpdateProfile(ctx, next, command.ExpectedVersion)
	if errors.Is(err, peoplepostgres.ErrProfileVersionConflict) {
		return xiangwanadmin.Person{}, xiangwanadmin.ErrVersionConflict
	}
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	result, err = projectAdminPeopleProfileResult(ctx, tx, stored)
	if err != nil {
		return xiangwanadmin.Person{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "people_profile.review", digest,
		operationResult[xiangwanadmin.Person]{Value: result}, result.ID,
		result.Version, command.RequestID, now,
	); err != nil {
		return xiangwanadmin.Person{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.Person{}, fmt.Errorf("commit PeopleProfile review: %w", err)
	}
	return result, nil
}

func projectAdminPeopleProfileResult(
	ctx context.Context,
	tx *activityWriteTransaction,
	value people.Profile,
) (xiangwanadmin.Person, error) {
	result := xiangwanadmin.Person{
		ID: value.ID, DisplayName: value.DisplayName, Headline: value.Headline,
		Introduction: value.Introduction, ProfileStatus: string(value.ProfileStatus),
		ModerationStatus: string(value.ModerationStatus), Version: value.Version,
		UpdatedAt: value.UpdatedAt,
	}
	var bindingID uuid.NullUUID
	if err := tx.QueryRowContext(ctx, `
SELECT id
FROM xiangwan_people_bindings
WHERE tenant_id = $1
  AND people_profile_id = $2
  AND binding_status = 'active'
`, value.TenantID, value.ID).Scan(&bindingID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.Person{}, fmt.Errorf("read PeopleProfile binding state: %w", err)
	}
	if bindingID.Valid {
		result.ActiveBindingID = &bindingID.UUID
	}
	return result, nil
}

// Keep this compile-time assertion close to the optional interface so a
// future Catalog implementation cannot silently lose profile writes.
var _ xiangwanadmin.PeopleProfileCatalog = (*Catalog)(nil)
