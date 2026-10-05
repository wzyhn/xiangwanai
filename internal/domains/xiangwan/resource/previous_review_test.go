package resource

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProjectPreviousInstanceReviewSelectsOrderedCappedImages(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	instance := &PreviousInstanceFacts{
		InstanceID:  reviewUUID(1),
		Title:       " 第八期圆桌 ",
		CompletedAt: completedAt,
	}
	sharedFile := reviewUUID(20)
	images := []PreviousReviewImageFacts{
		// Second document's image sorts after the first document's.
		{RelationID: reviewUUID(11), DocumentSortOrder: 1, BlockID: reviewUUID(30), BlockSortOrder: 0, FileID: reviewUUID(21)},
		{RelationID: reviewUUID(10), DocumentSortOrder: 0, BlockID: reviewUUID(31), BlockSortOrder: 2, FileID: reviewUUID(22)},
		{RelationID: reviewUUID(10), DocumentSortOrder: 0, BlockID: reviewUUID(32), BlockSortOrder: 0, FileID: sharedFile},
		// Duplicate File collapses even though the Block differs.
		{RelationID: reviewUUID(10), DocumentSortOrder: 0, BlockID: reviewUUID(33), BlockSortOrder: 1, FileID: sharedFile},
		// Fifth image would exceed the cap; fourth distinct File takes slot 3.
		{RelationID: reviewUUID(10), DocumentSortOrder: 0, BlockID: reviewUUID(34), BlockSortOrder: 3, FileID: reviewUUID(23)},
		{RelationID: reviewUUID(10), DocumentSortOrder: 0, BlockID: reviewUUID(35), BlockSortOrder: 4, FileID: reviewUUID(24)},
		// Invalid rows are dropped, never selected.
		{RelationID: uuid.Nil, DocumentSortOrder: 0, BlockID: reviewUUID(36), BlockSortOrder: 5, FileID: reviewUUID(25)},
		{RelationID: reviewUUID(10), DocumentSortOrder: -1, BlockID: reviewUUID(37), BlockSortOrder: 6, FileID: reviewUUID(26)},
	}
	review := ProjectPreviousInstanceReview(PreviousReviewFacts{
		Instance:           instance,
		HasPublishedReview: true,
		Images:             images,
	})
	if review == nil {
		t.Fatal("ProjectPreviousInstanceReview() = nil, want preview")
	}
	if review.InstanceID != instance.InstanceID || review.Title != "第八期圆桌" ||
		!review.CompletedAt.Equal(completedAt) {
		t.Fatalf("preview identity = %+v", review)
	}
	wantFiles := []uuid.UUID{sharedFile, reviewUUID(22), reviewUUID(23)}
	if len(review.Images) != len(wantFiles) {
		t.Fatalf("preview images = %+v", review.Images)
	}
	for index, want := range wantFiles {
		image := review.Images[index]
		if image.FileID != want || image.RelationID == uuid.Nil ||
			image.BlockID == uuid.Nil {
			t.Fatalf("preview image[%d] = %+v, want File %s", index, image, want)
		}
	}
}

func TestProjectPreviousInstanceReviewFillsPreviewWithDistinctFiles(t *testing.T) {
	t.Parallel()

	// One File repeated across many Blocks of the first document consumes a
	// single preview slot: the SQL scan now dedupes per File before it
	// bounds, so the sibling documents' Files must still fill the whole
	// 3-image preview.
	completedAt := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	sharedFile := reviewUUID(20)
	images := make([]PreviousReviewImageFacts, 0, 42)
	for block := 0; block < 40; block++ {
		images = append(images, PreviousReviewImageFacts{
			RelationID:        reviewUUID(10),
			DocumentSortOrder: 0,
			BlockID:           reviewUUID(byte(30 + block)),
			BlockSortOrder:    block,
			FileID:            sharedFile,
		})
	}
	images = append(images,
		PreviousReviewImageFacts{
			RelationID:        reviewUUID(11),
			DocumentSortOrder: 1,
			BlockID:           reviewUUID(81),
			BlockSortOrder:    0,
			FileID:            reviewUUID(21),
		},
		PreviousReviewImageFacts{
			RelationID:        reviewUUID(12),
			DocumentSortOrder: 2,
			BlockID:           reviewUUID(82),
			BlockSortOrder:    0,
			FileID:            reviewUUID(22),
		},
	)
	review := ProjectPreviousInstanceReview(PreviousReviewFacts{
		Instance: &PreviousInstanceFacts{
			InstanceID:  reviewUUID(1),
			Title:       "第九期圆桌",
			CompletedAt: completedAt,
		},
		HasPublishedReview: true,
		Images:             images,
	})
	if review == nil {
		t.Fatal("ProjectPreviousInstanceReview() = nil, want preview")
	}
	wantFiles := []uuid.UUID{sharedFile, reviewUUID(21), reviewUUID(22)}
	if len(review.Images) != len(wantFiles) {
		t.Fatalf("preview images = %+v, want %d distinct Files", review.Images, len(wantFiles))
	}
	for index, want := range wantFiles {
		if review.Images[index].FileID != want {
			t.Fatalf("preview image[%d].FileID = %s, want %s",
				index, review.Images[index].FileID, want)
		}
	}
}

func TestProjectPreviousInstanceReviewPreservesPublishedLinkOnlyReview(t *testing.T) {
	t.Parallel()

	completedAt := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	review := ProjectPreviousInstanceReview(PreviousReviewFacts{
		Instance: &PreviousInstanceFacts{
			InstanceID:  reviewUUID(1),
			Title:       "第十期圆桌",
			CompletedAt: completedAt,
		},
		HasPublishedReview: true,
	})
	if review == nil {
		t.Fatal("ProjectPreviousInstanceReview() = nil, want link-only preview")
	}
	if review.InstanceID != reviewUUID(1) || review.Title != "第十期圆桌" ||
		!review.CompletedAt.Equal(completedAt) || len(review.Images) != 0 {
		t.Fatalf("link-only previous review = %+v", review)
	}
}

func TestProjectPreviousInstanceReviewWithPolicyAllowsOnlyCurrentApprovedPhotoURLs(t *testing.T) {
	t.Parallel()
	policy, err := NewExternalDomainPolicy([]string{"images.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	facts := PreviousReviewFacts{
		Instance: &PreviousInstanceFacts{
			InstanceID: reviewUUID(1), Title: "上一期", CompletedAt: time.Now().UTC(),
		},
		HasPublishedReview: true,
		Images: []PreviousReviewImageFacts{
			{RelationID: reviewUUID(10), BlockID: reviewUUID(30), ExternalURL: "https://images.example.com/photo.jpg"},
			{RelationID: reviewUUID(10), BlockID: reviewUUID(31), BlockSortOrder: 1, ExternalURL: "https://images.example.com/photo.jpg"},
			{RelationID: reviewUUID(10), BlockID: reviewUUID(32), BlockSortOrder: 2, ExternalURL: "https://unlisted.example.com/photo.jpg"},
			{RelationID: reviewUUID(10), BlockID: reviewUUID(33), BlockSortOrder: 3, ExternalURL: "http://images.example.com/photo.jpg"},
			{RelationID: reviewUUID(10), BlockID: reviewUUID(34), BlockSortOrder: 4, FileID: reviewUUID(44)},
		},
	}
	review := ProjectPreviousInstanceReviewWithPolicy(facts, policy)
	if review == nil || len(review.Images) != 2 ||
		review.Images[0].ExternalURL != "https://images.example.com/photo.jpg" ||
		review.Images[0].FileID != uuid.Nil || review.Images[1].FileID != reviewUUID(44) {
		t.Fatalf("approved photo preview = %+v", review)
	}
	closed := ProjectPreviousInstanceReviewWithPolicy(facts, ExternalDomainPolicy{})
	if closed == nil || len(closed.Images) != 1 || closed.Images[0].FileID != reviewUUID(44) {
		t.Fatalf("unconfigured domain policy must fail closed: %+v", closed)
	}
}

func TestProjectPreviousInstanceReviewEmptyCasesStayNull(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		facts PreviousReviewFacts
	}{
		{name: "no previous instance", facts: PreviousReviewFacts{}},
		{
			name: "nil instance identity",
			facts: PreviousReviewFacts{
				Instance: &PreviousInstanceFacts{CompletedAt: time.Now().UTC()},
				Images: []PreviousReviewImageFacts{
					{RelationID: reviewUUID(10), BlockID: reviewUUID(30), FileID: reviewUUID(40)},
				},
			},
		},
		{
			name: "zero completed_at",
			facts: PreviousReviewFacts{
				Instance: &PreviousInstanceFacts{InstanceID: reviewUUID(1), Title: "第八期"},
				Images: []PreviousReviewImageFacts{
					{RelationID: reviewUUID(10), BlockID: reviewUUID(30), FileID: reviewUUID(40)},
				},
			},
		},
		{
			name: "review without usable images",
			facts: PreviousReviewFacts{
				Instance: &PreviousInstanceFacts{
					InstanceID:  reviewUUID(1),
					Title:       "第八期",
					CompletedAt: time.Now().UTC(),
				},
			},
		},
		{
			name: "all images invalid",
			facts: PreviousReviewFacts{
				Instance: &PreviousInstanceFacts{
					InstanceID:  reviewUUID(1),
					Title:       "第八期",
					CompletedAt: time.Now().UTC(),
				},
				Images: []PreviousReviewImageFacts{
					{RelationID: reviewUUID(10), BlockID: reviewUUID(30), FileID: uuid.Nil},
				},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if review := ProjectPreviousInstanceReview(test.facts); review != nil {
				t.Fatalf("ProjectPreviousInstanceReview() = %+v, want nil", review)
			}
		})
	}
}

func reviewUUID(seed byte) uuid.UUID {
	return uuid.UUID{
		seed, seed, seed, seed, seed, seed, seed, seed,
		seed, seed, seed, seed, seed, seed, seed, seed,
	}
}
