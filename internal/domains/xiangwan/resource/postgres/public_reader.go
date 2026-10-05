package resourcepostgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

var (
	ErrInvalidPublicResourceQuery = errors.New(
		"invalid xiangwan public resource query",
	)
	ErrPublishedResourceFactsConflict = errors.New(
		"xiangwan published resource facts conflict",
	)
)

const publishedResourceSelect = `
SELECT
    publication.id,
    relation.id,
    relation.tenant_id,
    relation.series_id,
    relation.instance_id,
    relation.session_id,
    relation.relation_kind,
    relation.content_id,
    relation.content_revision_at,
    relation.access_policy,
    relation.sort_order,
    relation.expected_target_version,
    publication.approval_observation_id,
    snapshot.snapshot_schema,
    snapshot.subject_digest,
    publication.published_at
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
JOIN xiangwan_activity_instances AS instance
  ON instance.tenant_id = relation.tenant_id
 AND instance.series_id = relation.series_id
 AND instance.id = relation.instance_id
LEFT JOIN xiangwan_activity_sessions AS session
  ON session.tenant_id = relation.tenant_id
 AND session.instance_id = relation.instance_id
 AND session.id = relation.session_id
`

func (repository *Repository) ListPublishedInstanceReview(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) ([]resource.PublishedResource, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || instanceID == uuid.Nil {
		return nil, ErrInvalidPublicResourceQuery
	}
	rows, err := repository.db.queryContext(ctx, publishedResourceSelect+`
WHERE relation.tenant_id = $1
  AND relation.instance_id = $2
  AND relation.relation_kind = 'instance_review'
  AND relation.session_id IS NULL
  AND relation.access_policy = 'public'
  AND publication.access_policy = 'public'
  AND instance.status IN ('completed', 'archived')
  AND instance.version = relation.expected_target_version
ORDER BY relation.sort_order ASC, publication.published_at ASC, relation.id ASC
LIMIT 201
`, tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan published Instance review: %w", err)
	}
	return scanPublishedResources(rows)
}

func (repository *Repository) ListPublishedSessionResources(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
	principalID *uuid.UUID,
) ([]resource.PublishedResource, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || sessionID == uuid.Nil ||
		(principalID != nil && *principalID == uuid.Nil) {
		return nil, ErrInvalidPublicResourceQuery
	}
	var principalArgument any
	if principalID != nil {
		principalArgument = *principalID
	}
	rows, err := repository.db.queryContext(ctx, publishedResourceSelect+`
WHERE relation.tenant_id = $1
  AND relation.session_id = $2
  AND relation.relation_kind = 'session_resources'
  AND instance.status IN ('published', 'completed', 'archived')
  AND session.status IN ('published', 'ended', 'archived')
  AND session.version = relation.expected_target_version
  AND (
      relation.access_policy = 'public'
      OR (
          relation.access_policy = 'confirmed_registration'
          AND $3::uuid IS NOT NULL
          AND EXISTS (
              SELECT 1
              FROM xiangwan_registrations AS registration
              WHERE registration.tenant_id = relation.tenant_id
                AND registration.series_id = relation.series_id
                AND registration.instance_id = relation.instance_id
                AND registration.session_id = relation.session_id
                AND registration.principal_id = $3
                AND registration.participation_status = 'confirmed'
          )
      )
  )
ORDER BY relation.sort_order ASC, publication.published_at ASC, relation.id ASC
LIMIT 201
`, tenantID, sessionID, principalArgument)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan published Session resources: %w", err)
	}
	return scanPublishedResources(rows)
}

func scanPublishedResources(
	rows resourceRowsScanner,
) ([]resource.PublishedResource, error) {
	if rows == nil {
		return nil, ErrPublishedResourceFactsConflict
	}
	defer func() { _ = rows.Close() }()

	result := make([]resource.PublishedResource, 0)
	for rows.Next() {
		var value resource.PublishedResource
		var sessionID uuid.NullUUID
		var subjectDigest []byte
		if err := rows.Scan(
			&value.PublicationID, &value.RelationID, &value.TenantID,
			&value.SeriesID, &value.InstanceID, &sessionID, &value.Kind,
			&value.ContentID, &value.ContentRevision, &value.AccessPolicy,
			&value.SortOrder, &value.TargetVersion,
			&value.ApprovalObservationID, &value.SnapshotSchema,
			&subjectDigest, &value.PublishedAt,
		); err != nil {
			return nil, fmt.Errorf("scan xiangwan published resource: %w", err)
		}
		if len(subjectDigest) != resource.DigestSize {
			return nil, ErrPublishedResourceFactsConflict
		}
		if sessionID.Valid {
			value.SessionID = &sessionID.UUID
		}
		copy(value.SubjectDigest[:], subjectDigest)
		value.ContentRevision = value.ContentRevision.UTC()
		value.PublishedAt = value.PublishedAt.UTC()
		if resource.ValidatePublishedResource(value) != nil {
			return nil, ErrPublishedResourceFactsConflict
		}
		result = append(result, value)
		if len(result) > resource.MaxPublishedResourcesPerContext {
			return nil, ErrPublishedResourceFactsConflict
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan published resources: %w", err)
	}
	return result, nil
}
