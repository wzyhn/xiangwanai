package activitypostgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

// ReadHomeCatalog is the PostgreSQL-backed application entry point. Database
// rows remain subject to the same fail-closed domain validation and canonical
// ordering as facts supplied by any future adapter.
func (repository *Repository) ReadHomeCatalog(
	ctx context.Context,
	tenantID uuid.UUID,
	filter activity.HomeFilter,
	availableQuickTags []activity.HomeQuickTag,
	now time.Time,
) (activity.HomeCatalog, error) {
	facts, err := repository.ListHomeSessionFacts(ctx, tenantID)
	if err != nil {
		return activity.HomeCatalog{}, err
	}
	catalog, err := activity.BuildHomeCatalog(facts, filter, availableQuickTags, now)
	if err != nil {
		return activity.HomeCatalog{}, fmt.Errorf("build xiangwan home catalog: %w", err)
	}
	return catalog, nil
}

// ListHomeSessionFacts loads the current public Instance for each visible
// Series. Every join repeats tenant_id so a malformed cross-tenant reference
// cannot leak a Session into another tenant's catalog.
func (repository *Repository) ListHomeSessionFacts(
	ctx context.Context,
	tenantID uuid.UUID,
) ([]activity.HomeSessionFacts, error) {
	rows, err := repository.db.queryContext(ctx, `
SELECT
    activity_series.id,
    activity_series.status,
    activity_series.is_recurring,
    activity_series.home_visible,
    activity_series.current_public_instance_id = activity_instance.id,
    activity_instance.id,
    activity_instance.title,
    activity_instance.status,
    activity_instance.activity_type,
    TO_JSON(activity_instance.quick_tag_codes),
    activity_instance.cover_image_url,
    activity_instance.publication_version,
    activity_session.id,
    activity_session.title,
    activity_session.status,
    activity_session.registration_start_at,
    activity_session.registration_end_at,
    activity_session.session_start_at,
    activity_session.session_end_at,
    activity_session.capacity,
    activity_session.confirmed_registration_count,
    activity_session.active_hold_count,
    activity_session.group_minimum,
    activity_session.low_stock_threshold,
    activity_session.price_cents,
    activity_session.delivery_mode,
    activity_session.area_code,
    activity_session.venue_name,
    activity_session.online_participation_mode,
    activity_session.sort_order,
    activity_series.favorite_count,
    activity_series.historical_registration_count
FROM xiangwan_activity_series AS activity_series
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = activity_series.tenant_id
 AND activity_instance.series_id = activity_series.id
 AND activity_instance.id = activity_series.current_public_instance_id
JOIN xiangwan_activity_sessions AS activity_session
  ON activity_session.tenant_id = activity_instance.tenant_id
 AND activity_session.instance_id = activity_instance.id
WHERE activity_series.tenant_id = $1
  AND activity_series.status = 'active'
  AND activity_series.home_visible
  AND activity_instance.status IN ('published', 'completed', 'cancelled')
  AND activity_session.status IN ('published', 'ended', 'cancelled')
ORDER BY activity_series.id ASC, activity_instance.id ASC, activity_session.id ASC
`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan home Session facts: %w", err)
	}
	defer rows.Close()

	facts := make([]activity.HomeSessionFacts, 0)
	for rows.Next() {
		fact, scanErr := scanHomeSessionFacts(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan xiangwan home Session facts: %w", scanErr)
		}
		facts = append(facts, fact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan home Session facts: %w", err)
	}
	return facts, nil
}

func scanHomeSessionFacts(row rowScanner) (activity.HomeSessionFacts, error) {
	var fact activity.HomeSessionFacts
	var activityType sql.NullString
	var quickTagCodes StringArrayJSON
	var registrationStartAt sql.NullTime
	var registrationEndAt sql.NullTime
	var sessionStartAt sql.NullTime
	var sessionEndAt sql.NullTime
	var capacity sql.NullInt64
	var groupMinimum sql.NullInt64
	var lowStockThreshold sql.NullInt64
	var priceCents sql.NullInt64
	var deliveryMode sql.NullString
	var area sql.NullString
	var venueName sql.NullString
	var onlineParticipationMode sql.NullString

	err := row.Scan(
		&fact.SeriesID,
		&fact.SeriesStatus,
		&fact.SeriesIsRecurring,
		&fact.SeriesHomeVisible,
		&fact.CurrentPublicInstance,
		&fact.InstanceID,
		&fact.InstanceTitle,
		&fact.InstanceStatus,
		&activityType,
		&quickTagCodes,
		&fact.InstanceCoverImageURL,
		&fact.PublicationVersion,
		&fact.SessionID,
		&fact.SessionTitle,
		&fact.SessionStatus,
		&registrationStartAt,
		&registrationEndAt,
		&sessionStartAt,
		&sessionEndAt,
		&capacity,
		&fact.ConfirmedCount,
		&fact.ActiveHoldCount,
		&groupMinimum,
		&lowStockThreshold,
		&priceCents,
		&deliveryMode,
		&area,
		&venueName,
		&onlineParticipationMode,
		&fact.SortOrder,
		&fact.SeriesFavoriteCount,
		&fact.HistoricalRegistrationCount,
	)
	if err != nil {
		return activity.HomeSessionFacts{}, err
	}

	fact.InstanceActivityType = activity.ActivityType(activityType.String)
	fact.InstanceQuickTagCodes = append([]string(nil), quickTagCodes...)
	fact.RegistrationStartAt = registrationStartAt.Time
	fact.RegistrationEndAt = registrationEndAt.Time
	fact.SessionStartAt = sessionStartAt.Time
	fact.SessionEndAt = sessionEndAt.Time
	fact.Capacity = int(capacity.Int64)
	fact.GroupMinimum = int(groupMinimum.Int64)
	fact.LowStockThreshold = nullIntPointer(lowStockThreshold)
	fact.PriceCents = priceCents.Int64
	fact.DeliveryMode = activity.DeliveryMode(deliveryMode.String)
	fact.Area = activity.AreaCode(area.String)
	fact.VenueName = venueName.String
	fact.OnlineParticipationMode = onlineParticipationMode.String
	return fact, nil
}
