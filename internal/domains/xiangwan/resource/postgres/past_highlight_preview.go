package resourcepostgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

var (
	ErrInvalidPastHighlightPreviewQuery = errors.New(
		"invalid xiangwan past-highlight preview query",
	)
	ErrPastHighlightPreviewFactsConflict = errors.New(
		"xiangwan past-highlight preview facts conflict",
	)
)

const pastHighlightPhotoSelect = `
WITH eligible_photos AS (
    SELECT
        relation.id AS relation_id,
        relation.content_id,
        content_block.id AS block_id,
        stored_file.id AS file_id,
        relation.series_id,
        relation.instance_id,
        relation.session_id,
        relation.relation_kind,
        stored_file.mime,
        relation.sort_order AS relation_sort_order,
        COALESCE(
            (
                SELECT ordered.ordinality::INTEGER - 1
                FROM jsonb_array_elements_text(curation.ordered_block_ids)
                     WITH ORDINALITY AS ordered(block_id, ordinality)
                WHERE ordered.block_id = content_block.id::TEXT
            ),
            content_block.sort_order
        ) AS block_sort_order,
        ROW_NUMBER() OVER (
            PARTITION BY stored_file.id
            ORDER BY
                CASE relation.relation_kind
                    WHEN 'instance_review' THEN 0
                    ELSE 1
                END ASC,
                relation.sort_order ASC,
                COALESCE(
                    (
                        SELECT ordered.ordinality::INTEGER - 1
                        FROM jsonb_array_elements_text(curation.ordered_block_ids)
                             WITH ORDINALITY AS ordered(block_id, ordinality)
                        WHERE ordered.block_id = content_block.id::TEXT
                    ),
                    content_block.sort_order
                ) ASC,
                content_block.id ASC
        ) AS file_rank
    FROM xiangwan_resource_publications AS publication
    JOIN xiangwan_resource_relations AS relation
      ON relation.tenant_id = publication.tenant_id
     AND relation.id = publication.relation_id
 AND NOT EXISTS (SELECT 1 FROM xiangwan_review_resource_replacements AS replacement
                 WHERE replacement.tenant_id = relation.tenant_id
                   AND replacement.previous_relation_id = relation.id)
     AND relation.content_id = publication.content_id
     AND relation.content_revision_at = publication.content_revision_at
     AND relation.access_policy = publication.access_policy
     AND relation.expected_target_version = publication.expected_target_version
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
    JOIN xiangwan_activity_instances AS activity_instance
      ON activity_instance.tenant_id = relation.tenant_id
     AND activity_instance.series_id = relation.series_id
     AND activity_instance.id = relation.instance_id
    LEFT JOIN xiangwan_activity_sessions AS activity_session
      ON activity_session.tenant_id = relation.tenant_id
     AND activity_session.instance_id = relation.instance_id
     AND activity_session.id = relation.session_id
    JOIN blocks AS content_block
      ON content_block.content_id = relation.content_id
     AND content_block.type = 'image'
    LEFT JOIN LATERAL (
        SELECT photo_curation.ordered_block_ids
        FROM xiangwan_review_photo_curations AS photo_curation
        WHERE photo_curation.tenant_id = relation.tenant_id
          AND photo_curation.relation_id = relation.id
        ORDER BY photo_curation.version DESC
        LIMIT 1
    ) AS curation ON TRUE
    JOIN contents AS content
      ON content.id = relation.content_id
     AND content.tenant_id = relation.tenant_id
     AND content.principal_id = relation.created_by
     AND content.type = 'review'
     AND content.status IN ('active', 'reviewing')
     AND content.visibility = 'private'
     AND content.updated_at = relation.content_revision_at
     AND content.deleted_at IS NULL
    JOIN files AS stored_file
      ON stored_file.id = CASE
          WHEN BTRIM(COALESCE(content_block.data->>'file_id', '')) ~*
              '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
          THEN BTRIM(content_block.data->>'file_id')::uuid
          ELSE NULL::uuid
      END
     AND stored_file.principal_id = relation.created_by
    WHERE relation.tenant_id = $1
      AND relation.series_id = $2
      AND relation.instance_id = $3
      AND relation.access_policy = 'public'
      AND publication.access_policy = 'public'
      AND NOT EXISTS (
          SELECT 1 FROM content_governance AS governance
          WHERE governance.content_id = relation.content_id
      )
      AND activity_instance.status IN ('completed', 'archived')
      AND stored_file.status = 'confirmed'
      AND LOWER(BTRIM(stored_file.mime)) LIKE 'image/%'
      AND stored_file.size > 0
      AND stored_file.delete_after IS NULL
      AND stored_file.expired_at IS NULL
      AND (curation.ordered_block_ids IS NULL
           OR curation.ordered_block_ids ? content_block.id::TEXT)
      AND (
          (
              relation.relation_kind = 'instance_review'
              AND relation.session_id IS NULL
              AND activity_instance.version = relation.expected_target_version
          )
          OR (
              relation.relation_kind = 'session_resources'
              AND $4::uuid IS NOT NULL
              AND relation.session_id = $4
              AND activity_session.status IN ('ended', 'archived')
              AND activity_session.version = relation.expected_target_version
          )
      )
)
SELECT
    relation_id,
    content_id,
    block_id,
    file_id,
    series_id,
    instance_id,
    session_id,
    relation_kind,
    mime,
    relation_sort_order,
    block_sort_order
FROM eligible_photos
WHERE file_rank = 1
ORDER BY
    CASE relation_kind WHEN 'instance_review' THEN 0 ELSE 1 END ASC,
    relation_sort_order ASC,
    block_sort_order ASC,
    block_id ASC
LIMIT 3
`

func (repository *Repository) ReadPastHighlightPreview(
	ctx context.Context,
	tenantID uuid.UUID,
	anchorInstanceID uuid.UUID,
) (*resource.PastHighlightPreview, error) {
	context, err := repository.ReadPastHighlightContext(
		ctx,
		tenantID,
		anchorInstanceID,
	)
	if err != nil || context == nil {
		return nil, err
	}
	photos, err := repository.ListPastHighlightPhotoReferences(
		ctx,
		tenantID,
		*context,
	)
	if err != nil {
		return nil, err
	}
	preview, err := resource.BuildPastHighlightPreview(*context, photos)
	if err != nil {
		return nil, ErrPastHighlightPreviewFactsConflict
	}
	return &preview, nil
}

func (repository *Repository) ListPastHighlightPhotoReferences(
	ctx context.Context,
	tenantID uuid.UUID,
	highlightContext resource.PastHighlightContext,
) ([]resource.PastHighlightPhotoReference, error) {
	if repository == nil || repository.db == nil || tenantID == uuid.Nil {
		return nil, ErrInvalidPastHighlightPreviewQuery
	}
	if _, err := resource.BuildPastHighlightPreview(
		highlightContext,
		nil,
	); err != nil {
		return nil, ErrInvalidPastHighlightPreviewQuery
	}
	var sessionArgument any
	if highlightContext.FeaturedSessionID != nil {
		sessionArgument = *highlightContext.FeaturedSessionID
	}
	rows, err := repository.db.queryContext(
		ctx,
		pastHighlightPhotoSelect,
		tenantID,
		highlightContext.SeriesID,
		highlightContext.PreviousInstanceID,
		sessionArgument,
	)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan past-highlight photos: %w", err)
	}
	return scanPastHighlightPhotos(rows, highlightContext)
}

func scanPastHighlightPhotos(
	rows resourceRowsScanner,
	highlightContext resource.PastHighlightContext,
) ([]resource.PastHighlightPhotoReference, error) {
	if rows == nil {
		return nil, ErrPastHighlightPreviewFactsConflict
	}
	defer func() { _ = rows.Close() }()

	photos := make([]resource.PastHighlightPhotoReference, 0)
	for rows.Next() {
		var photo resource.PastHighlightPhotoReference
		var sessionID uuid.NullUUID
		if err := rows.Scan(
			&photo.RelationID,
			&photo.ContentID,
			&photo.BlockID,
			&photo.FileID,
			&photo.SeriesID,
			&photo.InstanceID,
			&sessionID,
			&photo.Kind,
			&photo.MIME,
			&photo.RelationSortOrder,
			&photo.BlockSortOrder,
		); err != nil {
			return nil, fmt.Errorf("scan xiangwan past-highlight photo: %w", err)
		}
		if sessionID.Valid {
			photo.SessionID = &sessionID.UUID
		}
		photos = append(photos, photo)
		if len(photos) > resource.MaxPastHighlightPreviewPhotos {
			return nil, ErrPastHighlightPreviewFactsConflict
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan past-highlight photos: %w", err)
	}
	preview, err := resource.BuildPastHighlightPreview(highlightContext, photos)
	if err != nil {
		return nil, ErrPastHighlightPreviewFactsConflict
	}
	return preview.Photos, nil
}
