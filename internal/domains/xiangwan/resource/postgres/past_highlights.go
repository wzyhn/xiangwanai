package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

const maxPastHighlightSessionCandidates = 200

var (
	ErrInvalidPastHighlightQuery = errors.New(
		"invalid xiangwan past-highlight query",
	)
	ErrPastHighlightAnchorNotFound = resource.ErrPastHighlightAnchorUnavailable
	ErrPastHighlightFactsConflict  = errors.New(
		"xiangwan past-highlight facts conflict",
	)
)

const pastHighlightContextSelect = `
WITH anchor AS (
    SELECT
        activity_instance.series_id,
        activity_instance.id,
        activity_instance.published_at
    FROM xiangwan_activity_instances AS activity_instance
    JOIN xiangwan_activity_series AS activity_series
      ON activity_series.tenant_id = activity_instance.tenant_id
     AND activity_series.id = activity_instance.series_id
    WHERE activity_instance.tenant_id = $1
      AND activity_instance.id = $2
      AND activity_instance.status IN ('published', 'completed', 'archived')
      AND activity_instance.published_at IS NOT NULL
      AND activity_series.status IN ('active', 'archived')
      AND activity_series.is_recurring
),
public_contexts AS (
    SELECT
        relation.instance_id,
        relation.session_id,
        relation.relation_kind,
        relation.expected_target_version
    FROM xiangwan_resource_publications AS publication
    JOIN xiangwan_resource_relations AS relation
      ON relation.tenant_id = publication.tenant_id
     AND relation.id = publication.relation_id
 AND NOT EXISTS (SELECT 1 FROM xiangwan_review_resource_replacements AS replacement
                 WHERE replacement.tenant_id = relation.tenant_id AND replacement.previous_relation_id = relation.id)
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
    WHERE relation.tenant_id = $1
      AND relation.access_policy = 'public'
      AND publication.access_policy = 'public'
      AND NOT EXISTS (
          SELECT 1 FROM content_governance AS governance
          WHERE governance.content_id = relation.content_id
      )
      AND EXISTS (
          SELECT 1
          FROM blocks AS content_block
          LEFT JOIN LATERAL (
              SELECT photo_curation.ordered_block_ids
              FROM xiangwan_review_photo_curations AS photo_curation
              WHERE photo_curation.tenant_id = relation.tenant_id
                AND photo_curation.relation_id = relation.id
              ORDER BY photo_curation.version DESC
              LIMIT 1
          ) AS curation ON TRUE
          WHERE content_block.content_id = relation.content_id
            AND content_block.type IN ('text', 'image', 'link', 'file', 'video', 'audio')
            AND (content_block.type <> 'image'
                 OR curation.ordered_block_ids IS NULL
                 OR curation.ordered_block_ids ? content_block.id::TEXT)
      )
),
history AS (
    SELECT
        candidate.id,
        candidate.series_id,
        candidate.status,
        candidate.published_at,
        candidate.completed_at,
        EXISTS (
            SELECT 1
            FROM public_contexts AS public_context
            WHERE public_context.instance_id = candidate.id
              AND public_context.session_id IS NULL
              AND public_context.relation_kind = 'instance_review'
              AND public_context.expected_target_version = candidate.version
        ) AS has_instance_public_content,
        EXISTS (
            SELECT 1
            FROM xiangwan_activity_sessions AS candidate_session
            JOIN public_contexts AS public_context
              ON public_context.instance_id = candidate.id
             AND public_context.session_id = candidate_session.id
             AND public_context.relation_kind = 'session_resources'
             AND public_context.expected_target_version = candidate_session.version
            WHERE candidate_session.tenant_id = $1
              AND candidate_session.instance_id = candidate.id
              AND candidate_session.status IN ('ended', 'archived')
              AND candidate_session.delivery_mode IN ('offline', 'online')
              AND candidate_session.session_start_at IS NOT NULL
              AND candidate_session.confirmed_registration_count >= 0
              AND candidate_session.sort_order >= 0
        ) AS has_session_public_content
    FROM anchor
    JOIN xiangwan_activity_instances AS candidate
      ON candidate.tenant_id = $1
     AND candidate.series_id = anchor.series_id
     AND candidate.id <> anchor.id
    WHERE candidate.status IN ('completed', 'archived')
      AND candidate.published_at IS NOT NULL
      AND candidate.completed_at IS NOT NULL
      AND candidate.published_at <= candidate.completed_at
      AND candidate.published_at < anchor.published_at
),
selected_history AS (
    SELECT *
    FROM history
    WHERE has_instance_public_content OR has_session_public_content
    ORDER BY published_at DESC, completed_at DESC, id ASC
    LIMIT 1
),
eligible_sessions AS (
    SELECT
        candidate_session.id,
        candidate_session.delivery_mode,
        candidate_session.confirmed_registration_count,
        candidate_session.session_start_at,
        candidate_session.sort_order
    FROM selected_history AS selected
    JOIN xiangwan_activity_sessions AS candidate_session
      ON candidate_session.tenant_id = $1
     AND candidate_session.instance_id = selected.id
    WHERE candidate_session.status IN ('ended', 'archived')
      AND candidate_session.delivery_mode IN ('offline', 'online')
      AND candidate_session.session_start_at IS NOT NULL
      AND candidate_session.confirmed_registration_count >= 0
      AND candidate_session.sort_order >= 0
      AND EXISTS (
          SELECT 1
          FROM public_contexts AS public_context
          WHERE public_context.instance_id = selected.id
            AND public_context.session_id = candidate_session.id
            AND public_context.relation_kind = 'session_resources'
            AND public_context.expected_target_version = candidate_session.version
      )
    ORDER BY
        (candidate_session.delivery_mode = 'offline') DESC,
        candidate_session.confirmed_registration_count DESC,
        candidate_session.session_start_at ASC,
        candidate_session.sort_order ASC,
        candidate_session.id ASC
    LIMIT 201
)
SELECT
    anchor.series_id,
    anchor.id,
    anchor.published_at,
    selected.id,
    selected.series_id,
    selected.status,
    selected.published_at,
    selected.completed_at,
    selected.has_instance_public_content,
    eligible_session.id,
    eligible_session.delivery_mode,
    eligible_session.confirmed_registration_count,
    eligible_session.session_start_at,
    eligible_session.sort_order
FROM anchor
LEFT JOIN selected_history AS selected ON TRUE
LEFT JOIN eligible_sessions AS eligible_session ON TRUE
ORDER BY
    (eligible_session.delivery_mode = 'offline') DESC,
    eligible_session.confirmed_registration_count DESC,
    eligible_session.session_start_at ASC,
    eligible_session.sort_order ASC,
    eligible_session.id ASC
`

func (repository *Repository) ReadPastHighlightContext(
	ctx context.Context,
	tenantID uuid.UUID,
	anchorInstanceID uuid.UUID,
) (*resource.PastHighlightContext, error) {
	if repository == nil || repository.db == nil || tenantID == uuid.Nil ||
		anchorInstanceID == uuid.Nil {
		return nil, ErrInvalidPastHighlightQuery
	}
	rows, err := repository.db.queryContext(
		ctx,
		pastHighlightContextSelect,
		tenantID,
		anchorInstanceID,
	)
	if err != nil {
		return nil, fmt.Errorf("read xiangwan past-highlight context: %w", err)
	}
	return scanPastHighlightContext(rows)
}

func scanPastHighlightContext(
	rows resourceRowsScanner,
) (*resource.PastHighlightContext, error) {
	if rows == nil {
		return nil, ErrPastHighlightFactsConflict
	}
	defer func() { _ = rows.Close() }()

	var anchor resource.PastHighlightAnchor
	var candidate *resource.PastHighlightInstanceCandidate
	rowCount := 0
	for rows.Next() {
		rowCount++
		var rowAnchor resource.PastHighlightAnchor
		var candidateID uuid.NullUUID
		var candidateSeriesID uuid.NullUUID
		var candidateStatus sql.NullString
		var candidatePublishedAt sql.NullTime
		var candidateCompletedAt sql.NullTime
		var hasInstancePublicContent sql.NullBool
		var sessionID uuid.NullUUID
		var deliveryMode sql.NullString
		var confirmedRegistrationCount sql.NullInt64
		var sessionStartAt sql.NullTime
		var sortOrder sql.NullInt64
		if err := rows.Scan(
			&rowAnchor.SeriesID,
			&rowAnchor.InstanceID,
			&rowAnchor.PublishedAt,
			&candidateID,
			&candidateSeriesID,
			&candidateStatus,
			&candidatePublishedAt,
			&candidateCompletedAt,
			&hasInstancePublicContent,
			&sessionID,
			&deliveryMode,
			&confirmedRegistrationCount,
			&sessionStartAt,
			&sortOrder,
		); err != nil {
			return nil, fmt.Errorf("scan xiangwan past-highlight context: %w", err)
		}
		rowAnchor.PublishedAt = rowAnchor.PublishedAt.UTC()
		if rowCount == 1 {
			anchor = rowAnchor
		} else if rowAnchor != anchor {
			return nil, ErrPastHighlightFactsConflict
		}

		if !candidateID.Valid {
			if candidateSeriesID.Valid || candidateStatus.Valid ||
				candidatePublishedAt.Valid || candidateCompletedAt.Valid ||
				hasInstancePublicContent.Valid || sessionID.Valid ||
				deliveryMode.Valid || confirmedRegistrationCount.Valid ||
				sessionStartAt.Valid || sortOrder.Valid || rowCount != 1 {
				return nil, ErrPastHighlightFactsConflict
			}
			continue
		}
		if !candidateSeriesID.Valid || !candidateStatus.Valid ||
			!candidatePublishedAt.Valid || !candidateCompletedAt.Valid ||
			!hasInstancePublicContent.Valid {
			return nil, ErrPastHighlightFactsConflict
		}
		if candidate == nil {
			candidate = &resource.PastHighlightInstanceCandidate{
				InstanceID:               candidateID.UUID,
				SeriesID:                 candidateSeriesID.UUID,
				Status:                   activity.InstanceStatus(candidateStatus.String),
				PublishedAt:              candidatePublishedAt.Time.UTC(),
				CompletedAt:              candidateCompletedAt.Time.UTC(),
				HasInstancePublicContent: hasInstancePublicContent.Bool,
			}
		} else if candidate.InstanceID != candidateID.UUID ||
			candidate.SeriesID != candidateSeriesID.UUID ||
			candidate.Status != activity.InstanceStatus(candidateStatus.String) ||
			!candidate.PublishedAt.Equal(candidatePublishedAt.Time) ||
			!candidate.CompletedAt.Equal(candidateCompletedAt.Time) ||
			candidate.HasInstancePublicContent != hasInstancePublicContent.Bool {
			return nil, ErrPastHighlightFactsConflict
		}
		if !sessionID.Valid {
			if deliveryMode.Valid || confirmedRegistrationCount.Valid ||
				sessionStartAt.Valid || sortOrder.Valid || rowCount != 1 {
				return nil, ErrPastHighlightFactsConflict
			}
			continue
		}
		if !deliveryMode.Valid || !confirmedRegistrationCount.Valid ||
			!sessionStartAt.Valid || !sortOrder.Valid ||
			confirmedRegistrationCount.Int64 > int64(^uint(0)>>1) ||
			sortOrder.Int64 > int64(^uint(0)>>1) {
			return nil, ErrPastHighlightFactsConflict
		}
		candidate.Sessions = append(
			candidate.Sessions,
			resource.PastHighlightSessionCandidate{
				SessionID:                  sessionID.UUID,
				DeliveryMode:               activity.DeliveryMode(deliveryMode.String),
				ConfirmedRegistrationCount: int(confirmedRegistrationCount.Int64),
				SessionStartAt:             sessionStartAt.Time.UTC(),
				SortOrder:                  int(sortOrder.Int64),
				HasPublicContent:           true,
			},
		)
		if len(candidate.Sessions) > maxPastHighlightSessionCandidates {
			return nil, ErrPastHighlightFactsConflict
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan past-highlight context: %w", err)
	}
	if rowCount == 0 {
		return nil, ErrPastHighlightAnchorNotFound
	}
	candidates := make([]resource.PastHighlightInstanceCandidate, 0, 1)
	if candidate != nil {
		candidates = append(candidates, *candidate)
	}
	result, err := resource.SelectPastHighlightContext(anchor, candidates)
	if err != nil {
		return nil, ErrPastHighlightFactsConflict
	}
	return result, nil
}
