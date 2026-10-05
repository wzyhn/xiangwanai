package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

const maxNextInstanceRouteSessions = 200

var (
	ErrInvalidNextInstanceRouteQuery = errors.New(
		"invalid xiangwan next-Instance route query",
	)
	ErrNextInstanceRouteSourceNotFound = errors.New(
		"xiangwan next-Instance route source not found",
	)
	ErrNextInstanceRouteFactsConflict = errors.New(
		"xiangwan next-Instance route facts conflict",
	)
)

const nextInstanceRouteSelect = `
WITH source AS (
    SELECT
        source_instance.series_id,
        source_instance.id,
        source_instance.published_at,
        activity_series.status AS series_status,
        activity_series.is_recurring
    FROM xiangwan_activity_instances AS source_instance
    JOIN xiangwan_activity_series AS activity_series
      ON activity_series.tenant_id = source_instance.tenant_id
     AND activity_series.id = source_instance.series_id
    WHERE source_instance.tenant_id = $1
      AND source_instance.id = $2
      AND source_instance.status IN ('completed', 'archived')
      AND source_instance.published_at IS NOT NULL
      AND activity_series.status IN ('active', 'archived')
),
target AS (
    SELECT
        candidate_instance.series_id,
        candidate_instance.id,
        candidate_instance.status,
        candidate_instance.published_at
    FROM source
    JOIN xiangwan_activity_instances AS candidate_instance
      ON candidate_instance.tenant_id = $1
     AND candidate_instance.series_id = source.series_id
     AND candidate_instance.id <> source.id
    WHERE candidate_instance.status = 'published'
      AND source.series_status = 'active'
      AND source.is_recurring
      AND candidate_instance.published_at IS NOT NULL
      AND candidate_instance.published_at > source.published_at
    ORDER BY candidate_instance.published_at ASC, candidate_instance.id ASC
    LIMIT 1
)
SELECT
    source.series_id,
    source.id,
    source.published_at,
    target.series_id,
    target.id,
    target.status,
    target.published_at,
    target_session.id,
    target_session.status,
    target_session.session_start_at,
    target_session.sort_order
FROM source
LEFT JOIN target ON TRUE
LEFT JOIN LATERAL (
    SELECT
        activity_session.id,
        activity_session.status,
        activity_session.session_start_at,
        activity_session.sort_order
    FROM xiangwan_activity_sessions AS activity_session
    WHERE activity_session.tenant_id = $1
      AND activity_session.instance_id = target.id
      AND activity_session.status = 'published'
      AND activity_session.session_start_at IS NOT NULL
      AND activity_session.published_at IS NOT NULL
      AND activity_session.sort_order >= 0
    ORDER BY
        activity_session.session_start_at ASC,
        activity_session.sort_order ASC,
        activity_session.id ASC
    LIMIT 201
) AS target_session ON TRUE
ORDER BY
    target_session.session_start_at ASC,
    target_session.sort_order ASC,
    target_session.id ASC
`

func (repository *Repository) ResolveNextInstanceSessionRoute(
	ctx context.Context,
	tenantID uuid.UUID,
	sourceInstanceID uuid.UUID,
) (activity.NextInstanceRouteResolution, error) {
	if repository == nil || repository.db == nil || tenantID == uuid.Nil ||
		sourceInstanceID == uuid.Nil {
		return activity.NextInstanceRouteResolution{},
			ErrInvalidNextInstanceRouteQuery
	}
	rows, err := repository.db.queryContext(
		ctx,
		nextInstanceRouteSelect,
		tenantID,
		sourceInstanceID,
	)
	if err != nil {
		return activity.NextInstanceRouteResolution{}, fmt.Errorf(
			"read xiangwan next-Instance route: %w",
			err,
		)
	}
	return scanNextInstanceRoute(rows)
}

func scanNextInstanceRoute(
	rows rowsScanner,
) (activity.NextInstanceRouteResolution, error) {
	if rows == nil {
		return activity.NextInstanceRouteResolution{},
			ErrNextInstanceRouteFactsConflict
	}
	defer func() { _ = rows.Close() }()

	var anchor activity.NextInstanceRouteAnchor
	var candidate *activity.NextInstanceRouteCandidate
	rowCount := 0
	for rows.Next() {
		rowCount++
		var rowAnchor activity.NextInstanceRouteAnchor
		var targetSeriesID uuid.NullUUID
		var targetInstanceID uuid.NullUUID
		var targetStatus sql.NullString
		var targetPublishedAt sql.NullTime
		var sessionID uuid.NullUUID
		var sessionStatus sql.NullString
		var sessionStartAt sql.NullTime
		var sortOrder sql.NullInt64
		if err := rows.Scan(
			&rowAnchor.SeriesID,
			&rowAnchor.InstanceID,
			&rowAnchor.PublishedAt,
			&targetSeriesID,
			&targetInstanceID,
			&targetStatus,
			&targetPublishedAt,
			&sessionID,
			&sessionStatus,
			&sessionStartAt,
			&sortOrder,
		); err != nil {
			return activity.NextInstanceRouteResolution{}, fmt.Errorf(
				"scan xiangwan next-Instance route: %w",
				err,
			)
		}
		rowAnchor.PublishedAt = rowAnchor.PublishedAt.UTC()
		if rowCount == 1 {
			anchor = rowAnchor
		} else if rowAnchor != anchor {
			return activity.NextInstanceRouteResolution{},
				ErrNextInstanceRouteFactsConflict
		}

		if !targetInstanceID.Valid {
			if targetSeriesID.Valid || targetStatus.Valid ||
				targetPublishedAt.Valid || sessionID.Valid ||
				sessionStatus.Valid || sessionStartAt.Valid ||
				sortOrder.Valid || rowCount != 1 {
				return activity.NextInstanceRouteResolution{},
					ErrNextInstanceRouteFactsConflict
			}
			continue
		}
		if !targetSeriesID.Valid || !targetStatus.Valid ||
			!targetPublishedAt.Valid {
			return activity.NextInstanceRouteResolution{},
				ErrNextInstanceRouteFactsConflict
		}
		if candidate == nil {
			candidate = &activity.NextInstanceRouteCandidate{
				SeriesID:    targetSeriesID.UUID,
				InstanceID:  targetInstanceID.UUID,
				Status:      activity.InstanceStatus(targetStatus.String),
				PublishedAt: targetPublishedAt.Time.UTC(),
			}
		} else if candidate.SeriesID != targetSeriesID.UUID ||
			candidate.InstanceID != targetInstanceID.UUID ||
			candidate.Status != activity.InstanceStatus(targetStatus.String) ||
			!candidate.PublishedAt.Equal(targetPublishedAt.Time) {
			return activity.NextInstanceRouteResolution{},
				ErrNextInstanceRouteFactsConflict
		}
		if !sessionID.Valid {
			if sessionStatus.Valid || sessionStartAt.Valid || sortOrder.Valid ||
				rowCount != 1 {
				return activity.NextInstanceRouteResolution{},
					ErrNextInstanceRouteFactsConflict
			}
			continue
		}
		if !sessionStatus.Valid || !sessionStartAt.Valid || !sortOrder.Valid {
			return activity.NextInstanceRouteResolution{},
				ErrNextInstanceRouteFactsConflict
		}
		candidate.Sessions = append(candidate.Sessions, activity.SessionRouteCandidate{
			SeriesID:       candidate.SeriesID,
			InstanceID:     candidate.InstanceID,
			SessionID:      sessionID.UUID,
			SessionStatus:  activity.SessionStatus(sessionStatus.String),
			SessionStartAt: sessionStartAt.Time.UTC(),
			SortOrder:      int(sortOrder.Int64),
		})
		if len(candidate.Sessions) > maxNextInstanceRouteSessions {
			return activity.NextInstanceRouteResolution{},
				ErrNextInstanceRouteFactsConflict
		}
	}
	if err := rows.Err(); err != nil {
		return activity.NextInstanceRouteResolution{}, fmt.Errorf(
			"iterate xiangwan next-Instance route: %w",
			err,
		)
	}
	if rowCount == 0 {
		return activity.NextInstanceRouteResolution{},
			ErrNextInstanceRouteSourceNotFound
	}
	candidates := make([]activity.NextInstanceRouteCandidate, 0, 1)
	if candidate != nil {
		candidates = append(candidates, *candidate)
	}
	resolution, err := activity.ResolveNextInstanceRoute(anchor, candidates)
	if err != nil {
		return activity.NextInstanceRouteResolution{},
			ErrNextInstanceRouteFactsConflict
	}
	return resolution, nil
}
