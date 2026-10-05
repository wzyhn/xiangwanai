package resourcepostgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestPhotoCurationIDsRoundTripAndRejectDuplicateOrMalformed(t *testing.T) {
	t.Parallel()
	first, second := uuid.New(), uuid.New()
	encoded, err := encodePhotoIDs([]uuid.UUID{second, first})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodePhotoIDs(encoded)
	if err != nil || !equalPhotoIDs(decoded, []uuid.UUID{second, first}) {
		t.Fatalf("photo order round trip = %v, %v", decoded, err)
	}
	for _, ids := range [][]uuid.UUID{{first, first}, {uuid.Nil}} {
		if _, err := encodePhotoIDs(ids); !errors.Is(err, resource.ErrInvalidPhotoCuration) {
			t.Fatalf("encode invalid photo ids %v = %v", ids, err)
		}
	}
	for _, raw := range []string{`["not-a-uuid"]`, `null`, `{"wrong":"shape"}`,
		`["` + first.String() + `","` + first.String() + `"]`} {
		if _, err := decodePhotoIDs([]byte(raw)); !errors.Is(err, resource.ErrInvalidPhotoCuration) {
			t.Fatalf("decode invalid photo ids %s = %v", raw, err)
		}
	}
}

func TestPublicReviewPhotoCurationFiltersAllImageAccessPaths(t *testing.T) {
	t.Parallel()
	for path, query := range map[string]string{
		"review detail":          publicReviewSelect,
		"past highlight preview": pastHighlightPhotoSelect,
		"past highlight context": pastHighlightContextSelect,
		"previous review":        previousReviewSelect,
		"published file bytes":   publicFileGrantSelect,
	} {
		if !strings.Contains(query, "xiangwan_review_photo_curations") ||
			!strings.Contains(query, "ordered_block_ids ?") ||
			!strings.Contains(query, "ORDER BY photo_curation.version DESC") {
			t.Errorf("%s lost current curation gate", path)
		}
	}
	if !strings.Contains(previousReviewSelect, "AS has_published_review") ||
		!strings.Contains(previousReviewSelect, "visible_curation.ordered_block_ids ? visible_block.id::TEXT") {
		t.Fatal("previous review visibility flag ignores an all-hidden photo-only review")
	}
}
