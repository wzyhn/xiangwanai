// Package resource owns Xiangwan bindings between shared Content and an exact
// Activity Instance or Session. It does not own Content or File storage.
package resource

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

const MaxResourceSortOrder = 10_000

type RelationKind string

const (
	RelationKindInstanceReview   RelationKind = "instance_review"
	RelationKindSessionResources RelationKind = "session_resources"
)

type AccessPolicy string

const (
	AccessPolicyPublic                AccessPolicy = "public"
	AccessPolicyConfirmedRegistration AccessPolicy = "confirmed_registration"
)

type Relation struct {
	ID                    uuid.UUID
	TenantID              uuid.UUID
	SeriesID              uuid.UUID
	InstanceID            uuid.UUID
	SessionID             *uuid.UUID
	Kind                  RelationKind
	ContentID             uuid.UUID
	ContentRevision       time.Time
	AccessPolicy          AccessPolicy
	SortOrder             int
	ExpectedTargetVersion int64
	CreatedBy             uuid.UUID
	IdempotencyKey        string
	CreatedAt             time.Time
}

type CreateDraftCommand struct {
	TenantID              uuid.UUID
	SeriesID              uuid.UUID
	InstanceID            uuid.UUID
	SessionID             *uuid.UUID
	Kind                  RelationKind
	ContentID             uuid.UUID
	ContentRevision       time.Time
	AccessPolicy          AccessPolicy
	SortOrder             int
	ExpectedTargetVersion int64
	CreatedBy             uuid.UUID
	IdempotencyKey        string
	CreatedAt             time.Time
}

var (
	ErrInvalidRelation        = errors.New("invalid xiangwan ResourceRelation")
	ErrRelationIntentConflict = errors.New(
		"xiangwan ResourceRelation idempotency intent conflict",
	)
)

var idempotencyKeyPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`,
)

func NewDraft(command CreateDraftCommand) (Relation, error) {
	value := Relation{
		ID:                    uuid.New(),
		TenantID:              command.TenantID,
		SeriesID:              command.SeriesID,
		InstanceID:            command.InstanceID,
		SessionID:             cloneUUID(command.SessionID),
		Kind:                  command.Kind,
		ContentID:             command.ContentID,
		ContentRevision:       command.ContentRevision.UTC(),
		AccessPolicy:          command.AccessPolicy,
		SortOrder:             command.SortOrder,
		ExpectedTargetVersion: command.ExpectedTargetVersion,
		CreatedBy:             command.CreatedBy,
		IdempotencyKey:        command.IdempotencyKey,
		CreatedAt:             command.CreatedAt.UTC(),
	}
	if err := ValidateRelation(value); err != nil {
		return Relation{}, err
	}
	return value, nil
}

func ValidateRelation(value Relation) error {
	if value.ID == uuid.Nil || value.TenantID == uuid.Nil ||
		value.SeriesID == uuid.Nil || value.InstanceID == uuid.Nil ||
		value.ContentID == uuid.Nil || value.ContentRevision.IsZero() ||
		value.SortOrder < 0 || value.SortOrder > MaxResourceSortOrder ||
		value.ExpectedTargetVersion < 1 || value.CreatedBy == uuid.Nil ||
		!idempotencyKeyPattern.MatchString(value.IdempotencyKey) ||
		value.CreatedAt.IsZero() || value.ContentRevision.After(value.CreatedAt) {
		return ErrInvalidRelation
	}
	switch value.Kind {
	case RelationKindInstanceReview:
		if value.SessionID != nil || value.AccessPolicy != AccessPolicyPublic {
			return ErrInvalidRelation
		}
	case RelationKindSessionResources:
		if value.SessionID == nil || *value.SessionID == uuid.Nil ||
			(value.AccessPolicy != AccessPolicyPublic &&
				value.AccessPolicy != AccessPolicyConfirmedRegistration) {
			return ErrInvalidRelation
		}
	default:
		return ErrInvalidRelation
	}
	return nil
}

func SameCreateIntent(left, right Relation) bool {
	if ValidateRelation(left) != nil || ValidateRelation(right) != nil {
		return false
	}
	return left.TenantID == right.TenantID &&
		left.SeriesID == right.SeriesID &&
		left.InstanceID == right.InstanceID &&
		equalUUID(left.SessionID, right.SessionID) &&
		left.Kind == right.Kind &&
		left.ContentID == right.ContentID &&
		left.ContentRevision.Equal(right.ContentRevision) &&
		left.AccessPolicy == right.AccessPolicy &&
		left.SortOrder == right.SortOrder &&
		left.ExpectedTargetVersion == right.ExpectedTargetVersion &&
		left.CreatedBy == right.CreatedBy &&
		left.IdempotencyKey == right.IdempotencyKey
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func equalUUID(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
