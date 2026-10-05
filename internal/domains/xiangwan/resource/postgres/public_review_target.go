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

const publicReviewTargetSelect = `
SELECT
    activity_instance.series_id,
    activity_instance.id,
    target_session.id
FROM xiangwan_activity_instances AS activity_instance
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_instance.tenant_id
 AND activity_series.id = activity_instance.series_id
LEFT JOIN xiangwan_activity_sessions AS target_session
  ON target_session.tenant_id = activity_instance.tenant_id
 AND target_session.instance_id = activity_instance.id
 AND target_session.id = $3
 AND target_session.status IN ('ended', 'archived')
WHERE activity_instance.tenant_id = $1
  AND activity_instance.id = $2
  AND activity_instance.status IN ('completed', 'archived')
  AND activity_instance.completed_at IS NOT NULL
  AND activity_instance.completed_at <= $4
  AND activity_series.status IN ('active', 'archived')
  AND ($3::uuid IS NULL OR target_session.id IS NOT NULL)
LIMIT 1
`

func (repository *Repository) ResolvePublicReviewTarget(
	ctx context.Context,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
	sessionID *uuid.UUID,
	at time.Time,
) (resource.PastHighlightReviewTarget, error) {
	if repository == nil || repository.db == nil || ctx == nil ||
		tenantID == uuid.Nil || instanceID == uuid.Nil || at.IsZero() ||
		(sessionID != nil && *sessionID == uuid.Nil) {
		return resource.PastHighlightReviewTarget{},
			ErrInvalidPublicReviewQuery
	}
	var sessionArgument any
	if sessionID != nil {
		sessionArgument = *sessionID
	}
	var target resource.PastHighlightReviewTarget
	var resolvedSessionID uuid.NullUUID
	err := repository.db.queryRowContext(
		ctx,
		publicReviewTargetSelect,
		tenantID,
		instanceID,
		sessionArgument,
		at.UTC(),
	).Scan(
		&target.SeriesID,
		&target.InstanceID,
		&resolvedSessionID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return resource.PastHighlightReviewTarget{},
			ErrPublicReviewTargetNotFound
	}
	if err != nil {
		return resource.PastHighlightReviewTarget{}, fmt.Errorf(
			"resolve xiangwan public review target: %w",
			err,
		)
	}
	if resolvedSessionID.Valid {
		target.SessionID = &resolvedSessionID.UUID
	}
	if target.InstanceID != instanceID ||
		!sameOptionalPublicReviewSession(target.SessionID, sessionID) {
		return resource.PastHighlightReviewTarget{},
			ErrPublicReviewFactsConflict
	}
	policy, _ := resource.NewExternalDomainPolicy(nil)
	if _, err := resource.ProjectPublicReview(target, nil, policy); err != nil {
		return resource.PastHighlightReviewTarget{},
			ErrPublicReviewFactsConflict
	}
	return target, nil
}

func sameOptionalPublicReviewSession(left, right *uuid.UUID) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
