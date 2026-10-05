package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

var (
	ErrInvalidPublicReviewQuery = errors.New(
		"invalid xiangwan public review query",
	)
	ErrPublicReviewTargetNotFound = resource.ErrPublicReviewTargetUnavailable
	ErrPublicReviewFactsConflict  = errors.New(
		"xiangwan public review facts conflict",
	)
)

const publicReviewSelect = `
WITH target_scope AS (
    SELECT
        activity_instance.series_id,
        activity_instance.id AS instance_id,
        target_session.id AS session_id
    FROM xiangwan_activity_instances AS activity_instance
    JOIN xiangwan_activity_series AS activity_series
      ON activity_series.tenant_id = activity_instance.tenant_id
     AND activity_series.id = activity_instance.series_id
    LEFT JOIN xiangwan_activity_sessions AS target_session
      ON target_session.tenant_id = activity_instance.tenant_id
     AND target_session.instance_id = activity_instance.id
     AND target_session.id = $4
    WHERE activity_instance.tenant_id = $1
      AND activity_instance.series_id = $2
      AND activity_instance.id = $3
      AND activity_instance.status IN ('completed', 'archived')
      AND activity_series.status IN ('active', 'archived')
      AND (
          $4::uuid IS NULL
          OR target_session.status IN ('ended', 'archived')
      )
),
published_documents AS (
    SELECT
        relation.id AS relation_id,
        relation.content_id,
        relation.series_id,
        relation.instance_id,
        relation.session_id,
        relation.relation_kind,
        COALESCE(content.title, '') AS title,
        relation.sort_order,
        relation.created_by
    FROM target_scope AS target
    JOIN xiangwan_resource_publications AS publication
      ON publication.tenant_id = $1
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
     AND relation.series_id = target.series_id
     AND relation.instance_id = target.instance_id
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
    JOIN xiangwan_activity_instances AS activity_instance
      ON activity_instance.tenant_id = relation.tenant_id
     AND activity_instance.series_id = relation.series_id
     AND activity_instance.id = relation.instance_id
    LEFT JOIN xiangwan_activity_sessions AS activity_session
      ON activity_session.tenant_id = relation.tenant_id
     AND activity_session.instance_id = relation.instance_id
     AND activity_session.id = relation.session_id
    WHERE relation.access_policy = 'public'
      AND publication.access_policy = 'public'
      AND NOT EXISTS (
          SELECT 1 FROM content_governance AS governance
          WHERE governance.content_id = relation.content_id
      )
      AND (
          (
              relation.relation_kind = 'instance_review'
              AND relation.session_id IS NULL
              AND activity_instance.version = relation.expected_target_version
          )
          OR (
              relation.relation_kind = 'session_resources'
              AND target.session_id IS NOT NULL
              AND relation.session_id = target.session_id
              AND activity_session.status IN ('ended', 'archived')
              AND activity_session.version = relation.expected_target_version
          )
      )
    ORDER BY
        CASE relation.relation_kind
            WHEN 'instance_review' THEN 0
            ELSE 1
        END ASC,
        relation.sort_order ASC,
        relation.id ASC
    LIMIT 51
),
review_blocks AS (
    SELECT
        document.relation_id,
        content_block.id AS block_id,
        content_block.type AS block_type,
        CASE
            WHEN content_block.type = 'image' AND curation.ordered_block_ids IS NOT NULL
            THEN (
                SELECT image_slot.sort_order
                FROM (
                    SELECT original_image.sort_order,
                           ROW_NUMBER() OVER (
                               ORDER BY original_image.sort_order, original_image.id
                           ) AS ordinal
                    FROM blocks AS original_image
                    WHERE original_image.content_id = document.content_id
                      AND original_image.type = 'image'
                ) AS image_slot
                WHERE image_slot.ordinal = (
                    SELECT ordered.ordinality
                    FROM jsonb_array_elements_text(curation.ordered_block_ids)
                         WITH ORDINALITY AS ordered(block_id, ordinality)
                    WHERE ordered.block_id = content_block.id::TEXT
                )
            )
            ELSE content_block.sort_order
        END AS block_sort_order,
        CASE
            WHEN content_block.type = 'text'
            THEN COALESCE(content_block.data->>'text', '')
            ELSE ''
        END AS block_text,
        CASE
            WHEN content_block.type <> 'text'
            THEN COALESCE(
                NULLIF(content_block.data->>'label', ''),
                NULLIF(content_block.data->>'title', ''),
                NULLIF(content_block.data->>'name', ''),
                ''
            )
            ELSE ''
        END AS block_label,
        CASE
            WHEN content_block.type <> 'text'
            THEN COALESCE(content_block.data->>'subtitle', '')
            ELSE ''
        END AS block_subtitle,
        CASE
            WHEN content_block.type IN ('link', 'image')
            THEN COALESCE(content_block.data->>'url', '')
            ELSE ''
        END AS external_url,
        stored_file.id AS file_id,
        stored_file.mime,
        CASE WHEN content_block.type = 'link' AND content_block.data->>'kind' = 'video_channel_native' THEN COALESCE(content_block.data->>'finder_user_name','') ELSE '' END AS finder_user_name,
        CASE WHEN content_block.type = 'link' AND content_block.data->>'kind' = 'video_channel_native' THEN COALESCE(content_block.data->>'feed_id','') ELSE '' END AS feed_id,
        COALESCE(content_block.type = 'image' AND curation.cover_block_id = content_block.id, FALSE) AS is_cover
    FROM published_documents AS document
    JOIN blocks AS content_block
      ON content_block.content_id = document.content_id
     AND content_block.type IN ('text', 'image', 'link', 'file', 'video', 'audio')
    LEFT JOIN LATERAL (
        SELECT photo_curation.ordered_block_ids, photo_curation.cover_block_id
        FROM xiangwan_review_photo_curations AS photo_curation
        WHERE photo_curation.tenant_id = $1
          AND photo_curation.relation_id = document.relation_id
        ORDER BY photo_curation.version DESC
        LIMIT 1
    ) AS curation ON TRUE
    LEFT JOIN files AS stored_file
      ON content_block.type IN ('image', 'file', 'video', 'audio')
     AND stored_file.id = CASE
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
    WHERE (content_block.type <> 'link' OR COALESCE(content_block.data->>'enabled', 'true') <> 'false')
      AND (content_block.type <> 'image'
       OR curation.ordered_block_ids IS NULL
       OR curation.ordered_block_ids ? content_block.id::TEXT)
)
SELECT
    target.series_id,
    target.instance_id,
    target.session_id,
    document.relation_id,
    document.content_id,
    document.series_id,
    document.instance_id,
    document.session_id,
    document.relation_kind,
    document.title,
    document.sort_order,
    review_block.block_id,
    review_block.block_type,
    review_block.block_sort_order,
    review_block.block_text,
    review_block.block_label,
    review_block.block_subtitle,
    review_block.external_url,
    review_block.file_id,
    review_block.mime, review_block.finder_user_name, review_block.feed_id, review_block.is_cover
FROM target_scope AS target
LEFT JOIN published_documents AS document ON TRUE
LEFT JOIN review_blocks AS review_block
  ON review_block.relation_id = document.relation_id
ORDER BY
    CASE document.relation_kind
        WHEN 'instance_review' THEN 0
        ELSE 1
    END ASC,
    document.sort_order ASC,
    document.relation_id ASC,
    review_block.block_sort_order ASC,
    review_block.block_id ASC
LIMIT 502
`

func (repository *Repository) ReadPublicReview(
	ctx context.Context,
	tenantID uuid.UUID,
	target resource.PastHighlightReviewTarget,
	externalDomains resource.ExternalDomainPolicy,
) (resource.PublicReviewDetail, error) {
	if repository == nil || repository.db == nil || tenantID == uuid.Nil {
		return resource.PublicReviewDetail{}, ErrInvalidPublicReviewQuery
	}
	if _, err := resource.ProjectPublicReview(
		target,
		nil,
		externalDomains,
	); err != nil {
		return resource.PublicReviewDetail{}, ErrInvalidPublicReviewQuery
	}
	var sessionArgument any
	if target.SessionID != nil {
		sessionArgument = *target.SessionID
	}
	rows, err := repository.db.queryContext(
		ctx,
		publicReviewSelect,
		tenantID,
		target.SeriesID,
		target.InstanceID,
		sessionArgument,
	)
	if err != nil {
		return resource.PublicReviewDetail{}, fmt.Errorf(
			"read xiangwan public review: %w",
			err,
		)
	}
	return scanPublicReview(rows, target, externalDomains)
}

func scanPublicReview(
	rows resourceRowsScanner,
	target resource.PastHighlightReviewTarget,
	externalDomains resource.ExternalDomainPolicy,
) (resource.PublicReviewDetail, error) {
	if rows == nil {
		return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
	}
	defer func() { _ = rows.Close() }()

	documents := make([]resource.PublicReviewDocumentFacts, 0)
	documentIndexes := make(map[uuid.UUID]int)
	rowCount := 0
	blockCount := 0
	for rows.Next() {
		rowCount++
		var rowTargetSessionID uuid.NullUUID
		var rowTargetSeriesID uuid.UUID
		var rowTargetInstanceID uuid.UUID
		var relationID uuid.NullUUID
		var contentID uuid.NullUUID
		var documentSeriesID uuid.NullUUID
		var documentInstanceID uuid.NullUUID
		var documentSessionID uuid.NullUUID
		var relationKind sql.NullString
		var documentTitle sql.NullString
		var relationSortOrder sql.NullInt64
		var blockID uuid.NullUUID
		var blockType sql.NullString
		var blockSortOrder sql.NullInt64
		var blockText sql.NullString
		var blockLabel sql.NullString
		var blockSubtitle sql.NullString
		var externalURL sql.NullString
		var fileID uuid.NullUUID
		var fileMIME sql.NullString
		var finderUserName, feedID sql.NullString
		var isCover sql.NullBool
		if err := rows.Scan(
			&rowTargetSeriesID,
			&rowTargetInstanceID,
			&rowTargetSessionID,
			&relationID,
			&contentID,
			&documentSeriesID,
			&documentInstanceID,
			&documentSessionID,
			&relationKind,
			&documentTitle,
			&relationSortOrder,
			&blockID,
			&blockType,
			&blockSortOrder,
			&blockText,
			&blockLabel,
			&blockSubtitle,
			&externalURL,
			&fileID,
			&fileMIME, &finderUserName, &feedID, &isCover,
		); err != nil {
			return resource.PublicReviewDetail{}, fmt.Errorf(
				"scan xiangwan public review: %w",
				err,
			)
		}
		if !publicReviewTargetMatches(
			target,
			rowTargetSeriesID,
			rowTargetInstanceID,
			rowTargetSessionID,
		) {
			return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
		}
		if !relationID.Valid {
			if contentID.Valid || documentSeriesID.Valid ||
				documentInstanceID.Valid || documentSessionID.Valid ||
				relationKind.Valid || documentTitle.Valid ||
				relationSortOrder.Valid || blockID.Valid || blockType.Valid ||
				blockSortOrder.Valid || blockText.Valid || blockLabel.Valid ||
				blockSubtitle.Valid || externalURL.Valid || fileID.Valid ||
				fileMIME.Valid || finderUserName.Valid || feedID.Valid || isCover.Valid || rowCount != 1 {
				return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
			}
			continue
		}
		if !contentID.Valid || !documentSeriesID.Valid ||
			!documentInstanceID.Valid || !relationKind.Valid ||
			!documentTitle.Valid || !relationSortOrder.Valid {
			return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
		}
		index, exists := documentIndexes[relationID.UUID]
		if !exists {
			document := resource.PublicReviewDocumentFacts{
				RelationID:        relationID.UUID,
				ContentID:         contentID.UUID,
				SeriesID:          documentSeriesID.UUID,
				InstanceID:        documentInstanceID.UUID,
				Kind:              resource.RelationKind(relationKind.String),
				Title:             documentTitle.String,
				RelationSortOrder: int(relationSortOrder.Int64),
			}
			if documentSessionID.Valid {
				document.SessionID = &documentSessionID.UUID
			}
			documents = append(documents, document)
			if len(documents) > resource.MaxPublicReviewDocuments {
				return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
			}
			index = len(documents) - 1
			documentIndexes[relationID.UUID] = index
		} else if !samePublicReviewDocumentRow(
			documents[index],
			contentID.UUID,
			documentSeriesID.UUID,
			documentInstanceID.UUID,
			documentSessionID,
			relationKind.String,
			documentTitle.String,
			int(relationSortOrder.Int64),
		) {
			return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
		}
		if !blockID.Valid {
			if blockType.Valid || blockSortOrder.Valid || blockText.Valid ||
				blockLabel.Valid || blockSubtitle.Valid || externalURL.Valid ||
				fileID.Valid || fileMIME.Valid || finderUserName.Valid || feedID.Valid || isCover.Valid {
				return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
			}
			continue
		}
		if !blockType.Valid || !blockSortOrder.Valid || !blockText.Valid ||
			!blockLabel.Valid || !blockSubtitle.Valid || !externalURL.Valid || !isCover.Valid {
			return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
		}
		block := resource.PublicReviewBlockFacts{
			BlockID:     blockID.UUID,
			Type:        resource.PublicReviewBlockType(blockType.String),
			SortOrder:   int(blockSortOrder.Int64),
			Text:        blockText.String,
			Label:       blockLabel.String,
			Subtitle:    blockSubtitle.String,
			ExternalURL: externalURL.String,
			MIME:        fileMIME.String,
			IsCover:     isCover.Bool,
		}
		if finderUserName.String != "" || feedID.String != "" {
			block.VideoChannel = &resource.ReviewVideoChannel{FinderUserName: finderUserName.String, FeedID: feedID.String}
		}
		if fileID.Valid != fileMIME.Valid {
			return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
		}
		if fileID.Valid {
			block.FileID = &fileID.UUID
		}
		documents[index].Blocks = append(documents[index].Blocks, block)
		blockCount++
		if blockCount > resource.MaxPublicReviewBlocks {
			return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
		}
	}
	if err := rows.Err(); err != nil {
		return resource.PublicReviewDetail{}, fmt.Errorf(
			"iterate xiangwan public review: %w",
			err,
		)
	}
	if rowCount == 0 {
		return resource.PublicReviewDetail{}, ErrPublicReviewTargetNotFound
	}
	detail, err := resource.ProjectPublicReview(
		target,
		documents,
		externalDomains,
	)
	if err != nil {
		return resource.PublicReviewDetail{}, ErrPublicReviewFactsConflict
	}
	return detail, nil
}

func publicReviewTargetMatches(
	want resource.PastHighlightReviewTarget,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.NullUUID,
) bool {
	if seriesID != want.SeriesID || instanceID != want.InstanceID {
		return false
	}
	if want.SessionID == nil {
		return !sessionID.Valid
	}
	return sessionID.Valid && sessionID.UUID == *want.SessionID
}

func samePublicReviewDocumentRow(
	want resource.PublicReviewDocumentFacts,
	contentID uuid.UUID,
	seriesID uuid.UUID,
	instanceID uuid.UUID,
	sessionID uuid.NullUUID,
	kind string,
	title string,
	sortOrder int,
) bool {
	if want.ContentID != contentID || want.SeriesID != seriesID ||
		want.InstanceID != instanceID || want.Kind != resource.RelationKind(kind) ||
		want.Title != title || want.RelationSortOrder != sortOrder {
		return false
	}
	if want.SessionID == nil {
		return !sessionID.Valid
	}
	return sessionID.Valid && sessionID.UUID == *want.SessionID
}
