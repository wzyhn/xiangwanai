package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

var (
	ErrInvalidPreviousReviewQuery = errors.New(
		"invalid xiangwan previous review query",
	)
	ErrPreviousReviewFactsConflict = errors.New(
		"xiangwan previous review facts conflict",
	)
)

// previousReviewSelect mirrors the published review chain of
// publicReviewSelect, narrowed to the instance_review documents of the latest
// completed/archived sibling Instance that precedes the current one. The
// published-document existence flag is selected independently from the image
// preview so a link-only review still yields a previous-review identity.
const previousReviewSelect = `
WITH current_instance AS (
    SELECT id, status, completed_at
    FROM xiangwan_activity_instances
    WHERE tenant_id = $1
      AND id = $3
),
previous_instance AS (
    SELECT
        candidate.id,
        candidate.title,
        candidate.completed_at,
        candidate.version
    FROM current_instance
    JOIN xiangwan_activity_instances AS candidate
      ON candidate.tenant_id = $1
     AND candidate.series_id = $2
     AND candidate.id <> current_instance.id
    WHERE candidate.status IN ('completed', 'archived')
      AND candidate.completed_at IS NOT NULL
      AND (
          current_instance.status NOT IN ('completed', 'archived')
          OR current_instance.completed_at IS NULL
          OR (candidate.completed_at, candidate.id) <
             (current_instance.completed_at, current_instance.id)
      )
    ORDER BY candidate.completed_at DESC, candidate.id DESC
    LIMIT 1
),
published_documents AS (
    SELECT
        relation.id AS relation_id,
        relation.content_id,
        relation.created_by,
        relation.sort_order
    FROM previous_instance
    JOIN xiangwan_resource_relations AS relation
      ON relation.tenant_id = $1
     AND relation.series_id = $2
     AND relation.instance_id = previous_instance.id
     AND relation.relation_kind = 'instance_review'
     AND relation.session_id IS NULL
     AND relation.access_policy = 'public'
     AND relation.expected_target_version = previous_instance.version
    JOIN xiangwan_resource_publications AS publication
      ON publication.tenant_id = relation.tenant_id
     AND publication.relation_id = relation.id
 AND NOT EXISTS (SELECT 1 FROM xiangwan_review_resource_replacements AS replacement
                 WHERE replacement.tenant_id = relation.tenant_id
                   AND replacement.previous_relation_id = relation.id)
     AND publication.content_id = relation.content_id
     AND publication.content_revision_at = relation.content_revision_at
     AND publication.access_policy = relation.access_policy
     AND publication.expected_target_version = relation.expected_target_version
    JOIN xiangwan_resource_content_snapshots AS snapshot
      ON snapshot.tenant_id = relation.tenant_id
     AND snapshot.relation_id = relation.id
     AND snapshot.content_id = relation.content_id
     AND snapshot.content_revision_at = relation.content_revision_at
    JOIN xiangwan_resource_moderation_observations AS approval
      ON approval.tenant_id = publication.tenant_id
     AND approval.id = publication.approval_observation_id
     AND approval.relation_id = relation.id
     AND approval.content_id = relation.content_id
     AND approval.content_revision_at = relation.content_revision_at
     AND approval.subject_digest = snapshot.subject_digest
     AND approval.decision = 'approved'
    JOIN contents AS content
      ON content.id = relation.content_id
     AND content.tenant_id = relation.tenant_id
     AND content.principal_id = relation.created_by
     AND content.type = 'review'
     AND content.status IN ('active', 'reviewing')
     AND content.visibility = 'private'
     AND content.updated_at = relation.content_revision_at
     AND content.deleted_at IS NULL
    WHERE NOT EXISTS (
        SELECT 1 FROM content_governance AS governance
        WHERE governance.content_id = relation.content_id
    )
),
review_images AS (
    SELECT
        document.relation_id,
        document.sort_order AS document_sort_order,
        content_block.id AS block_id,
        COALESCE(
            (
                SELECT ordered.ordinality::INTEGER - 1
                FROM jsonb_array_elements_text(curation.ordered_block_ids)
                     WITH ORDINALITY AS ordered(block_id, ordinality)
                WHERE ordered.block_id = content_block.id::TEXT
            ),
            content_block.sort_order
        ) AS block_sort_order,
        stored_file.id AS file_id,
        COALESCE(content_block.data->>'url', '') AS external_url,
        CASE WHEN stored_file.id IS NOT NULL
             THEN 'file:' || stored_file.id::TEXT
             ELSE 'url:' || COALESCE(content_block.data->>'url', '')
        END AS source_key
    FROM published_documents AS document
    JOIN blocks AS content_block
      ON content_block.content_id = document.content_id
     AND content_block.type = 'image'
    LEFT JOIN LATERAL (
        SELECT photo_curation.ordered_block_ids
        FROM xiangwan_review_photo_curations AS photo_curation
        WHERE photo_curation.tenant_id = $1
          AND photo_curation.relation_id = document.relation_id
        ORDER BY photo_curation.version DESC
        LIMIT 1
    ) AS curation ON TRUE
    LEFT JOIN files AS stored_file
      ON stored_file.id = CASE
          WHEN BTRIM(COALESCE(content_block.data->>'file_id', '')) ~*
              '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
          THEN BTRIM(content_block.data->>'file_id')::uuid
          ELSE NULL::uuid
      END
     AND stored_file.principal_id = document.created_by
     AND stored_file.status = 'confirmed'
     AND stored_file.size > 0
     AND stored_file.delete_after IS NULL
     AND stored_file.expired_at IS NULL
     AND stored_file.mime LIKE 'image/%'
     AND stored_file.mime <> 'image/svg+xml'
    WHERE (curation.ordered_block_ids IS NULL
           OR curation.ordered_block_ids ? content_block.id::TEXT)
      AND (stored_file.id IS NOT NULL
           OR NULLIF(BTRIM(COALESCE(content_block.data->>'url', '')), '') IS NOT NULL)
),
distinct_review_images AS (
    -- Collapse duplicate File or URL sources before the preview bound.
    SELECT DISTINCT ON (review_images.source_key)
        review_images.relation_id,
        review_images.document_sort_order,
        review_images.block_id,
        review_images.block_sort_order,
        review_images.file_id,
        review_images.external_url
    FROM review_images
    ORDER BY
        review_images.source_key,
        review_images.document_sort_order ASC,
        review_images.relation_id ASC,
        review_images.block_sort_order ASC,
        review_images.block_id ASC
),
bounded_review_images AS (
    -- The public projection needs at most MaxPreviousReviewImages (3)
    -- previews; the scan bound keeps one pathological review document from
    -- streaming its whole image block chain into the session detail read.
    -- The ordering mirrors the final ORDER BY exactly, so the first three
    -- rows after the outer sort stay the semantically-first three images.
    -- The bound applies only after source dedupe, never before it.
    SELECT
        relation_id,
        document_sort_order,
        block_id,
        block_sort_order,
        file_id,
        external_url
    FROM distinct_review_images
    ORDER BY
        document_sort_order ASC,
        relation_id ASC,
        block_sort_order ASC,
        block_id ASC
    LIMIT 32
)
SELECT
    previous.id,
    previous.title,
    previous.completed_at,
    EXISTS (
        SELECT 1
        FROM published_documents AS visible_document
        JOIN blocks AS visible_block
          ON visible_block.content_id = visible_document.content_id
         AND visible_block.type IN ('text', 'image', 'link', 'file', 'video', 'audio')
        LEFT JOIN LATERAL (
            SELECT photo_curation.ordered_block_ids
            FROM xiangwan_review_photo_curations AS photo_curation
            WHERE photo_curation.tenant_id = $1
              AND photo_curation.relation_id = visible_document.relation_id
            ORDER BY photo_curation.version DESC
            LIMIT 1
        ) AS visible_curation ON TRUE
        WHERE (visible_block.type <> 'link' OR COALESCE(visible_block.data->>'enabled', 'true') <> 'false')
          AND (visible_block.type <> 'image'
           OR visible_curation.ordered_block_ids IS NULL
           OR visible_curation.ordered_block_ids ? visible_block.id::TEXT)
    ) AS has_published_review,
    review.relation_id,
    review.document_sort_order,
    review.block_id,
    review.block_sort_order,
    review.file_id,
    review.external_url
FROM previous_instance AS previous
LEFT JOIN bounded_review_images AS review ON TRUE
ORDER BY
    review.document_sort_order ASC,
    review.relation_id ASC,
    review.block_sort_order ASC,
    review.block_id ASC
`

// ReadPreviousInstanceReview reads the previous completed/archived sibling
// Instance plus the available image Blocks of its published review documents.
// The absent case is an empty result, never an error.
func (repository *Repository) ReadPreviousInstanceReview(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
	currentInstanceID uuid.UUID,
) (resource.PreviousReviewFacts, error) {
	if repository == nil || repository.db == nil || tenantID == uuid.Nil ||
		seriesID == uuid.Nil || currentInstanceID == uuid.Nil {
		return resource.PreviousReviewFacts{}, ErrInvalidPreviousReviewQuery
	}
	rows, err := repository.db.queryContext(
		ctx,
		previousReviewSelect,
		tenantID,
		seriesID,
		currentInstanceID,
	)
	if err != nil {
		return resource.PreviousReviewFacts{}, fmt.Errorf(
			"read xiangwan previous Instance review: %w",
			err,
		)
	}
	return scanPreviousInstanceReview(rows)
}

func scanPreviousInstanceReview(
	rows resourceRowsScanner,
) (resource.PreviousReviewFacts, error) {
	if rows == nil {
		return resource.PreviousReviewFacts{}, ErrPreviousReviewFactsConflict
	}
	defer func() { _ = rows.Close() }()

	var instance *resource.PreviousInstanceFacts
	images := make([]resource.PreviousReviewImageFacts, 0)
	hasPublishedReview := false
	rowCount := 0
	for rows.Next() {
		rowCount++
		var instanceID uuid.UUID
		var title string
		var completedAt time.Time
		var rowHasPublishedReview bool
		var relationID uuid.NullUUID
		var documentSortOrder sql.NullInt64
		var blockID uuid.NullUUID
		var blockSortOrder sql.NullInt64
		var fileID uuid.NullUUID
		var externalURL sql.NullString
		if err := rows.Scan(
			&instanceID,
			&title,
			&completedAt,
			&rowHasPublishedReview,
			&relationID,
			&documentSortOrder,
			&blockID,
			&blockSortOrder,
			&fileID,
			&externalURL,
		); err != nil {
			return resource.PreviousReviewFacts{}, fmt.Errorf(
				"scan xiangwan previous Instance review: %w",
				err,
			)
		}
		if instance == nil {
			instance = &resource.PreviousInstanceFacts{
				InstanceID:  instanceID,
				Title:       title,
				CompletedAt: completedAt.UTC(),
			}
		} else if instance.InstanceID != instanceID ||
			instance.Title != title ||
			!instance.CompletedAt.Equal(completedAt.UTC()) {
			return resource.PreviousReviewFacts{}, ErrPreviousReviewFactsConflict
		}
		if rowHasPublishedReview {
			// The flag is repeated for every image row by the outer query. Keep
			// it as a fact on the whole result so link-only reviews survive the
			// image projection even when the LEFT JOIN has no image row.
			// A false value can never erase a true value from a prior row.
			// (The query currently returns a single previous Instance.)
			//
			// This assignment is intentionally below the identity consistency
			// check so a malformed mixed-instance result still fails closed.
			hasPublishedReview = true
		}
		if !relationID.Valid {
			if documentSortOrder.Valid || blockID.Valid || blockSortOrder.Valid ||
				fileID.Valid || externalURL.Valid || rowCount != 1 {
				return resource.PreviousReviewFacts{}, ErrPreviousReviewFactsConflict
			}
			continue
		}
		if !documentSortOrder.Valid || !blockID.Valid || !blockSortOrder.Valid ||
			!externalURL.Valid || (!fileID.Valid && externalURL.String == "") {
			return resource.PreviousReviewFacts{}, ErrPreviousReviewFactsConflict
		}
		images = append(images, resource.PreviousReviewImageFacts{
			RelationID:        relationID.UUID,
			DocumentSortOrder: int(documentSortOrder.Int64),
			BlockID:           blockID.UUID,
			BlockSortOrder:    int(blockSortOrder.Int64),
			FileID:            fileID.UUID,
			ExternalURL:       externalURL.String,
		})
		if len(images) > resource.MaxPublicReviewBlocks {
			return resource.PreviousReviewFacts{}, ErrPreviousReviewFactsConflict
		}
	}
	if err := rows.Err(); err != nil {
		return resource.PreviousReviewFacts{}, fmt.Errorf(
			"iterate xiangwan previous Instance review: %w",
			err,
		)
	}
	return resource.PreviousReviewFacts{
		Instance:           instance,
		HasPublishedReview: hasPublishedReview,
		Images:             images,
	}, nil
}
