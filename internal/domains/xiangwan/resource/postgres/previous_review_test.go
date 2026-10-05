package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestReadPreviousInstanceReviewScansOneInstanceAndImages(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	seriesID := uuid.New()
	currentInstanceID := uuid.New()
	previousInstanceID := uuid.New()
	completedAt := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	relationID := uuid.New()
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(query string, args ...any) (resourceRowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakePublishedResourceRows{values: [][]any{
				{
					previousInstanceID, "第八期圆桌", completedAt, true,
					uuid.NullUUID{UUID: relationID, Valid: true},
					sql.NullInt64{Int64: 0, Valid: true},
					uuid.NullUUID{UUID: uuid.New(), Valid: true},
					sql.NullInt64{Int64: 0, Valid: true},
					uuid.NullUUID{UUID: uuid.New(), Valid: true},
					sql.NullString{String: "", Valid: true},
				},
				{
					previousInstanceID, "第八期圆桌", completedAt, true,
					uuid.NullUUID{UUID: relationID, Valid: true},
					sql.NullInt64{Int64: 0, Valid: true},
					uuid.NullUUID{UUID: uuid.New(), Valid: true},
					sql.NullInt64{Int64: 1, Valid: true},
					uuid.NullUUID{UUID: uuid.New(), Valid: true},
					sql.NullString{String: "", Valid: true},
				},
			}}, nil
		},
	}}
	facts, err := repository.ReadPreviousInstanceReview(
		context.Background(),
		tenantID,
		seriesID,
		currentInstanceID,
	)
	if err != nil {
		t.Fatalf("ReadPreviousInstanceReview() error = %v", err)
	}
	if facts.Instance == nil || facts.Instance.InstanceID != previousInstanceID ||
		facts.Instance.Title != "第八期圆桌" ||
		!facts.Instance.CompletedAt.Equal(completedAt) ||
		!facts.HasPublishedReview ||
		len(facts.Images) != 2 ||
		facts.Images[0].RelationID != relationID ||
		facts.Images[1].BlockSortOrder != 1 {
		t.Fatalf("ReadPreviousInstanceReview() = %+v", facts)
	}
	for _, fragment := range []string{
		"candidate.series_id = $2",
		"candidate.id <> current_instance.id",
		"candidate.status IN ('completed', 'archived')",
		"relation.relation_kind = 'instance_review'",
		"content_block.type = 'image'",
		"stored_file.mime LIKE 'image/%'",
		"content_governance",
		"EXISTS (",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("previous review query missing %q", fragment)
		}
	}
	if strings.Contains(capturedQuery, "content.metadata->>'product_code'") {
		t.Fatal("previous review query must preserve legacy resource Content rows without product metadata")
	}
	if !reflect.DeepEqual(capturedArgs, []any{tenantID, seriesID, currentInstanceID}) {
		t.Fatalf("previous review args = %#v", capturedArgs)
	}
}

func TestReadPreviousInstanceReviewAbsentAndConflict(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	seriesID := uuid.New()
	currentInstanceID := uuid.New()
	completedAt := time.Now().UTC()

	t.Run("no previous instance", func(t *testing.T) {
		repository := &Repository{db: &fakeResourceExecutor{
			query: func(string, ...any) (resourceRowsScanner, error) {
				return &fakePublishedResourceRows{}, nil
			},
		}}
		facts, err := repository.ReadPreviousInstanceReview(
			context.Background(), tenantID, seriesID, currentInstanceID,
		)
		if err != nil || facts.Instance != nil || len(facts.Images) != 0 {
			t.Fatalf("ReadPreviousInstanceReview() = %+v, %v", facts, err)
		}
	})

	t.Run("previous instance without review images", func(t *testing.T) {
		repository := &Repository{db: &fakeResourceExecutor{
			query: func(string, ...any) (resourceRowsScanner, error) {
				return &fakePublishedResourceRows{values: [][]any{{
					uuid.New(), "第八期圆桌", completedAt, false,
					uuid.NullUUID{}, sql.NullInt64{},
					uuid.NullUUID{}, sql.NullInt64{}, uuid.NullUUID{}, sql.NullString{},
				}}}, nil
			},
		}}
		facts, err := repository.ReadPreviousInstanceReview(
			context.Background(), tenantID, seriesID, currentInstanceID,
		)
		if err != nil || facts.Instance == nil || facts.HasPublishedReview || len(facts.Images) != 0 {
			t.Fatalf("ReadPreviousInstanceReview() = %+v, %v", facts, err)
		}
	})

	t.Run("published link-only review", func(t *testing.T) {
		repository := &Repository{db: &fakeResourceExecutor{
			query: func(string, ...any) (resourceRowsScanner, error) {
				return &fakePublishedResourceRows{values: [][]any{{
					uuid.New(), "第八期圆桌", completedAt, true,
					uuid.NullUUID{}, sql.NullInt64{},
					uuid.NullUUID{}, sql.NullInt64{}, uuid.NullUUID{}, sql.NullString{},
				}}}, nil
			},
		}}
		facts, err := repository.ReadPreviousInstanceReview(
			context.Background(), tenantID, seriesID, currentInstanceID,
		)
		if err != nil || facts.Instance == nil || !facts.HasPublishedReview || len(facts.Images) != 0 {
			t.Fatalf("ReadPreviousInstanceReview() = %+v, %v", facts, err)
		}
	})

	t.Run("published URL photo is kept for policy projection", func(t *testing.T) {
		blockID := uuid.New()
		repository := &Repository{db: &fakeResourceExecutor{
			query: func(string, ...any) (resourceRowsScanner, error) {
				return &fakePublishedResourceRows{values: [][]any{{
					uuid.New(), "上一期", completedAt, true,
					uuid.NullUUID{UUID: uuid.New(), Valid: true},
					sql.NullInt64{Int64: 0, Valid: true},
					uuid.NullUUID{UUID: blockID, Valid: true},
					sql.NullInt64{Int64: 0, Valid: true},
					uuid.NullUUID{},
					sql.NullString{String: "https://images.example.com/review.jpg", Valid: true},
				}}}, nil
			},
		}}
		facts, err := repository.ReadPreviousInstanceReview(
			context.Background(), tenantID, seriesID, currentInstanceID,
		)
		if err != nil || len(facts.Images) != 1 || facts.Images[0].BlockID != blockID ||
			facts.Images[0].FileID != uuid.Nil ||
			facts.Images[0].ExternalURL != "https://images.example.com/review.jpg" {
			t.Fatalf("URL photo facts = %+v, %v", facts, err)
		}
	})

	t.Run("mixed instance identity is a conflict", func(t *testing.T) {
		repository := &Repository{db: &fakeResourceExecutor{
			query: func(string, ...any) (resourceRowsScanner, error) {
				return &fakePublishedResourceRows{values: [][]any{
					{
						uuid.New(), "第八期圆桌", completedAt, true,
						uuid.NullUUID{UUID: uuid.New(), Valid: true},
						sql.NullInt64{Int64: 0, Valid: true},
						uuid.NullUUID{UUID: uuid.New(), Valid: true},
						sql.NullInt64{Int64: 0, Valid: true},
						uuid.NullUUID{UUID: uuid.New(), Valid: true},
						sql.NullString{String: "", Valid: true},
					},
					{
						uuid.New(), "另一期", completedAt, true,
						uuid.NullUUID{UUID: uuid.New(), Valid: true},
						sql.NullInt64{Int64: 0, Valid: true},
						uuid.NullUUID{UUID: uuid.New(), Valid: true},
						sql.NullInt64{Int64: 0, Valid: true},
						uuid.NullUUID{UUID: uuid.New(), Valid: true},
						sql.NullString{String: "", Valid: true},
					},
				}}, nil
			},
		}}
		_, err := repository.ReadPreviousInstanceReview(
			context.Background(), tenantID, seriesID, currentInstanceID,
		)
		if !errors.Is(err, ErrPreviousReviewFactsConflict) {
			t.Fatalf("ReadPreviousInstanceReview() error = %v", err)
		}
	})

	t.Run("invalid query rejected before SQL", func(t *testing.T) {
		repository := &Repository{db: &fakeResourceExecutor{
			query: func(string, ...any) (resourceRowsScanner, error) {
				t.Fatal("invalid query reached the database")
				return nil, nil
			},
		}}
		_, err := repository.ReadPreviousInstanceReview(
			context.Background(), tenantID, seriesID, uuid.Nil,
		)
		if !errors.Is(err, ErrInvalidPreviousReviewQuery) {
			t.Fatalf("ReadPreviousInstanceReview() error = %v", err)
		}
	})

	// The image scan must dedupe per source before it bounds: with the LIMIT
	// first, the first 32 rows could all be duplicates of one File or URL and the
	// projection would end up with fewer than the 3 distinct previews.
	t.Run("image scan dedupes per source before it bounds", func(t *testing.T) {
		distinctAt := strings.Index(
			previousReviewSelect, "SELECT DISTINCT ON (review_images.source_key)",
		)
		if distinctAt < 0 {
			t.Fatal("per-source DISTINCT ON dedupe missing")
		}
		boundedStart := strings.Index(
			previousReviewSelect, "bounded_review_images AS (",
		)
		if boundedStart < 0 || boundedStart < distinctAt {
			t.Fatal("bounded image scan must come after the source dedupe")
		}
		bounded := previousReviewSelect[boundedStart:]
		if end := strings.Index(bounded, ")\nSELECT"); end >= 0 {
			bounded = bounded[:end]
		}
		orderAt := strings.LastIndex(bounded, "ORDER BY\n")
		limitAt := strings.LastIndex(bounded, "LIMIT 32")
		if orderAt < 0 || limitAt < 0 || orderAt > limitAt {
			t.Fatalf("bounded image scan must ORDER BY before LIMIT 32:\n%s", bounded)
		}
	})
}
