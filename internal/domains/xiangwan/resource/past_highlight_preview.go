package resource

import (
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const MaxPastHighlightPreviewPhotos = 3

type PastHighlightPhotoReference struct {
	RelationID        uuid.UUID
	ContentID         uuid.UUID
	BlockID           uuid.UUID
	FileID            uuid.UUID
	SeriesID          uuid.UUID
	InstanceID        uuid.UUID
	SessionID         *uuid.UUID
	Kind              RelationKind
	MIME              string
	RelationSortOrder int
	BlockSortOrder    int
}

type PastHighlightReviewTarget struct {
	SeriesID   uuid.UUID
	InstanceID uuid.UUID
	SessionID  *uuid.UUID
}

type PastHighlightPreview struct {
	Context    PastHighlightContext
	Photos     []PastHighlightPhotoReference
	MoreTarget PastHighlightReviewTarget
}

var ErrInvalidPastHighlightPreviewFacts = errors.New(
	"invalid xiangwan past-highlight preview facts",
)

func BuildPastHighlightPreview(
	context PastHighlightContext,
	photos []PastHighlightPhotoReference,
) (PastHighlightPreview, error) {
	if !validPastHighlightContext(context) {
		return PastHighlightPreview{}, ErrInvalidPastHighlightPreviewFacts
	}
	ordered := append([]PastHighlightPhotoReference(nil), photos...)
	seenBlocks := make(map[uuid.UUID]struct{}, len(ordered))
	for _, photo := range ordered {
		if !validPastHighlightPhoto(context, photo) {
			return PastHighlightPreview{}, ErrInvalidPastHighlightPreviewFacts
		}
		if _, duplicate := seenBlocks[photo.BlockID]; duplicate {
			return PastHighlightPreview{}, ErrInvalidPastHighlightPreviewFacts
		}
		seenBlocks[photo.BlockID] = struct{}{}
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		leftScope := pastHighlightPhotoScopeOrder(ordered[left].Kind)
		rightScope := pastHighlightPhotoScopeOrder(ordered[right].Kind)
		if leftScope != rightScope {
			return leftScope < rightScope
		}
		if ordered[left].RelationSortOrder != ordered[right].RelationSortOrder {
			return ordered[left].RelationSortOrder < ordered[right].RelationSortOrder
		}
		if ordered[left].BlockSortOrder != ordered[right].BlockSortOrder {
			return ordered[left].BlockSortOrder < ordered[right].BlockSortOrder
		}
		return ordered[left].BlockID.String() < ordered[right].BlockID.String()
	})
	selected := make([]PastHighlightPhotoReference, 0, MaxPastHighlightPreviewPhotos)
	seenFiles := make(map[uuid.UUID]struct{}, len(ordered))
	for _, photo := range ordered {
		if _, duplicate := seenFiles[photo.FileID]; duplicate {
			continue
		}
		seenFiles[photo.FileID] = struct{}{}
		selected = append(selected, photo)
		if len(selected) == MaxPastHighlightPreviewPhotos {
			break
		}
	}
	return PastHighlightPreview{
		Context: context,
		Photos:  selected,
		MoreTarget: PastHighlightReviewTarget{
			SeriesID:   context.SeriesID,
			InstanceID: context.PreviousInstanceID,
			SessionID:  copyPastHighlightUUID(context.FeaturedSessionID),
		},
	}, nil
}

func validPastHighlightContext(value PastHighlightContext) bool {
	return value.SeriesID != uuid.Nil && value.AnchorInstanceID != uuid.Nil &&
		value.PreviousInstanceID != uuid.Nil &&
		value.AnchorInstanceID != value.PreviousInstanceID &&
		!value.PreviousInstancePublishedAt.IsZero() &&
		!value.PreviousInstanceCompletedAt.IsZero() &&
		!value.PreviousInstancePublishedAt.After(value.PreviousInstanceCompletedAt) &&
		(value.FeaturedSessionID == nil || *value.FeaturedSessionID != uuid.Nil)
}

func validPastHighlightPhoto(
	context PastHighlightContext,
	value PastHighlightPhotoReference,
) bool {
	if value.RelationID == uuid.Nil || value.ContentID == uuid.Nil ||
		value.BlockID == uuid.Nil || value.FileID == uuid.Nil ||
		value.SeriesID != context.SeriesID ||
		value.InstanceID != context.PreviousInstanceID ||
		value.RelationSortOrder < 0 || value.BlockSortOrder < 0 ||
		!strings.HasPrefix(strings.ToLower(strings.TrimSpace(value.MIME)), "image/") {
		return false
	}
	switch value.Kind {
	case RelationKindInstanceReview:
		return value.SessionID == nil
	case RelationKindSessionResources:
		return context.FeaturedSessionID != nil && value.SessionID != nil &&
			*value.SessionID == *context.FeaturedSessionID
	default:
		return false
	}
}

func pastHighlightPhotoScopeOrder(kind RelationKind) int {
	if kind == RelationKindInstanceReview {
		return 0
	}
	return 1
}

func copyPastHighlightUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}
