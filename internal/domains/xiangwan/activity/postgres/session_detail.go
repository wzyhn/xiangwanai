package activitypostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

var ErrSessionDetailNotFound = errors.New("xiangwan Session detail not found")

// ReadSessionDetail resolves one public Session directly by tenant and Session
// identity. It never substitutes another Session from the same Instance or
// Series when the requested Session is unavailable.
func (repository *Repository) ReadSessionDetail(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
	now time.Time,
) (activity.SessionDetail, error) {
	facts, err := repository.GetSessionDetailFacts(ctx, tenantID, sessionID)
	if err != nil {
		return activity.SessionDetail{}, err
	}
	detail, err := activity.BuildSessionDetail(facts, now)
	if err != nil {
		return activity.SessionDetail{}, fmt.Errorf("build xiangwan Session detail: %w", err)
	}
	return detail, nil
}

func (repository *Repository) GetSessionDetailFacts(
	ctx context.Context,
	tenantID uuid.UUID,
	sessionID uuid.UUID,
) (activity.SessionDetailFacts, error) {
	facts, err := scanSessionDetailFacts(repository.db.queryRowContext(ctx, `
SELECT
    activity_series.id,
    activity_series.status,
    activity_series.is_recurring,
    COALESCE(activity_series.current_public_instance_id = activity_instance.id, FALSE),
    activity_series.successful_published_instance_count,
    activity_series.historical_registration_count,
    activity_instance.id,
    activity_instance.title,
    activity_instance.status,
    activity_instance.activity_type,
    TO_JSON(activity_instance.quick_tag_codes),
    activity_instance.cover_image_url,
    activity_instance.detail_blocks,
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
    activity_session.address,
    activity_session.longitude,
    activity_session.latitude,
    activity_session.online_participation_mode,
    activity_session.online_participation_compliant
FROM xiangwan_activity_sessions AS activity_session
JOIN xiangwan_activity_instances AS activity_instance
  ON activity_instance.tenant_id = activity_session.tenant_id
 AND activity_instance.id = activity_session.instance_id
JOIN xiangwan_activity_series AS activity_series
  ON activity_series.tenant_id = activity_instance.tenant_id
 AND activity_series.id = activity_instance.series_id
WHERE activity_session.tenant_id = $1
  AND activity_session.id = $2
  AND activity_series.status = 'active'
  AND activity_instance.status IN ('published', 'completed', 'cancelled')
  AND activity_session.status IN ('published', 'ended', 'cancelled')
`, tenantID, sessionID))
	if errors.Is(err, sql.ErrNoRows) {
		return activity.SessionDetailFacts{}, errors.Join(
			ErrSessionDetailNotFound,
			activity.ErrSessionDetailUnavailable,
		)
	}
	if err != nil {
		return activity.SessionDetailFacts{}, fmt.Errorf("get xiangwan Session detail facts: %w", err)
	}
	return facts, nil
}

func scanSessionDetailFacts(row rowScanner) (activity.SessionDetailFacts, error) {
	var facts activity.SessionDetailFacts
	var activityType sql.NullString
	var quickTagCodes StringArrayJSON
	var detailBlocks []byte
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
	var address sql.NullString
	var longitude sql.NullFloat64
	var latitude sql.NullFloat64
	var onlineParticipationMode sql.NullString
	var onlineParticipationCompliant sql.NullBool

	err := row.Scan(
		&facts.SeriesID,
		&facts.SeriesStatus,
		&facts.SeriesIsRecurring,
		&facts.CurrentPublicInstance,
		&facts.SuccessfulPublishedInstanceCount,
		&facts.HistoricalRegistrationCount,
		&facts.InstanceID,
		&facts.InstanceTitle,
		&facts.InstanceStatus,
		&activityType,
		&quickTagCodes,
		&facts.InstanceCoverImageURL,
		&detailBlocks,
		&facts.PublicationVersion,
		&facts.SessionID,
		&facts.SessionTitle,
		&facts.SessionStatus,
		&registrationStartAt,
		&registrationEndAt,
		&sessionStartAt,
		&sessionEndAt,
		&capacity,
		&facts.ConfirmedCount,
		&facts.ActiveHoldCount,
		&groupMinimum,
		&lowStockThreshold,
		&priceCents,
		&deliveryMode,
		&area,
		&venueName,
		&address,
		&longitude,
		&latitude,
		&onlineParticipationMode,
		&onlineParticipationCompliant,
	)
	if err != nil {
		return activity.SessionDetailFacts{}, err
	}

	facts.InstanceActivityType = activity.ActivityType(activityType.String)
	facts.InstanceQuickTagCodes = append([]string(nil), quickTagCodes...)
	blocks, err := activity.ProjectDetailBlocks(detailBlocks)
	if err != nil {
		return activity.SessionDetailFacts{}, fmt.Errorf(
			"decode xiangwan Instance detail blocks: %w", err,
		)
	}
	facts.InstanceDetailBlocks = blocks
	facts.RegistrationStartAt = registrationStartAt.Time
	facts.RegistrationEndAt = registrationEndAt.Time
	facts.SessionStartAt = sessionStartAt.Time
	facts.SessionEndAt = sessionEndAt.Time
	facts.Capacity = int(capacity.Int64)
	facts.GroupMinimum = int(groupMinimum.Int64)
	facts.LowStockThreshold = nullIntPointer(lowStockThreshold)
	facts.PriceCents = priceCents.Int64
	facts.DeliveryMode = activity.DeliveryMode(deliveryMode.String)
	facts.Area = activity.AreaCode(area.String)
	facts.VenueName = venueName.String
	facts.Address = address.String
	facts.Longitude = nullFloat64Pointer(longitude)
	facts.Latitude = nullFloat64Pointer(latitude)
	facts.OnlineParticipationMode = onlineParticipationMode.String
	facts.OnlineParticipationCompliant = onlineParticipationCompliant.Bool
	return facts, nil
}
