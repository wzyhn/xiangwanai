package resource

import (
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const (
	MaxPublicReviewDocuments = 50
	MaxPublicReviewBlocks    = 500
)

type PublicReviewBlockType string

const (
	PublicReviewBlockTypeText  PublicReviewBlockType = "text"
	PublicReviewBlockTypeImage PublicReviewBlockType = "image"
	PublicReviewBlockTypeLink  PublicReviewBlockType = "link"
	PublicReviewBlockTypeFile  PublicReviewBlockType = "file"
	PublicReviewBlockTypeVideo PublicReviewBlockType = "video"
	PublicReviewBlockTypeAudio PublicReviewBlockType = "audio"
)

type PublicReviewBlockAvailability string

const (
	PublicReviewBlockAvailable     PublicReviewBlockAvailability = "available"
	PublicReviewBlockUnavailable   PublicReviewBlockAvailability = "unavailable"
	PublicReviewBlockPolicyBlocked PublicReviewBlockAvailability = "policy_blocked"
)

type PublicReviewBlockFacts struct {
	BlockID      uuid.UUID
	Type         PublicReviewBlockType
	SortOrder    int
	Text         string
	Label        string
	Subtitle     string
	ExternalURL  string
	VideoChannel *ReviewVideoChannel
	FileID       *uuid.UUID
	MIME         string
	IsCover      bool
}

type PublicReviewDocumentFacts struct {
	RelationID        uuid.UUID
	ContentID         uuid.UUID
	SeriesID          uuid.UUID
	InstanceID        uuid.UUID
	SessionID         *uuid.UUID
	Kind              RelationKind
	Title             string
	RelationSortOrder int
	Blocks            []PublicReviewBlockFacts
}

type PublicReviewBlock struct {
	BlockID      uuid.UUID
	Type         PublicReviewBlockType
	SortOrder    int
	Text         string
	Label        string
	Subtitle     string
	ExternalURL  string
	VideoChannel *ReviewVideoChannel
	FileID       *uuid.UUID
	MIME         string
	Availability PublicReviewBlockAvailability
	IsCover      bool
}

type PublicReviewDocument struct {
	RelationID uuid.UUID
	ContentID  uuid.UUID
	SessionID  *uuid.UUID
	Kind       RelationKind
	Title      string
	SortOrder  int
	Blocks     []PublicReviewBlock
}

type PublicReviewDetail struct {
	Target    PastHighlightReviewTarget
	Documents []PublicReviewDocument
}

var ErrInvalidPublicReviewFacts = errors.New(
	"invalid xiangwan public review facts",
)

func ProjectPublicReview(
	target PastHighlightReviewTarget,
	facts []PublicReviewDocumentFacts,
	externalDomains ExternalDomainPolicy,
) (PublicReviewDetail, error) {
	if !validPublicReviewTarget(target) ||
		len(facts) > MaxPublicReviewDocuments {
		return PublicReviewDetail{}, ErrInvalidPublicReviewFacts
	}
	ordered := append([]PublicReviewDocumentFacts(nil), facts...)
	seenRelations := make(map[uuid.UUID]struct{}, len(ordered))
	seenContents := make(map[uuid.UUID]struct{}, len(ordered))
	seenBlocks := make(map[uuid.UUID]struct{})
	blockCount := 0
	for _, document := range ordered {
		if !validPublicReviewDocument(target, document) {
			return PublicReviewDetail{}, ErrInvalidPublicReviewFacts
		}
		if _, duplicate := seenRelations[document.RelationID]; duplicate {
			return PublicReviewDetail{}, ErrInvalidPublicReviewFacts
		}
		seenRelations[document.RelationID] = struct{}{}
		if _, duplicate := seenContents[document.ContentID]; duplicate {
			return PublicReviewDetail{}, ErrInvalidPublicReviewFacts
		}
		seenContents[document.ContentID] = struct{}{}
		for _, block := range document.Blocks {
			blockCount++
			if blockCount > MaxPublicReviewBlocks ||
				!validPublicReviewBlockFacts(block) {
				return PublicReviewDetail{}, ErrInvalidPublicReviewFacts
			}
			if _, duplicate := seenBlocks[block.BlockID]; duplicate {
				return PublicReviewDetail{}, ErrInvalidPublicReviewFacts
			}
			seenBlocks[block.BlockID] = struct{}{}
		}
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		leftScope := publicReviewScopeOrder(ordered[left].Kind)
		rightScope := publicReviewScopeOrder(ordered[right].Kind)
		if leftScope != rightScope {
			return leftScope < rightScope
		}
		if ordered[left].RelationSortOrder != ordered[right].RelationSortOrder {
			return ordered[left].RelationSortOrder < ordered[right].RelationSortOrder
		}
		return ordered[left].RelationID.String() <
			ordered[right].RelationID.String()
	})
	documents := make([]PublicReviewDocument, 0, len(ordered))
	for _, fact := range ordered {
		blocks := projectPublicReviewBlocks(fact.Blocks, externalDomains)
		if len(blocks) == 0 {
			continue
		}
		documents = append(documents, PublicReviewDocument{
			RelationID: fact.RelationID,
			ContentID:  fact.ContentID,
			SessionID:  copyPastHighlightUUID(fact.SessionID),
			Kind:       fact.Kind,
			Title:      strings.TrimSpace(fact.Title),
			SortOrder:  fact.RelationSortOrder,
			Blocks:     blocks,
		})
	}
	return PublicReviewDetail{
		Target: PastHighlightReviewTarget{
			SeriesID:   target.SeriesID,
			InstanceID: target.InstanceID,
			SessionID:  copyPastHighlightUUID(target.SessionID),
		},
		Documents: documents,
	}, nil
}

func validPublicReviewTarget(value PastHighlightReviewTarget) bool {
	return value.SeriesID != uuid.Nil && value.InstanceID != uuid.Nil &&
		(value.SessionID == nil || *value.SessionID != uuid.Nil)
}

func validPublicReviewDocument(
	target PastHighlightReviewTarget,
	value PublicReviewDocumentFacts,
) bool {
	if value.RelationID == uuid.Nil || value.ContentID == uuid.Nil ||
		value.SeriesID != target.SeriesID || value.InstanceID != target.InstanceID ||
		value.RelationSortOrder < 0 || len(value.Title) > 500 {
		return false
	}
	switch value.Kind {
	case RelationKindInstanceReview:
		return value.SessionID == nil
	case RelationKindSessionResources:
		return target.SessionID != nil && value.SessionID != nil &&
			*value.SessionID == *target.SessionID
	default:
		return false
	}
}

func validPublicReviewBlockFacts(value PublicReviewBlockFacts) bool {
	if value.IsCover && value.Type != PublicReviewBlockTypeImage {
		return false
	}
	if value.BlockID == uuid.Nil || value.SortOrder < 0 ||
		len(value.Text) > 100_000 || len(value.Label) > 500 || len(value.Subtitle) > 500 ||
		len(value.ExternalURL) > 4_096 || len(value.MIME) > 100 ||
		(value.FileID != nil && *value.FileID == uuid.Nil) {
		return false
	}
	if value.VideoChannel != nil && (value.Type != PublicReviewBlockTypeLink || value.ExternalURL != "" || !ValidReviewVideoChannel(*value.VideoChannel)) {
		return false
	}
	switch value.Type {
	case PublicReviewBlockTypeText:
		return strings.TrimSpace(value.Text) != "" && value.FileID == nil &&
			strings.TrimSpace(value.ExternalURL) == ""
	case PublicReviewBlockTypeLink:
		return value.FileID == nil && strings.TrimSpace(value.Text) == ""
	case PublicReviewBlockTypeImage:
		// A photo may be backed by an approved platform File or by an
		// administrator-supplied HTTPS image reference. The latter is still
		// checked against ExternalDomainPolicy during projection.
		return strings.TrimSpace(value.Text) == "" &&
			((value.FileID == nil && strings.TrimSpace(value.ExternalURL) != "") ||
				(value.FileID != nil && strings.TrimSpace(value.ExternalURL) == ""))
	case PublicReviewBlockTypeFile, PublicReviewBlockTypeVideo,
		PublicReviewBlockTypeAudio:
		return strings.TrimSpace(value.Text) == "" &&
			strings.TrimSpace(value.ExternalURL) == ""
	default:
		return false
	}
}

func projectPublicReviewBlocks(
	facts []PublicReviewBlockFacts,
	externalDomains ExternalDomainPolicy,
) []PublicReviewBlock {
	ordered := append([]PublicReviewBlockFacts(nil), facts...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].SortOrder != ordered[right].SortOrder {
			return ordered[left].SortOrder < ordered[right].SortOrder
		}
		return ordered[left].BlockID.String() < ordered[right].BlockID.String()
	})
	result := make([]PublicReviewBlock, 0, len(ordered))
	for _, fact := range ordered {
		block := PublicReviewBlock{
			BlockID:   fact.BlockID,
			Type:      fact.Type,
			SortOrder: fact.SortOrder,
			Text:      strings.TrimSpace(fact.Text),
			Label:     strings.TrimSpace(fact.Label),
			Subtitle:  strings.TrimSpace(fact.Subtitle),
			IsCover:   fact.Type == PublicReviewBlockTypeImage && fact.IsCover,
		}
		switch fact.Type {
		case PublicReviewBlockTypeText:
			block.Availability = PublicReviewBlockAvailable
		case PublicReviewBlockTypeLink, PublicReviewBlockTypeImage:
			if fact.Type == PublicReviewBlockTypeImage && fact.FileID != nil {
				projectPublicReviewFile(&block, fact)
				break
			}
			if fact.VideoChannel != nil {
				channel := *fact.VideoChannel
				block.VideoChannel = &channel
				block.Availability = PublicReviewBlockAvailable
			} else {
				projectPublicReviewLink(&block, fact.ExternalURL, externalDomains)
			}
		default:
			projectPublicReviewFile(&block, fact)
		}
		result = append(result, block)
	}
	return result
}

func projectPublicReviewLink(
	block *PublicReviewBlock,
	rawURL string,
	externalDomains ExternalDomainPolicy,
) {
	if strings.TrimSpace(rawURL) == "" {
		block.Availability = PublicReviewBlockUnavailable
		return
	}
	allowedURL, allowed := externalDomains.AllowURL(rawURL)
	if !allowed {
		block.Availability = PublicReviewBlockPolicyBlocked
		return
	}
	block.ExternalURL = allowedURL
	block.Availability = PublicReviewBlockAvailable
}

func projectPublicReviewFile(
	block *PublicReviewBlock,
	fact PublicReviewBlockFacts,
) {
	if fact.FileID == nil {
		block.Availability = PublicReviewBlockUnavailable
		return
	}
	mime := strings.ToLower(strings.TrimSpace(fact.MIME))
	if !publicReviewMIMEAllowed(fact.Type, mime) {
		block.Availability = PublicReviewBlockPolicyBlocked
		return
	}
	block.FileID = copyPastHighlightUUID(fact.FileID)
	block.MIME = mime
	block.Availability = PublicReviewBlockAvailable
}

func publicReviewMIMEAllowed(kind PublicReviewBlockType, mime string) bool {
	if mime == "" || mime == "text/html" || mime == "image/svg+xml" {
		return false
	}
	switch kind {
	case PublicReviewBlockTypeImage:
		return strings.HasPrefix(mime, "image/")
	case PublicReviewBlockTypeVideo:
		return strings.HasPrefix(mime, "video/")
	case PublicReviewBlockTypeAudio:
		return strings.HasPrefix(mime, "audio/")
	case PublicReviewBlockTypeFile:
		return true
	default:
		return false
	}
}

func publicReviewScopeOrder(kind RelationKind) int {
	if kind == RelationKindInstanceReview {
		return 0
	}
	return 1
}
