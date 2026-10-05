package resource

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

type Publication struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	RelationID            uuid.UUID
	ContentID             uuid.UUID
	ContentRevision       time.Time
	ApprovalObservationID uuid.UUID
	AccessPolicy          AccessPolicy
	ExpectedTargetVersion int64
	PublishedBy           uuid.UUID
	IdempotencyKey        string
	PublishedAt           time.Time
}

type PublishCommand struct {
	TenantID              uuid.UUID
	RelationID            uuid.UUID
	ContentID             uuid.UUID
	ContentRevision       time.Time
	ApprovalObservationID uuid.UUID
	AccessPolicy          AccessPolicy
	ExpectedTargetVersion int64
	PublishedBy           uuid.UUID
	IdempotencyKey        string
	PublishedAt           time.Time
}

var (
	ErrInvalidPublication = errors.New(
		"invalid xiangwan resource publication",
	)
	ErrPublicationIntentConflict = errors.New(
		"xiangwan resource publication idempotency intent conflict",
	)
)

func NewPublication(command PublishCommand) (Publication, error) {
	value := Publication{
		ID:                    uuid.New(),
		TenantID:              command.TenantID,
		RelationID:            command.RelationID,
		ContentID:             command.ContentID,
		ContentRevision:       command.ContentRevision.UTC(),
		ApprovalObservationID: command.ApprovalObservationID,
		AccessPolicy:          command.AccessPolicy,
		ExpectedTargetVersion: command.ExpectedTargetVersion,
		PublishedBy:           command.PublishedBy,
		IdempotencyKey:        command.IdempotencyKey,
		PublishedAt:           command.PublishedAt.UTC(),
	}
	if err := ValidatePublication(value); err != nil {
		return Publication{}, err
	}
	return value, nil
}

func ValidatePublication(value Publication) error {
	if value.ID == uuid.Nil || value.TenantID == uuid.Nil ||
		value.RelationID == uuid.Nil || value.ContentID == uuid.Nil ||
		value.ContentRevision.IsZero() ||
		value.ApprovalObservationID == uuid.Nil ||
		!validPublicationAccessPolicy(value.AccessPolicy) ||
		value.ExpectedTargetVersion < 1 || value.PublishedBy == uuid.Nil ||
		!idempotencyKeyPattern.MatchString(value.IdempotencyKey) ||
		value.PublishedAt.IsZero() ||
		value.ContentRevision.After(value.PublishedAt) {
		return ErrInvalidPublication
	}
	return nil
}

func SamePublicationIntent(left, right Publication) bool {
	if ValidatePublication(left) != nil || ValidatePublication(right) != nil {
		return false
	}
	return left.TenantID == right.TenantID &&
		left.RelationID == right.RelationID &&
		left.ContentID == right.ContentID &&
		left.ContentRevision.Equal(right.ContentRevision) &&
		left.ApprovalObservationID == right.ApprovalObservationID &&
		left.AccessPolicy == right.AccessPolicy &&
		left.ExpectedTargetVersion == right.ExpectedTargetVersion &&
		left.PublishedBy == right.PublishedBy &&
		left.IdempotencyKey == right.IdempotencyKey
}

func validPublicationAccessPolicy(value AccessPolicy) bool {
	return value == AccessPolicyPublic ||
		value == AccessPolicyConfirmedRegistration
}
