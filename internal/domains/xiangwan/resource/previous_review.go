package resource

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MaxPreviousReviewImages caps the image preview of the previous Instance on
// the public Session detail page.
const MaxPreviousReviewImages = 3

// PreviousInstanceFacts identifies the selected previous completed/archived
// Instance of the same Series.
type PreviousInstanceFacts struct {
	InstanceID  uuid.UUID
	Title       string
	CompletedAt time.Time
}

// PreviousReviewImageFacts is one available image Block of the previous
// Instance's published review documents. The triple (RelationID, BlockID,
// FileID) is exactly what the public media route re-authenticates.
type PreviousReviewImageFacts struct {
	RelationID        uuid.UUID
	DocumentSortOrder int
	BlockID           uuid.UUID
	BlockSortOrder    int
	FileID            uuid.UUID
	ExternalURL       string
}

// PreviousReviewFacts carries everything the public Session detail needs to
// project the previous-Instance review preview.
type PreviousReviewFacts struct {
	Instance           *PreviousInstanceFacts
	HasPublishedReview bool
	Images             []PreviousReviewImageFacts
}

// PreviousReviewImage is one selected preview image.
type PreviousReviewImage struct {
	RelationID  uuid.UUID
	BlockID     uuid.UUID
	FileID      uuid.UUID
	ExternalURL string
}

// PreviousInstanceReview is the public preview of the previous Instance's
// published review on a Session detail page.
type PreviousInstanceReview struct {
	InstanceID  uuid.UUID
	SessionID   *uuid.UUID
	Title       string
	CompletedAt time.Time
	Images      []PreviousReviewImage
}

// ProjectPreviousInstanceReview selects the preview of the previous
// Instance's published review: images are ordered by document then block
// position, duplicate File or approved URL sources collapse, and at most MaxPreviousReviewImages
// survive. A published review remains a valid preview when it has no usable
// image Blocks (for example, a video-channel link); the caller receives the
// previous Instance identity with an empty Images slice so it can still open
// the full review page.
func ProjectPreviousInstanceReview(
	facts PreviousReviewFacts,
) *PreviousInstanceReview {
	return ProjectPreviousInstanceReviewWithPolicy(facts, ExternalDomainPolicy{})
}

// ProjectPreviousInstanceReviewWithPolicy also projects administrator-supplied
// HTTPS images after the current external-domain policy has approved them.
func ProjectPreviousInstanceReviewWithPolicy(
	facts PreviousReviewFacts,
	externalDomains ExternalDomainPolicy,
) *PreviousInstanceReview {
	if facts.Instance == nil || facts.Instance.InstanceID == uuid.Nil ||
		facts.Instance.CompletedAt.IsZero() {
		return nil
	}
	ordered := make([]PreviousReviewImageFacts, 0, len(facts.Images))
	seenBlocks := make(map[uuid.UUID]struct{}, len(facts.Images))
	for _, image := range facts.Images {
		if image.RelationID == uuid.Nil || image.BlockID == uuid.Nil ||
			image.DocumentSortOrder < 0 || image.BlockSortOrder < 0 ||
			(image.FileID != uuid.Nil && image.ExternalURL != "") {
			continue
		}
		if image.FileID == uuid.Nil {
			allowedURL, allowed := externalDomains.AllowURL(image.ExternalURL)
			if !allowed {
				continue
			}
			image.ExternalURL = allowedURL
		}
		if _, duplicate := seenBlocks[image.BlockID]; duplicate {
			continue
		}
		seenBlocks[image.BlockID] = struct{}{}
		ordered = append(ordered, image)
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].DocumentSortOrder != ordered[right].DocumentSortOrder {
			return ordered[left].DocumentSortOrder < ordered[right].DocumentSortOrder
		}
		if ordered[left].RelationID != ordered[right].RelationID {
			return ordered[left].RelationID.String() < ordered[right].RelationID.String()
		}
		if ordered[left].BlockSortOrder != ordered[right].BlockSortOrder {
			return ordered[left].BlockSortOrder < ordered[right].BlockSortOrder
		}
		return ordered[left].BlockID.String() < ordered[right].BlockID.String()
	})
	images := make([]PreviousReviewImage, 0, MaxPreviousReviewImages)
	seenSources := make(map[string]struct{}, len(ordered))
	for _, image := range ordered {
		source := "file:" + image.FileID.String()
		if image.FileID == uuid.Nil {
			source = "url:" + image.ExternalURL
		}
		if _, duplicate := seenSources[source]; duplicate {
			continue
		}
		seenSources[source] = struct{}{}
		images = append(images, PreviousReviewImage{
			RelationID:  image.RelationID,
			BlockID:     image.BlockID,
			FileID:      image.FileID,
			ExternalURL: image.ExternalURL,
		})
		if len(images) == MaxPreviousReviewImages {
			break
		}
	}
	if len(images) == 0 && !facts.HasPublishedReview {
		return nil
	}
	return &PreviousInstanceReview{
		InstanceID:  facts.Instance.InstanceID,
		Title:       strings.TrimSpace(facts.Instance.Title),
		CompletedAt: facts.Instance.CompletedAt.UTC(),
		Images:      images,
	}
}
