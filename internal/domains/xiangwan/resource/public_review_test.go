package resource

import (
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

func TestProjectPublicReviewUsesExactContextAndAllowlistedFields(t *testing.T) {
	t.Parallel()

	sessionID := publicReviewUUID(3)
	target := PastHighlightReviewTarget{
		SeriesID:   publicReviewUUID(1),
		InstanceID: publicReviewUUID(2),
		SessionID:  &sessionID,
	}
	imageFileID := publicReviewUUID(10)
	instanceDocument := knownPublicReviewDocument(
		target,
		RelationKindInstanceReview,
		nil,
		2,
	)
	instanceDocument.Blocks = []PublicReviewBlockFacts{
		{
			BlockID:     publicReviewUUID(12),
			Type:        PublicReviewBlockTypeLink,
			SortOrder:   3,
			Label:       " 活动资料 ",
			Subtitle:    "资料整理",
			ExternalURL: "https://DOCS.EXAMPLE.COM:443/review",
		},
		{
			BlockID:   publicReviewUUID(11),
			Type:      PublicReviewBlockTypeText,
			SortOrder: 1,
			Text:      " 精彩回顾 ",
		},
		{
			BlockID:   publicReviewUUID(13),
			Type:      PublicReviewBlockTypeImage,
			SortOrder: 2,
			FileID:    &imageFileID,
			MIME:      "IMAGE/WEBP",
		},
		{
			BlockID:     publicReviewUUID(15),
			Type:        PublicReviewBlockTypeImage,
			SortOrder:   4,
			ExternalURL: "https://docs.example.com/photo.webp",
			Label:       "活动照片",
		},
	}
	sessionDocument := knownPublicReviewDocument(
		target,
		RelationKindSessionResources,
		&sessionID,
		1,
	)
	sessionDocument.Blocks = []PublicReviewBlockFacts{
		{
			BlockID:     publicReviewUUID(14),
			Type:        PublicReviewBlockTypeLink,
			SortOrder:   1,
			Label:       "未授权链接",
			ExternalURL: "https://evil.example.net/resource",
		},
	}
	policy, err := NewExternalDomainPolicy([]string{"docs.example.com"})
	if err != nil {
		t.Fatalf("NewExternalDomainPolicy() error = %v", err)
	}

	got, err := ProjectPublicReview(
		target,
		[]PublicReviewDocumentFacts{sessionDocument, instanceDocument},
		policy,
	)
	if err != nil {
		t.Fatalf("ProjectPublicReview() error = %v", err)
	}
	if len(got.Documents) != 2 ||
		got.Documents[0].Kind != RelationKindInstanceReview ||
		got.Documents[0].SessionID != nil ||
		got.Documents[1].Kind != RelationKindSessionResources ||
		got.Documents[1].SessionID == nil ||
		*got.Documents[1].SessionID != sessionID {
		t.Fatalf("ProjectPublicReview() documents = %+v", got.Documents)
	}
	blocks := got.Documents[0].Blocks
	if len(blocks) != 4 || blocks[0].Text != "精彩回顾" ||
		blocks[0].Availability != PublicReviewBlockAvailable ||
		blocks[1].FileID == nil || *blocks[1].FileID != imageFileID ||
		blocks[1].MIME != "image/webp" ||
		blocks[2].ExternalURL != "https://docs.example.com/review" ||
		blocks[2].Label != "活动资料" || blocks[2].Subtitle != "资料整理" ||
		blocks[3].ExternalURL != "https://docs.example.com/photo.webp" ||
		blocks[3].Availability != PublicReviewBlockAvailable {
		t.Fatalf("ProjectPublicReview() blocks = %+v", blocks)
	}
	blocked := got.Documents[1].Blocks[0]
	if blocked.Availability != PublicReviewBlockPolicyBlocked ||
		blocked.ExternalURL != "" {
		t.Fatalf("ProjectPublicReview() blocked link = %+v", blocked)
	}
	if got.Target.SessionID == nil || *got.Target.SessionID != sessionID {
		t.Fatalf("ProjectPublicReview() target = %+v", got.Target)
	}
}

func TestProjectPublicReviewMarksMissingOrUnsafeFilesUnavailable(t *testing.T) {
	t.Parallel()

	target := PastHighlightReviewTarget{
		SeriesID:   publicReviewUUID(20),
		InstanceID: publicReviewUUID(21),
	}
	document := knownPublicReviewDocument(
		target,
		RelationKindInstanceReview,
		nil,
		1,
	)
	unsafeFileID := publicReviewUUID(22)
	document.Blocks = []PublicReviewBlockFacts{
		{
			BlockID:   publicReviewUUID(23),
			Type:      PublicReviewBlockTypeVideo,
			SortOrder: 1,
		},
		{
			BlockID:   publicReviewUUID(24),
			Type:      PublicReviewBlockTypeImage,
			SortOrder: 2,
			FileID:    &unsafeFileID,
			MIME:      "image/svg+xml",
		},
	}
	policy, _ := NewExternalDomainPolicy(nil)
	got, err := ProjectPublicReview(
		target,
		[]PublicReviewDocumentFacts{document},
		policy,
	)
	if err != nil || len(got.Documents) != 1 ||
		got.Documents[0].Blocks[0].Availability != PublicReviewBlockUnavailable ||
		got.Documents[0].Blocks[1].Availability != PublicReviewBlockPolicyBlocked ||
		got.Documents[0].Blocks[1].FileID != nil {
		t.Fatalf("ProjectPublicReview(files) = %+v, %v", got, err)
	}
}

func TestProjectPublicReviewRejectsMixedOrDuplicateFacts(t *testing.T) {
	t.Parallel()

	sessionID := publicReviewUUID(30)
	target := PastHighlightReviewTarget{
		SeriesID:   publicReviewUUID(31),
		InstanceID: publicReviewUUID(32),
		SessionID:  &sessionID,
	}
	document := knownPublicReviewDocument(
		target,
		RelationKindSessionResources,
		&sessionID,
		1,
	)
	document.Blocks = []PublicReviewBlockFacts{
		{
			BlockID:   publicReviewUUID(33),
			Type:      PublicReviewBlockTypeText,
			SortOrder: 1,
			Text:      "review",
		},
	}
	policy, _ := NewExternalDomainPolicy(nil)
	tests := []struct {
		name  string
		facts []PublicReviewDocumentFacts
	}{
		{name: "cross Session", facts: []PublicReviewDocumentFacts{func() PublicReviewDocumentFacts {
			value := clonePublicReviewDocumentFacts(document)
			other := uuid.New()
			value.SessionID = &other
			return value
		}()}},
		{name: "duplicate relation", facts: []PublicReviewDocumentFacts{
			clonePublicReviewDocumentFacts(document),
			clonePublicReviewDocumentFacts(document),
		}},
		{name: "duplicate Block", facts: []PublicReviewDocumentFacts{func() PublicReviewDocumentFacts {
			value := clonePublicReviewDocumentFacts(document)
			value.Blocks = append(value.Blocks, value.Blocks[0])
			return value
		}()}},
		{name: "unsupported Block", facts: []PublicReviewDocumentFacts{func() PublicReviewDocumentFacts {
			value := clonePublicReviewDocumentFacts(document)
			value.Blocks[0].Type = "code"
			return value
		}()}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := ProjectPublicReview(
				target,
				test.facts,
				policy,
			); !errors.Is(err, ErrInvalidPublicReviewFacts) {
				t.Fatalf("ProjectPublicReview() error = %v", err)
			}
		})
	}
}

func TestProjectPublicReviewDoesNotMutateInput(t *testing.T) {
	t.Parallel()

	target := PastHighlightReviewTarget{
		SeriesID:   publicReviewUUID(40),
		InstanceID: publicReviewUUID(41),
	}
	document := knownPublicReviewDocument(
		target,
		RelationKindInstanceReview,
		nil,
		1,
	)
	document.Blocks = []PublicReviewBlockFacts{
		{
			BlockID:   publicReviewUUID(42),
			Type:      PublicReviewBlockTypeText,
			SortOrder: 2,
			Text:      "second",
		},
		{
			BlockID:   publicReviewUUID(43),
			Type:      PublicReviewBlockTypeText,
			SortOrder: 1,
			Text:      "first",
		},
	}
	facts := []PublicReviewDocumentFacts{document}
	want := append([]PublicReviewDocumentFacts(nil), facts...)
	want[0].Blocks = append([]PublicReviewBlockFacts(nil), facts[0].Blocks...)
	policy, _ := NewExternalDomainPolicy(nil)
	_, err := ProjectPublicReview(target, facts, policy)
	if err != nil || !reflect.DeepEqual(facts, want) {
		t.Fatalf("ProjectPublicReview() mutated input: %+v, %v", facts, err)
	}
}

func knownPublicReviewDocument(
	target PastHighlightReviewTarget,
	kind RelationKind,
	sessionID *uuid.UUID,
	sortOrder int,
) PublicReviewDocumentFacts {
	return PublicReviewDocumentFacts{
		RelationID:        uuid.New(),
		ContentID:         uuid.New(),
		SeriesID:          target.SeriesID,
		InstanceID:        target.InstanceID,
		SessionID:         copyPastHighlightUUID(sessionID),
		Kind:              kind,
		Title:             " Review ",
		RelationSortOrder: sortOrder,
	}
}

func clonePublicReviewDocumentFacts(
	value PublicReviewDocumentFacts,
) PublicReviewDocumentFacts {
	value.SessionID = copyPastHighlightUUID(value.SessionID)
	value.Blocks = append([]PublicReviewBlockFacts(nil), value.Blocks...)
	for index := range value.Blocks {
		value.Blocks[index].FileID = copyPastHighlightUUID(value.Blocks[index].FileID)
	}
	return value
}

func publicReviewUUID(lastByte byte) uuid.UUID {
	var value uuid.UUID
	value[len(value)-1] = lastByte
	return value
}
