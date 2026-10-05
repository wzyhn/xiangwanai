package resource

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

type PublicFileGrant struct {
	TenantID   uuid.UUID
	RelationID uuid.UUID
	ContentID  uuid.UUID
	BlockID    uuid.UUID
	FileID     uuid.UUID
	SeriesID   uuid.UUID
	InstanceID uuid.UUID
	SessionID  *uuid.UUID
	Kind       RelationKind
	BlockType  PublicReviewBlockType
	MIME       string
	Size       int64
}

var ErrInvalidPublicFileGrant = errors.New(
	"invalid xiangwan public file grant",
)

var ErrPublicFileUnavailable = errors.New(
	"xiangwan public file is unavailable",
)

func ValidatePublicFileGrant(value PublicFileGrant) error {
	if value.TenantID == uuid.Nil || value.RelationID == uuid.Nil ||
		value.ContentID == uuid.Nil || value.BlockID == uuid.Nil ||
		value.FileID == uuid.Nil || value.SeriesID == uuid.Nil ||
		value.InstanceID == uuid.Nil || value.Size <= 0 ||
		!publicReviewMIMEAllowed(
			value.BlockType,
			strings.ToLower(strings.TrimSpace(value.MIME)),
		) {
		return ErrInvalidPublicFileGrant
	}
	switch value.Kind {
	case RelationKindInstanceReview:
		if value.SessionID != nil {
			return ErrInvalidPublicFileGrant
		}
	case RelationKindSessionResources:
		if value.SessionID == nil || *value.SessionID == uuid.Nil {
			return ErrInvalidPublicFileGrant
		}
	default:
		return ErrInvalidPublicFileGrant
	}
	return nil
}
