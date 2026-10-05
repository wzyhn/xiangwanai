package resourcepostgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

var (
	ErrInvalidPublicFileQuery = errors.New(
		"invalid xiangwan public file query",
	)
	ErrPublicFileNotFound      = resource.ErrPublicFileUnavailable
	ErrPublicFileFactsConflict = errors.New(
		"xiangwan public file facts conflict",
	)
)

const publicFileGrantSelect = `
SELECT
    relation.tenant_id,
    relation.id,
    relation.content_id,
    content_block.id,
    stored_file.id,
    relation.series_id,
    relation.instance_id,
    relation.session_id,
    relation.relation_kind,
    content_block.type,
    LOWER(BTRIM(stored_file.mime)),
    stored_file.size
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
JOIN contents AS content
  ON content.id = relation.content_id
 AND content.tenant_id = relation.tenant_id
 AND content.principal_id = relation.created_by
 AND content.type = 'review'
 AND content.status IN ('active', 'reviewing')
 AND content.visibility = 'private'
 AND content.updated_at = relation.content_revision_at
 AND content.deleted_at IS NULL
JOIN blocks AS content_block
  ON content_block.content_id = relation.content_id
 AND content_block.id = $3
 AND content_block.type IN ('image', 'file', 'video', 'audio')
LEFT JOIN LATERAL (
    SELECT photo_curation.ordered_block_ids
    FROM xiangwan_review_photo_curations AS photo_curation
    WHERE photo_curation.tenant_id = relation.tenant_id
      AND photo_curation.relation_id = relation.id
    ORDER BY photo_curation.version DESC
    LIMIT 1
) AS curation ON TRUE
JOIN files AS stored_file
  ON stored_file.id = $4
 AND stored_file.id = CASE
      WHEN BTRIM(COALESCE(content_block.data->>'file_id', '')) ~*
          '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
      THEN BTRIM(content_block.data->>'file_id')::uuid
      ELSE NULL::uuid
  END
 AND stored_file.principal_id = relation.created_by
 AND stored_file.status = 'confirmed'
 AND stored_file.size > 0
 AND stored_file.delete_after IS NULL
 AND stored_file.expired_at IS NULL
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = relation.tenant_id
 AND activity_instance.series_id = relation.series_id
 AND activity_instance.id = relation.instance_id
LEFT JOIN xiangwan_activity_sessions AS activity_session
  ON activity_session.tenant_id = relation.tenant_id
 AND activity_session.instance_id = relation.instance_id
 AND activity_session.id = relation.session_id
WHERE relation.tenant_id = $1
  AND relation.id = $2
  AND relation.access_policy = 'public'
  AND publication.access_policy = 'public'
  AND (content_block.type <> 'image'
       OR curation.ordered_block_ids IS NULL
       OR curation.ordered_block_ids ? content_block.id::TEXT)
  AND NOT EXISTS (
      SELECT 1 FROM content_governance AS governance
      WHERE governance.content_id = relation.content_id
  )
  AND (
      (
          relation.relation_kind = 'instance_review'
          AND relation.session_id IS NULL
          AND activity_instance.status IN ('completed', 'archived')
          AND activity_instance.version = relation.expected_target_version
      )
      OR (
          relation.relation_kind = 'session_resources'
          AND relation.session_id IS NOT NULL
          AND activity_instance.status IN ('published', 'completed', 'archived')
          AND activity_session.status IN ('published', 'ended', 'archived')
          AND activity_session.version = relation.expected_target_version
      )
  )
LIMIT 2
`

func (repository *Repository) AuthorizePublicFile(
	ctx context.Context,
	tenantID uuid.UUID,
	relationID uuid.UUID,
	blockID uuid.UUID,
	fileID uuid.UUID,
) (resource.PublicFileGrant, error) {
	if repository == nil || repository.db == nil || tenantID == uuid.Nil ||
		relationID == uuid.Nil || blockID == uuid.Nil || fileID == uuid.Nil {
		return resource.PublicFileGrant{}, ErrInvalidPublicFileQuery
	}
	rows, err := repository.db.queryContext(
		ctx,
		publicFileGrantSelect,
		tenantID,
		relationID,
		blockID,
		fileID,
	)
	if err != nil {
		return resource.PublicFileGrant{}, fmt.Errorf(
			"authorize xiangwan public file: %w",
			err,
		)
	}
	return scanPublicFileGrant(rows)
}

func scanPublicFileGrant(
	rows resourceRowsScanner,
) (resource.PublicFileGrant, error) {
	if rows == nil {
		return resource.PublicFileGrant{}, ErrPublicFileFactsConflict
	}
	defer func() { _ = rows.Close() }()

	var grant resource.PublicFileGrant
	rowCount := 0
	for rows.Next() {
		rowCount++
		if rowCount > 1 {
			return resource.PublicFileGrant{}, ErrPublicFileFactsConflict
		}
		var sessionID uuid.NullUUID
		if err := rows.Scan(
			&grant.TenantID,
			&grant.RelationID,
			&grant.ContentID,
			&grant.BlockID,
			&grant.FileID,
			&grant.SeriesID,
			&grant.InstanceID,
			&sessionID,
			&grant.Kind,
			&grant.BlockType,
			&grant.MIME,
			&grant.Size,
		); err != nil {
			return resource.PublicFileGrant{}, fmt.Errorf(
				"scan xiangwan public file grant: %w",
				err,
			)
		}
		if sessionID.Valid {
			grant.SessionID = &sessionID.UUID
		}
		grant.MIME = strings.ToLower(strings.TrimSpace(grant.MIME))
		if resource.ValidatePublicFileGrant(grant) != nil {
			return resource.PublicFileGrant{}, ErrPublicFileFactsConflict
		}
	}
	if err := rows.Err(); err != nil {
		return resource.PublicFileGrant{}, fmt.Errorf(
			"iterate xiangwan public file grant: %w",
			err,
		)
	}
	if rowCount == 0 {
		return resource.PublicFileGrant{}, ErrPublicFileNotFound
	}
	return grant, nil
}
