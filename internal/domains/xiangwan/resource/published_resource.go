package resource

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const MaxPublishedResourcesPerContext = 200

type PublishedResource struct {
	PublicationID         uuid.UUID
	RelationID            uuid.UUID
	TenantID              uuid.UUID
	SeriesID              uuid.UUID
	InstanceID            uuid.UUID
	SessionID             *uuid.UUID
	Kind                  RelationKind
	ContentID             uuid.UUID
	ContentRevision       time.Time
	AccessPolicy          AccessPolicy
	SortOrder             int
	TargetVersion         int64
	ApprovalObservationID uuid.UUID
	SnapshotSchema        string
	SubjectDigest         Digest
	PublishedAt           time.Time
}

var ErrInvalidPublishedResource = errors.New(
	"invalid xiangwan published resource",
)

func ValidatePublishedResource(value PublishedResource) error {
	if value.PublicationID == uuid.Nil || value.RelationID == uuid.Nil ||
		value.TenantID == uuid.Nil || value.SeriesID == uuid.Nil ||
		value.InstanceID == uuid.Nil || value.ContentID == uuid.Nil ||
		value.ContentRevision.IsZero() || value.SortOrder < 0 ||
		value.SortOrder > MaxResourceSortOrder || value.TargetVersion < 1 ||
		value.ApprovalObservationID == uuid.Nil ||
		value.SnapshotSchema != ContentSnapshotSchema ||
		zeroDigest(value.SubjectDigest) || value.PublishedAt.IsZero() ||
		value.ContentRevision.After(value.PublishedAt) {
		return ErrInvalidPublishedResource
	}
	switch value.Kind {
	case RelationKindInstanceReview:
		if value.SessionID != nil || value.AccessPolicy != AccessPolicyPublic {
			return ErrInvalidPublishedResource
		}
	case RelationKindSessionResources:
		if value.SessionID == nil || *value.SessionID == uuid.Nil ||
			!validPublicationAccessPolicy(value.AccessPolicy) {
			return ErrInvalidPublishedResource
		}
	default:
		return ErrInvalidPublishedResource
	}
	return nil
}
