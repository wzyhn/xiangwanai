package activitypostgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func (repository *Repository) ResolveSeriesSessionRoute(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) (activity.SessionRouteResolution, error) {
	candidates, err := repository.ListSeriesSessionRouteCandidates(ctx, tenantID, seriesID)
	if err != nil {
		return activity.SessionRouteResolution{}, err
	}
	resolution, err := activity.ResolveSessionRoute(candidates)
	if err != nil {
		return activity.SessionRouteResolution{}, fmt.Errorf("resolve Xiangwan Series Session route: %w", err)
	}
	return resolution, nil
}

func (repository *Repository) ResolveInstanceSessionRoute(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) (activity.SessionRouteResolution, error) {
	candidates, err := repository.ListInstanceSessionRouteCandidates(ctx, tenantID, instanceID)
	if err != nil {
		return activity.SessionRouteResolution{}, err
	}
	resolution, err := activity.ResolveSessionRoute(candidates)
	if err != nil {
		return activity.SessionRouteResolution{}, fmt.Errorf("resolve Xiangwan Instance Session route: %w", err)
	}
	return resolution, nil
}

func (repository *Repository) ListSeriesSessionRouteCandidates(
	ctx context.Context,
	tenantID uuid.UUID,
	seriesID uuid.UUID,
) ([]activity.SessionRouteCandidate, error) {
	return repository.listSessionRouteCandidates(ctx, `
SELECT
    activity_series.id,
    activity_instance.id,
    activity_session.id,
    activity_session.title,
    activity_session.status,
    activity_session.session_start_at,
    activity_session.sort_order,
    FALSE AS review_only
FROM xiangwan_activity_series AS activity_series
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = activity_series.tenant_id
 AND activity_instance.series_id = activity_series.id
 AND activity_instance.id = activity_series.current_public_instance_id
JOIN xiangwan_activity_sessions AS activity_session
  ON activity_session.tenant_id = activity_instance.tenant_id
 AND activity_session.instance_id = activity_instance.id
WHERE activity_series.tenant_id = $1
  AND activity_series.id = $2
  AND activity_series.status = 'active'
  AND activity_instance.status IN ('published', 'completed', 'cancelled')
  AND activity_session.status IN ('published', 'ended', 'cancelled')
ORDER BY activity_session.session_start_at ASC, activity_session.sort_order ASC, activity_session.id ASC
LIMIT 201
`, tenantID, seriesID)
}

func (repository *Repository) ListInstanceSessionRouteCandidates(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) ([]activity.SessionRouteCandidate, error) {
	return repository.listSessionRouteCandidates(ctx, `
SELECT
    activity_series.id,
    activity_instance.id,
    activity_session.id,
    activity_session.title,
    activity_session.status,
    activity_session.session_start_at,
    activity_session.sort_order,
    NOT (
      activity_series.status = 'active'
      AND activity_instance.status IN ('published', 'completed', 'cancelled')
      AND activity_session.status IN ('published', 'ended', 'cancelled')
    ) AS review_only
FROM xiangwan_activity_instances AS activity_instance
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_instance.tenant_id
 AND activity_series.id = activity_instance.series_id
JOIN xiangwan_activity_sessions AS activity_session
  ON activity_session.tenant_id = activity_instance.tenant_id
 AND activity_session.instance_id = activity_instance.id
WHERE activity_instance.tenant_id = $1
  AND activity_instance.id = $2
  AND activity_series.status IN ('active', 'archived')
  AND activity_instance.status IN ('published', 'completed', 'cancelled', 'archived')
  AND activity_instance.published_at IS NOT NULL
  AND activity_session.status IN ('published', 'ended', 'cancelled', 'archived')
  AND activity_session.published_at IS NOT NULL
  AND (
    (
      activity_series.status = 'active'
      AND activity_instance.status IN ('published', 'completed', 'cancelled')
      AND activity_session.status IN ('published', 'ended', 'cancelled')
    )
    OR (
      activity_instance.status IN ('completed', 'archived')
      AND activity_session.status IN ('ended', 'archived')
    )
  )
ORDER BY activity_session.session_start_at ASC, activity_session.sort_order ASC, activity_session.id ASC
LIMIT 201
`, tenantID, instanceID)
}

func (repository *Repository) listSessionRouteCandidates(
	ctx context.Context,
	query string,
	tenantID uuid.UUID,
	targetID uuid.UUID,
) ([]activity.SessionRouteCandidate, error) {
	rows, err := repository.db.queryContext(ctx, query, tenantID, targetID)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan Session route candidates: %w", err)
	}
	defer rows.Close()

	candidates := make([]activity.SessionRouteCandidate, 0)
	for rows.Next() {
		var candidate activity.SessionRouteCandidate
		var sessionStartAt sql.NullTime
		if err := rows.Scan(
			&candidate.SeriesID,
			&candidate.InstanceID,
			&candidate.SessionID,
			&candidate.SessionTitle,
			&candidate.SessionStatus,
			&sessionStartAt,
			&candidate.SortOrder,
			&candidate.ReviewOnly,
		); err != nil {
			return nil, fmt.Errorf("scan xiangwan Session route candidate: %w", err)
		}
		candidate.SessionTitle = strings.TrimSpace(candidate.SessionTitle)
		candidate.SessionStartAt = sessionStartAt.Time
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan Session route candidates: %w", err)
	}
	return candidates, nil
}
