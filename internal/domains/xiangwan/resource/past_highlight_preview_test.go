package resource

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBuildPastHighlightPreviewCapsAndOrdersUniquePhotos(t *testing.T) {
	t.Parallel()

	context := knownPastHighlightContext()
	sessionID := *context.FeaturedSessionID
	duplicateFileID := previewUUID(20)
	photos := []PastHighlightPhotoReference{
		knownPastHighlightPhoto(context, RelationKindSessionResources, &sessionID, 5, 1, previewUUID(10)),
		knownPastHighlightPhoto(context, RelationKindInstanceReview, nil, 2, 2, previewUUID(11)),
		knownPastHighlightPhoto(context, RelationKindInstanceReview, nil, 1, 3, previewUUID(12)),
		knownPastHighlightPhoto(context, RelationKindInstanceReview, nil, 1, 1, duplicateFileID),
		knownPastHighlightPhoto(context, RelationKindSessionResources, &sessionID, 1, 1, previewUUID(13)),
		knownPastHighlightPhoto(context, RelationKindInstanceReview, nil, 1, 2, duplicateFileID),
	}
	original := append([]PastHighlightPhotoReference(nil), photos...)

	got, err := BuildPastHighlightPreview(context, photos)
	if err != nil {
		t.Fatalf("BuildPastHighlightPreview() error = %v", err)
	}
	if len(got.Photos) != MaxPastHighlightPreviewPhotos ||
		got.Photos[0].FileID != duplicateFileID ||
		got.Photos[1].FileID != previewUUID(12) ||
		got.Photos[2].FileID != previewUUID(11) {
		t.Fatalf("BuildPastHighlightPreview() photos = %+v", got.Photos)
	}
	if got.MoreTarget.SeriesID != context.SeriesID ||
		got.MoreTarget.InstanceID != context.PreviousInstanceID ||
		got.MoreTarget.SessionID == nil ||
		*got.MoreTarget.SessionID != sessionID {
		t.Fatalf("BuildPastHighlightPreview() target = %+v", got.MoreTarget)
	}
	if !reflect.DeepEqual(photos, original) {
		t.Fatalf("BuildPastHighlightPreview() mutated input: %+v", photos)
	}
}

func TestBuildPastHighlightPreviewAllowsTextOnlyReviewContext(t *testing.T) {
	t.Parallel()

	context := knownPastHighlightContext()
	context.FeaturedSessionID = nil
	got, err := BuildPastHighlightPreview(context, nil)
	if err != nil || len(got.Photos) != 0 ||
		got.MoreTarget.InstanceID != context.PreviousInstanceID ||
		got.MoreTarget.SessionID != nil {
		t.Fatalf("BuildPastHighlightPreview(text only) = %+v, %v", got, err)
	}
}

func TestBuildPastHighlightPreviewRejectsCrossContextOrUnsafePhotos(
	t *testing.T,
) {
	t.Parallel()

	context := knownPastHighlightContext()
	valid := knownPastHighlightPhoto(
		context,
		RelationKindInstanceReview,
		nil,
		1,
		1,
		previewUUID(30),
	)
	tests := []struct {
		name   string
		mutate func(*PastHighlightPhotoReference)
	}{
		{name: "other Series", mutate: func(value *PastHighlightPhotoReference) { value.SeriesID = uuid.New() }},
		{name: "other Instance", mutate: func(value *PastHighlightPhotoReference) { value.InstanceID = uuid.New() }},
		{name: "other Session", mutate: func(value *PastHighlightPhotoReference) {
			other := uuid.New()
			value.Kind = RelationKindSessionResources
			value.SessionID = &other
		}},
		{name: "non-image MIME", mutate: func(value *PastHighlightPhotoReference) { value.MIME = "application/pdf" }},
		{name: "negative order", mutate: func(value *PastHighlightPhotoReference) { value.BlockSortOrder = -1 }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			photo := valid
			test.mutate(&photo)
			if _, err := BuildPastHighlightPreview(
				context,
				[]PastHighlightPhotoReference{photo},
			); !errors.Is(err, ErrInvalidPastHighlightPreviewFacts) {
				t.Fatalf("BuildPastHighlightPreview() error = %v", err)
			}
		})
	}
}

func TestBuildPastHighlightPreviewRejectsDuplicateBlocksOrInvalidContext(
	t *testing.T,
) {
	t.Parallel()

	context := knownPastHighlightContext()
	photo := knownPastHighlightPhoto(
		context,
		RelationKindInstanceReview,
		nil,
		1,
		1,
		previewUUID(40),
	)
	duplicate := photo
	duplicate.FileID = previewUUID(41)
	if _, err := BuildPastHighlightPreview(
		context,
		[]PastHighlightPhotoReference{photo, duplicate},
	); !errors.Is(err, ErrInvalidPastHighlightPreviewFacts) {
		t.Fatalf("BuildPastHighlightPreview(duplicate Block) error = %v", err)
	}
	if _, err := BuildPastHighlightPreview(
		PastHighlightContext{},
		nil,
	); !errors.Is(err, ErrInvalidPastHighlightPreviewFacts) {
		t.Fatalf("BuildPastHighlightPreview(invalid context) error = %v", err)
	}
}

func knownPastHighlightContext() PastHighlightContext {
	sessionID := previewUUID(4)
	return PastHighlightContext{
		SeriesID:                    previewUUID(1),
		AnchorInstanceID:            previewUUID(2),
		PreviousInstanceID:          previewUUID(3),
		FeaturedSessionID:           &sessionID,
		PreviousInstancePublishedAt: previewTime(10),
		PreviousInstanceCompletedAt: previewTime(12),
	}
}

func knownPastHighlightPhoto(
	context PastHighlightContext,
	kind RelationKind,
	sessionID *uuid.UUID,
	relationOrder int,
	blockOrder int,
	fileID uuid.UUID,
) PastHighlightPhotoReference {
	return PastHighlightPhotoReference{
		RelationID:        uuid.New(),
		ContentID:         uuid.New(),
		BlockID:           uuid.New(),
		FileID:            fileID,
		SeriesID:          context.SeriesID,
		InstanceID:        context.PreviousInstanceID,
		SessionID:         copyPastHighlightUUID(sessionID),
		Kind:              kind,
		MIME:              "image/webp",
		RelationSortOrder: relationOrder,
		BlockSortOrder:    blockOrder,
	}
}

func previewUUID(lastByte byte) uuid.UUID {
	var value uuid.UUID
	value[len(value)-1] = lastByte
	return value
}

func previewTime(hour int) time.Time {
	return time.Date(2026, time.September, 18, hour, 0, 0, 0, time.UTC)
}
