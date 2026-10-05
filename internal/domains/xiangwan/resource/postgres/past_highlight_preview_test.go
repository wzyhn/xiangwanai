package resourcepostgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestListPastHighlightPhotoReferencesUsesExactPublicContext(
	t *testing.T,
) {
	t.Parallel()

	highlightContext := knownPastHighlightPreviewContext()
	instancePhoto := knownPastHighlightAdapterPhoto(
		highlightContext,
		resource.RelationKindInstanceReview,
		nil,
	)
	sessionPhoto := knownPastHighlightAdapterPhoto(
		highlightContext,
		resource.RelationKindSessionResources,
		highlightContext.FeaturedSessionID,
	)
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(query string, args ...any) (resourceRowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return pastHighlightPhotoRows(instancePhoto, sessionPhoto), nil
		},
	}}

	got, err := repository.ListPastHighlightPhotoReferences(
		context.Background(),
		pastHighlightAdapterUUID(70),
		highlightContext,
	)
	if err != nil || len(got) != 2 ||
		got[0].FileID != instancePhoto.FileID ||
		got[1].FileID != sessionPhoto.FileID {
		t.Fatalf("ListPastHighlightPhotoReferences() = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(capturedArgs, []any{
		pastHighlightAdapterUUID(70),
		highlightContext.SeriesID,
		highlightContext.PreviousInstanceID,
		*highlightContext.FeaturedSessionID,
	}) {
		t.Fatalf("ListPastHighlightPhotoReferences() args = %#v", capturedArgs)
	}
	for _, fragment := range []string{
		"JOIN xiangwan_resource_content_snapshots AS snapshot",
		"approval.subject_digest = snapshot.subject_digest",
		"approval.decision = 'approved'",
		"relation.series_id = $2",
		"relation.instance_id = $3",
		"relation.access_policy = 'public'",
		"publication.access_policy = 'public'",
		"content.principal_id = relation.created_by",
		"content_block.type = 'image'",
		"stored_file.principal_id = relation.created_by",
		"stored_file.status = 'confirmed'",
		"LOWER(BTRIM(stored_file.mime)) LIKE 'image/%'",
		"stored_file.delete_after IS NULL",
		"relation.session_id = $4",
		"activity_session.version = relation.expected_target_version",
		"PARTITION BY stored_file.id",
		"LIMIT 3",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("past-highlight photo query does not contain %q", fragment)
		}
	}
	if strings.Contains(capturedQuery, "content.metadata->>'product_code'") {
		t.Fatal("past-highlight query must use the Xiangwan resource publication chain, not mutable Content metadata")
	}
}

func TestReadPastHighlightPreviewKeepsPhotosAndMoreOnSameContext(
	t *testing.T,
) {
	t.Parallel()

	tenantID := pastHighlightAdapterUUID(80)
	seriesID := pastHighlightAdapterUUID(81)
	anchorID := pastHighlightAdapterUUID(82)
	previousID := pastHighlightAdapterUUID(83)
	sessionID := pastHighlightAdapterUUID(84)
	photo := resource.PastHighlightPhotoReference{
		RelationID:        pastHighlightAdapterUUID(85),
		ContentID:         pastHighlightAdapterUUID(86),
		BlockID:           pastHighlightAdapterUUID(87),
		FileID:            pastHighlightAdapterUUID(88),
		SeriesID:          seriesID,
		InstanceID:        previousID,
		SessionID:         &sessionID,
		Kind:              resource.RelationKindSessionResources,
		MIME:              "image/jpeg",
		RelationSortOrder: 1,
		BlockSortOrder:    1,
	}
	queryCount := 0
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			queryCount++
			if queryCount == 1 {
				return &fakePublishedResourceRows{values: [][]any{
					pastHighlightAdapterRow(
						seriesID,
						anchorID,
						pastHighlightAdapterTime(20),
						previousID,
						pastHighlightAdapterTime(10),
						pastHighlightAdapterTime(12),
						false,
						sessionID,
						activity.DeliveryModeOffline,
						2,
						11,
						1,
					),
				}}, nil
			}
			return pastHighlightPhotoRows(photo), nil
		},
	}}

	got, err := repository.ReadPastHighlightPreview(
		context.Background(),
		tenantID,
		anchorID,
	)
	if err != nil || got == nil || queryCount != 2 || len(got.Photos) != 1 ||
		got.Photos[0].FileID != photo.FileID ||
		got.MoreTarget.InstanceID != previousID ||
		got.MoreTarget.SessionID == nil ||
		*got.MoreTarget.SessionID != sessionID {
		t.Fatalf("ReadPastHighlightPreview() = %+v, queries=%d, %v", got, queryCount, err)
	}
}

func TestListPastHighlightPhotoReferencesRejectsInvalidContextBeforeSQL(
	t *testing.T,
) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			t.Fatal("invalid preview context reached PostgreSQL")
			return nil, nil
		},
	}}
	if _, err := repository.ListPastHighlightPhotoReferences(
		context.Background(),
		pastHighlightAdapterUUID(90),
		resource.PastHighlightContext{},
	); !errors.Is(err, ErrInvalidPastHighlightPreviewQuery) {
		t.Fatalf("ListPastHighlightPhotoReferences(invalid) error = %v", err)
	}
}

func TestScanPastHighlightPhotosRejectsCrossSessionFacts(t *testing.T) {
	t.Parallel()

	highlightContext := knownPastHighlightPreviewContext()
	otherSessionID := uuid.New()
	photo := knownPastHighlightAdapterPhoto(
		highlightContext,
		resource.RelationKindSessionResources,
		&otherSessionID,
	)
	if _, err := scanPastHighlightPhotos(
		pastHighlightPhotoRows(photo),
		highlightContext,
	); !errors.Is(err, ErrPastHighlightPreviewFactsConflict) {
		t.Fatalf("scanPastHighlightPhotos(cross Session) error = %v", err)
	}
}

func knownPastHighlightPreviewContext() resource.PastHighlightContext {
	sessionID := pastHighlightAdapterUUID(64)
	return resource.PastHighlightContext{
		SeriesID:                    pastHighlightAdapterUUID(61),
		AnchorInstanceID:            pastHighlightAdapterUUID(62),
		PreviousInstanceID:          pastHighlightAdapterUUID(63),
		FeaturedSessionID:           &sessionID,
		PreviousInstancePublishedAt: pastHighlightAdapterTime(10),
		PreviousInstanceCompletedAt: pastHighlightAdapterTime(12),
	}
}

func knownPastHighlightAdapterPhoto(
	highlightContext resource.PastHighlightContext,
	kind resource.RelationKind,
	sessionID *uuid.UUID,
) resource.PastHighlightPhotoReference {
	return resource.PastHighlightPhotoReference{
		RelationID:        uuid.New(),
		ContentID:         uuid.New(),
		BlockID:           uuid.New(),
		FileID:            uuid.New(),
		SeriesID:          highlightContext.SeriesID,
		InstanceID:        highlightContext.PreviousInstanceID,
		SessionID:         sessionID,
		Kind:              kind,
		MIME:              "image/webp",
		RelationSortOrder: 1,
		BlockSortOrder:    1,
	}
}

func pastHighlightPhotoRows(
	photos ...resource.PastHighlightPhotoReference,
) *fakePublishedResourceRows {
	rows := &fakePublishedResourceRows{
		values: make([][]any, 0, len(photos)),
	}
	for _, photo := range photos {
		sessionID := uuid.NullUUID{}
		if photo.SessionID != nil {
			sessionID = uuid.NullUUID{UUID: *photo.SessionID, Valid: true}
		}
		rows.values = append(rows.values, []any{
			photo.RelationID,
			photo.ContentID,
			photo.BlockID,
			photo.FileID,
			photo.SeriesID,
			photo.InstanceID,
			sessionID,
			photo.Kind,
			photo.MIME,
			photo.RelationSortOrder,
			photo.BlockSortOrder,
		})
	}
	return rows
}
