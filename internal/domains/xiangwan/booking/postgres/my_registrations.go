// Package bookingpostgres reads user-owned Xiangwan booking projections from
// the customer PostgreSQL database.
package bookingpostgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyRegistrationsFilter = booking.ErrInvalidMyRegistrationsFilter
	ErrInvalidMyRegistrationsCursor = booking.ErrInvalidMyRegistrationsCursor
	ErrStaleMyRegistrationsCursor   = booking.ErrStaleMyRegistrationsCursor
	ErrMyRegistrationProjection     = errors.New(
		"xiangwan My Registration projection mismatch",
	)
)

type DB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type Repository struct {
	db queryExecutor
}

func NewRepository(db DB) *Repository {
	return &Repository{db: sqlQueryExecutor{db: db}}
}

type rowScanner interface {
	Scan(...any) error
}

type rowsScanner interface {
	rowScanner
	Next() bool
	Err() error
	Close() error
}

type queryExecutor interface {
	queryContext(context.Context, string, ...any) (rowsScanner, error)
}

type sqlQueryExecutor struct {
	db DB
}

func (executor sqlQueryExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	return executor.db.QueryContext(ctx, query, args...)
}

const myRegistrationProjection = `
    registration_id,
    tenant_id,
    series_id,
    instance_id,
    session_id,
    principal_id,
    participation_status,
    registration_idempotency_key,
    confirmed_at,
    cancelled_at,
    cancellation_reason,
    registration_version,
    registration_created_at,
    registration_updated_at,
    series_title,
    instance_title,
    instance_status,
    instance_version,
    instance_created_at,
    instance_updated_at,
    session_title,
    session_status,
    session_start_at,
    session_end_at,
    delivery_mode,
    area_code,
    venue_name,
    address,
    online_participation_mode,
    session_version,
    session_created_at,
    session_updated_at,
    order_id,
    payment_status,
    original_price_cents,
    discount_cents,
    payable_cents,
    actual_paid_cents,
    paid_at,
    closed_at,
    order_version,
    order_created_at,
    order_updated_at,
    hold_id,
    hold_status,
    hold_expires_at,
    hold_version,
    hold_created_at,
    hold_updated_at,
    refund_case_id,
    refund_status,
    refund_reason_code,
    requested_refund_cents,
    successful_refund_cents,
    refund_resolved_at,
    refund_version,
    refund_created_at,
    refund_updated_at,
    checkin_status,
    checked_in_at,
    revoked_at,
    checkin_updated_at,
    view_state,
    sort_rank,
    last_business_at,
    sort_at
`

const myRegistrationsQueryPrefix = `
WITH facts AS (
    SELECT
        registration_record.id AS registration_id,
        registration_record.tenant_id,
        registration_record.series_id,
        registration_record.instance_id,
        registration_record.session_id,
        registration_record.principal_id,
        registration_record.participation_status,
        registration_record.idempotency_key AS registration_idempotency_key,
        registration_record.confirmed_at,
        registration_record.cancelled_at,
        registration_record.cancellation_reason,
        registration_record.version AS registration_version,
        registration_record.created_at AS registration_created_at,
        registration_record.updated_at AS registration_updated_at,
        activity_series.title AS series_title,
        activity_instance.title AS instance_title,
        activity_instance.status AS instance_status,
        activity_instance.version AS instance_version,
        activity_instance.created_at AS instance_created_at,
        activity_instance.updated_at AS instance_updated_at,
        activity_session.title AS session_title,
        activity_session.status AS session_status,
        activity_session.session_start_at,
        activity_session.session_end_at,
        activity_session.delivery_mode,
        activity_session.area_code,
        activity_session.venue_name,
        activity_session.address,
        activity_session.online_participation_mode,
        activity_session.version AS session_version,
        activity_session.created_at AS session_created_at,
        activity_session.updated_at AS session_updated_at,
        order_record.id AS order_id,
        order_record.payment_status,
        order_record.original_price_cents,
        order_record.discount_cents,
        order_record.payable_cents,
        order_record.actual_paid_cents,
        order_record.paid_at,
        order_record.closed_at,
        order_record.version AS order_version,
        order_record.created_at AS order_created_at,
        order_record.updated_at AS order_updated_at,
        capacity_hold.id AS hold_id,
        capacity_hold.hold_status,
        capacity_hold.expires_at AS hold_expires_at,
        capacity_hold.version AS hold_version,
        capacity_hold.created_at AS hold_created_at,
        capacity_hold.updated_at AS hold_updated_at,
        refund_case.id AS refund_case_id,
        refund_case.refund_status,
        refund_case.reason_code AS refund_reason_code,
        refund_case.requested_refund_cents,
        refund_case.successful_refund_cents,
        refund_case.resolved_at AS refund_resolved_at,
        refund_case.version AS refund_version,
        refund_case.created_at AS refund_created_at,
        refund_case.updated_at AS refund_updated_at,
        checkin_record.checkin_status,
        checkin_record.checked_in_at,
        checkin_record.revoked_at,
        checkin_record.updated_at AS checkin_updated_at,
        session_cancellation.id AS session_cancellation_receipt_id,
        session_cancellation.cancellation_reason
            AS session_cancellation_reason,
        session_cancellation.cancelled_at AS session_cancelled_at,
        instance_cancellation.id AS instance_cancellation_receipt_id,
        instance_cancellation.cancellation_reason
            AS instance_cancellation_reason,
        instance_cancellation.cancelled_at AS instance_cancelled_at,
        CASE
            WHEN refund_case.refund_status = 'refunded' THEN 'refunded'
            WHEN refund_case.refund_status IN (
                'pending_manual', 'processing', 'failed'
            ) THEN 'refund_processing'
            WHEN registration_record.participation_status = 'cancelled'
              OR activity_instance.status = 'cancelled'
              OR activity_session.status = 'cancelled' THEN 'cancelled'
            WHEN activity_instance.status IN ('completed', 'archived')
              OR activity_session.status IN ('ended', 'archived')
              OR activity_session.session_end_at <= $3 THEN 'ended'
            WHEN registration_record.participation_status = 'pending_payment'
                THEN 'pending_payment'
            ELSE 'registered'
        END AS view_state,
        GREATEST(
            registration_record.updated_at,
            activity_instance.updated_at,
            activity_session.updated_at,
            COALESCE(order_record.updated_at, '-infinity'::TIMESTAMPTZ),
            COALESCE(capacity_hold.updated_at, '-infinity'::TIMESTAMPTZ),
            COALESCE(refund_case.updated_at, '-infinity'::TIMESTAMPTZ),
            COALESCE(checkin_record.updated_at, '-infinity'::TIMESTAMPTZ)
        ) AS base_last_business_at
    FROM xiangwan_registrations AS registration_record
    JOIN xiangwan_activity_series AS activity_series
      ON activity_series.tenant_id = registration_record.tenant_id
     AND activity_series.id = registration_record.series_id
    JOIN xiangwan_activity_instances AS activity_instance
      ON activity_instance.tenant_id = registration_record.tenant_id
     AND activity_instance.series_id = registration_record.series_id
     AND activity_instance.id = registration_record.instance_id
    JOIN xiangwan_activity_sessions AS activity_session
      ON activity_session.tenant_id = registration_record.tenant_id
     AND activity_session.instance_id = registration_record.instance_id
     AND activity_session.id = registration_record.session_id
    LEFT JOIN xiangwan_orders AS order_record
      ON order_record.tenant_id = registration_record.tenant_id
     AND order_record.registration_id = registration_record.id
     AND order_record.series_id = registration_record.series_id
     AND order_record.instance_id = registration_record.instance_id
     AND order_record.session_id = registration_record.session_id
     AND order_record.principal_id = registration_record.principal_id
    LEFT JOIN xiangwan_capacity_holds AS capacity_hold
      ON capacity_hold.tenant_id = registration_record.tenant_id
     AND capacity_hold.order_id = order_record.id
     AND capacity_hold.registration_id = registration_record.id
     AND capacity_hold.session_id = registration_record.session_id
    LEFT JOIN xiangwan_refund_cases AS refund_case
      ON refund_case.tenant_id = registration_record.tenant_id
     AND refund_case.order_id = order_record.id
     AND refund_case.registration_id = registration_record.id
     AND refund_case.series_id = registration_record.series_id
     AND refund_case.instance_id = registration_record.instance_id
     AND refund_case.session_id = registration_record.session_id
     AND refund_case.principal_id = registration_record.principal_id
    LEFT JOIN xiangwan_checkins AS checkin_record
      ON checkin_record.tenant_id = registration_record.tenant_id
     AND checkin_record.registration_id = registration_record.id
     AND checkin_record.series_id = registration_record.series_id
     AND checkin_record.instance_id = registration_record.instance_id
     AND checkin_record.session_id = registration_record.session_id
     AND checkin_record.principal_id = registration_record.principal_id
    LEFT JOIN xiangwan_session_cancellation_receipts AS session_cancellation
      ON session_cancellation.tenant_id = registration_record.tenant_id
     AND session_cancellation.series_id = registration_record.series_id
     AND session_cancellation.instance_id = registration_record.instance_id
     AND session_cancellation.session_id = registration_record.session_id
    LEFT JOIN xiangwan_instance_cancellation_receipts AS instance_cancellation
      ON instance_cancellation.tenant_id = registration_record.tenant_id
     AND instance_cancellation.series_id = registration_record.series_id
     AND instance_cancellation.instance_id = registration_record.instance_id
    WHERE registration_record.tenant_id = $1
      AND registration_record.principal_id = $2
),
projected AS (
    SELECT
        facts.*,
        CASE view_state
            WHEN 'pending_payment' THEN 1
            WHEN 'registered' THEN 2
            WHEN 'refund_processing' THEN 3
            WHEN 'refunded' THEN 4
            WHEN 'cancelled' THEN 5
            WHEN 'ended' THEN 6
        END AS sort_rank,
        GREATEST(
            base_last_business_at,
            CASE
                WHEN view_state = 'ended' THEN session_end_at
                ELSE '-infinity'::TIMESTAMPTZ
            END
        ) AS last_business_at
    FROM facts
),
ranked AS (
    SELECT
        projected.*,
        CASE
            WHEN sort_rank IN (1, 2) THEN session_start_at
            ELSE last_business_at
        END AS sort_at,
        CASE
            WHEN order_id IS NULL THEN NULL
            WHEN refund_status = 'refunded' THEN 'refunded'
            WHEN refund_status IN (
                'pending_manual', 'processing', 'failed'
            ) THEN 'refund_processing'
            WHEN payment_status IN (
                'paid_confirmed', 'settled_zero'
            ) THEN 'paid'
            WHEN payment_status = 'closed_unpaid' THEN 'closed'
            ELSE 'pending_payment'
        END AS order_view_state,
        CASE
            WHEN order_id IS NULL THEN NULL
            WHEN refund_status IN (
                'pending_manual', 'processing', 'failed'
            ) THEN 2
            WHEN payment_status IN ('paid_confirmed', 'settled_zero')
             AND refund_status = 'refunded' THEN 4
            WHEN payment_status IN (
                'paid_confirmed', 'settled_zero'
            ) THEN 3
            WHEN payment_status = 'closed_unpaid' THEN 5
            ELSE 1
        END AS order_sort_rank,
        CASE
            WHEN order_id IS NULL THEN NULL
            ELSE GREATEST(
                order_updated_at,
                COALESCE(hold_updated_at, '-infinity'::TIMESTAMPTZ),
                COALESCE(refund_updated_at, '-infinity'::TIMESTAMPTZ)
            )
        END AS order_sort_at
    FROM projected
)
SELECT`

const myRegistrationsQueryFilter = `
FROM ranked
WHERE ($4 = 'all' OR view_state = $4)
`

const myRegistrationsOrder = `
ORDER BY
    sort_rank ASC,
    CASE WHEN sort_rank IN (1, 2) THEN sort_at END ASC NULLS LAST,
    CASE WHEN sort_rank IN (1, 2) THEN registration_id END ASC NULLS LAST,
    CASE WHEN sort_rank NOT IN (1, 2) THEN sort_at END DESC NULLS LAST,
    CASE WHEN sort_rank NOT IN (1, 2) THEN registration_id END DESC NULLS LAST
`

func (repository *Repository) List(
	ctx context.Context,
	filter booking.MyRegistrationFilter,
) (booking.MyRegistrationsPage, error) {
	normalized, cursor, err := normalizeMyRegistrationsFilter(filter)
	if err != nil {
		return booking.MyRegistrationsPage{}, err
	}

	var query strings.Builder
	query.WriteString(myRegistrationsQueryPrefix)
	query.WriteString(myRegistrationProjection)
	query.WriteString(myRegistrationsQueryFilter)
	args := []any{
		normalized.TenantID,
		normalized.PrincipalID,
		normalized.At,
		normalized.State,
	}
	if cursor != nil {
		query.WriteString(`
  AND (
        sort_rank > $5
        OR (
            sort_rank = $5
            AND sort_rank IN (1, 2)
            AND (sort_at, registration_id) > ($6, $7)
        )
        OR (
            sort_rank = $5
            AND sort_rank NOT IN (1, 2)
            AND (sort_at, registration_id) < ($6, $7)
        )
  )
`)
		args = append(
			args,
			cursor.SortRank,
			cursor.SortAt,
			cursor.RegistrationID,
		)
	}
	query.WriteString(myRegistrationsOrder)
	query.WriteString(fmt.Sprintf("LIMIT $%d\n", len(args)+1))
	args = append(args, normalized.Limit+1)

	rows, err := repository.db.queryContext(ctx, query.String(), args...)
	if err != nil {
		return booking.MyRegistrationsPage{}, fmt.Errorf(
			"list xiangwan My Registrations: %w",
			err,
		)
	}
	defer func() {
		_ = rows.Close()
	}()

	items := make([]booking.MyRegistrationItem, 0, normalized.Limit+1)
	for rows.Next() {
		item, scanErr := scanMyRegistrationItem(rows, normalized.At)
		if scanErr != nil {
			return booking.MyRegistrationsPage{}, fmt.Errorf(
				"scan xiangwan My Registration: %w",
				scanErr,
			)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return booking.MyRegistrationsPage{}, fmt.Errorf(
			"iterate xiangwan My Registrations: %w",
			err,
		)
	}

	nextCursor := ""
	if len(items) > normalized.Limit {
		items = items[:normalized.Limit]
		nextCursor, err = encodeMyRegistrationsCursor(
			normalized,
			items[len(items)-1],
		)
		if err != nil {
			return booking.MyRegistrationsPage{}, err
		}
	}
	return booking.MyRegistrationsPage{
		Items:       items,
		ActiveState: normalized.State,
		AsOf:        normalized.At,
		NextCursor:  nextCursor,
	}, nil
}

func scanMyRegistrationItem(
	row rowScanner,
	asOf time.Time,
) (booking.MyRegistrationItem, error) {
	var facts booking.MyRegistrationFacts
	var confirmedAt sql.NullTime
	var cancelledAt sql.NullTime
	var cancellationReason sql.NullString
	var sessionStartAt sql.NullTime
	var sessionEndAt sql.NullTime
	var deliveryMode sql.NullString
	var area sql.NullString
	var venueName sql.NullString
	var address sql.NullString
	var onlineMode sql.NullString
	var orderID uuid.NullUUID
	var paymentStatus sql.NullString
	var originalPriceCents sql.NullInt64
	var discountCents sql.NullInt64
	var payableCents sql.NullInt64
	var actualPaidCents sql.NullInt64
	var paidAt sql.NullTime
	var closedAt sql.NullTime
	var orderVersion sql.NullInt64
	var orderCreatedAt sql.NullTime
	var orderUpdatedAt sql.NullTime
	var holdID uuid.NullUUID
	var holdStatus sql.NullString
	var holdExpiresAt sql.NullTime
	var holdVersion sql.NullInt64
	var holdCreatedAt sql.NullTime
	var holdUpdatedAt sql.NullTime
	var refundCaseID uuid.NullUUID
	var refundStatus sql.NullString
	var refundReasonCode sql.NullString
	var requestedRefundCents sql.NullInt64
	var successfulRefundCents sql.NullInt64
	var refundResolvedAt sql.NullTime
	var refundVersion sql.NullInt64
	var refundCreatedAt sql.NullTime
	var refundUpdatedAt sql.NullTime
	var checkinStatus sql.NullString
	var checkedInAt sql.NullTime
	var revokedAt sql.NullTime
	var checkinUpdatedAt sql.NullTime
	var projectedState booking.MyRegistrationState
	var projectedRank int
	var projectedLastBusinessAt time.Time
	var projectedSortAt time.Time

	err := row.Scan(
		&facts.Registration.ID,
		&facts.Registration.TenantID,
		&facts.Registration.SeriesID,
		&facts.Registration.InstanceID,
		&facts.Registration.SessionID,
		&facts.Registration.PrincipalID,
		&facts.Registration.ParticipationStatus,
		&facts.Registration.IdempotencyKey,
		&confirmedAt,
		&cancelledAt,
		&cancellationReason,
		&facts.Registration.Version,
		&facts.Registration.CreatedAt,
		&facts.Registration.UpdatedAt,
		&facts.SeriesTitle,
		&facts.Instance.Title,
		&facts.Instance.Status,
		&facts.Instance.Version,
		&facts.Instance.CreatedAt,
		&facts.Instance.UpdatedAt,
		&facts.Session.Title,
		&facts.Session.Status,
		&sessionStartAt,
		&sessionEndAt,
		&deliveryMode,
		&area,
		&venueName,
		&address,
		&onlineMode,
		&facts.Session.Version,
		&facts.Session.CreatedAt,
		&facts.Session.UpdatedAt,
		&orderID,
		&paymentStatus,
		&originalPriceCents,
		&discountCents,
		&payableCents,
		&actualPaidCents,
		&paidAt,
		&closedAt,
		&orderVersion,
		&orderCreatedAt,
		&orderUpdatedAt,
		&holdID,
		&holdStatus,
		&holdExpiresAt,
		&holdVersion,
		&holdCreatedAt,
		&holdUpdatedAt,
		&refundCaseID,
		&refundStatus,
		&refundReasonCode,
		&requestedRefundCents,
		&successfulRefundCents,
		&refundResolvedAt,
		&refundVersion,
		&refundCreatedAt,
		&refundUpdatedAt,
		&checkinStatus,
		&checkedInAt,
		&revokedAt,
		&checkinUpdatedAt,
		&projectedState,
		&projectedRank,
		&projectedLastBusinessAt,
		&projectedSortAt,
	)
	if err != nil {
		return booking.MyRegistrationItem{}, err
	}

	facts.Registration.ConfirmedAt = nullTimePointer(confirmedAt)
	facts.Registration.CancelledAt = nullTimePointer(cancelledAt)
	facts.Registration.CancellationReason = nullStringPointer(cancellationReason)
	facts.Instance.ID = facts.Registration.InstanceID
	facts.Instance.TenantID = facts.Registration.TenantID
	facts.Instance.SeriesID = facts.Registration.SeriesID
	facts.Session.ID = facts.Registration.SessionID
	facts.Session.TenantID = facts.Registration.TenantID
	facts.Session.InstanceID = facts.Registration.InstanceID
	facts.Session.SessionStartAt = nullTimePointer(sessionStartAt)
	facts.Session.SessionEndAt = nullTimePointer(sessionEndAt)
	facts.Session.DeliveryMode = nullDeliveryModePointer(deliveryMode)
	facts.Session.Area = nullAreaPointer(area)
	facts.Session.VenueName = nullStringPointer(venueName)
	facts.Session.Address = nullStringPointer(address)
	facts.Session.OnlineParticipationMode = nullStringPointer(onlineMode)
	facts.Checkin, err = hydrateCheckin(
		checkinStatus,
		checkedInAt,
		revokedAt,
		checkinUpdatedAt,
	)
	if err != nil {
		return booking.MyRegistrationItem{}, err
	}

	facts.Order, err = hydrateOrder(
		facts.Registration,
		orderID,
		paymentStatus,
		originalPriceCents,
		discountCents,
		payableCents,
		actualPaidCents,
		paidAt,
		closedAt,
		orderVersion,
		orderCreatedAt,
		orderUpdatedAt,
	)
	if err != nil {
		return booking.MyRegistrationItem{}, err
	}
	facts.Hold, err = hydrateHold(
		facts.Registration,
		orderID,
		holdID,
		holdStatus,
		holdExpiresAt,
		holdVersion,
		holdCreatedAt,
		holdUpdatedAt,
	)
	if err != nil {
		return booking.MyRegistrationItem{}, err
	}
	facts.Refund, err = hydrateRefund(
		facts.Registration,
		orderID,
		refundCaseID,
		refundStatus,
		refundReasonCode,
		requestedRefundCents,
		successfulRefundCents,
		refundResolvedAt,
		refundVersion,
		refundCreatedAt,
		refundUpdatedAt,
	)
	if err != nil {
		return booking.MyRegistrationItem{}, err
	}

	item, err := booking.ProjectMyRegistration(facts, asOf)
	if err != nil {
		return booking.MyRegistrationItem{}, fmt.Errorf(
			"%w: %v",
			ErrMyRegistrationProjection,
			err,
		)
	}
	if item.State != projectedState ||
		item.SortRank != projectedRank ||
		!item.LastBusinessAt.Equal(projectedLastBusinessAt) ||
		!item.SortAt.Equal(projectedSortAt) {
		return booking.MyRegistrationItem{}, fmt.Errorf(
			"%w: SQL=(%s,%d,%s,%s) domain=(%s,%d,%s,%s)",
			ErrMyRegistrationProjection,
			projectedState,
			projectedRank,
			projectedLastBusinessAt.UTC().Format(time.RFC3339Nano),
			projectedSortAt.UTC().Format(time.RFC3339Nano),
			item.State,
			item.SortRank,
			item.LastBusinessAt.UTC().Format(time.RFC3339Nano),
			item.SortAt.UTC().Format(time.RFC3339Nano),
		)
	}
	return item, nil
}

func hydrateCheckin(
	status sql.NullString,
	checkedInAt sql.NullTime,
	revokedAt sql.NullTime,
	updatedAt sql.NullTime,
) (booking.CheckinSummary, error) {
	if !status.Valid {
		if checkedInAt.Valid || revokedAt.Valid || updatedAt.Valid {
			return booking.CheckinSummary{}, ErrMyRegistrationProjection
		}
		return booking.CheckinSummary{
			Status: booking.CheckinStatusNotRecorded,
		}, nil
	}
	if !checkedInAt.Valid || !updatedAt.Valid {
		return booking.CheckinSummary{}, ErrMyRegistrationProjection
	}
	checked := checkedInAt.Time.UTC()
	result := booking.CheckinSummary{
		Status:      booking.CheckinStatus(status.String),
		CheckedInAt: &checked,
	}
	switch result.Status {
	case booking.CheckinStatusCheckedIn:
		if revokedAt.Valid || !updatedAt.Time.Equal(checkedInAt.Time) {
			return booking.CheckinSummary{}, ErrMyRegistrationProjection
		}
	case booking.CheckinStatusRevoked:
		if !revokedAt.Valid ||
			revokedAt.Time.Before(checkedInAt.Time) ||
			!updatedAt.Time.Equal(revokedAt.Time) {
			return booking.CheckinSummary{}, ErrMyRegistrationProjection
		}
		revoked := revokedAt.Time.UTC()
		result.RevokedAt = &revoked
	default:
		return booking.CheckinSummary{}, ErrMyRegistrationProjection
	}
	return result, nil
}

func hydrateOrder(
	current registration.Registration,
	orderID uuid.NullUUID,
	status sql.NullString,
	originalPriceCents sql.NullInt64,
	discountCents sql.NullInt64,
	payableCents sql.NullInt64,
	actualPaidCents sql.NullInt64,
	paidAt sql.NullTime,
	closedAt sql.NullTime,
	version sql.NullInt64,
	createdAt sql.NullTime,
	updatedAt sql.NullTime,
) (*payment.Order, error) {
	if !orderID.Valid {
		if anyNullValueValid(
			status.Valid,
			originalPriceCents.Valid,
			discountCents.Valid,
			payableCents.Valid,
			actualPaidCents.Valid,
			paidAt.Valid,
			closedAt.Valid,
			version.Valid,
			createdAt.Valid,
			updatedAt.Valid,
		) {
			return nil, ErrMyRegistrationProjection
		}
		return nil, nil
	}
	if !allNullValuesValid(
		status.Valid,
		originalPriceCents.Valid,
		discountCents.Valid,
		payableCents.Valid,
		version.Valid,
		createdAt.Valid,
		updatedAt.Valid,
	) {
		return nil, ErrMyRegistrationProjection
	}
	return &payment.Order{
		ID:                 orderID.UUID,
		TenantID:           current.TenantID,
		RegistrationID:     current.ID,
		SeriesID:           current.SeriesID,
		InstanceID:         current.InstanceID,
		SessionID:          current.SessionID,
		PrincipalID:        current.PrincipalID,
		PaymentStatus:      payment.OrderStatus(status.String),
		OriginalPriceCents: originalPriceCents.Int64,
		DiscountCents:      discountCents.Int64,
		PayableCents:       payableCents.Int64,
		ActualPaidCents:    nullInt64Pointer(actualPaidCents),
		PaidAt:             nullTimePointer(paidAt),
		ClosedAt:           nullTimePointer(closedAt),
		Version:            version.Int64,
		CreatedAt:          createdAt.Time,
		UpdatedAt:          updatedAt.Time,
	}, nil
}

func hydrateHold(
	current registration.Registration,
	orderID uuid.NullUUID,
	holdID uuid.NullUUID,
	status sql.NullString,
	expiresAt sql.NullTime,
	version sql.NullInt64,
	createdAt sql.NullTime,
	updatedAt sql.NullTime,
) (*payment.CapacityHold, error) {
	if !holdID.Valid {
		if anyNullValueValid(
			status.Valid,
			expiresAt.Valid,
			version.Valid,
			createdAt.Valid,
			updatedAt.Valid,
		) {
			return nil, ErrMyRegistrationProjection
		}
		return nil, nil
	}
	if !orderID.Valid ||
		!allNullValuesValid(
			status.Valid,
			expiresAt.Valid,
			version.Valid,
			createdAt.Valid,
			updatedAt.Valid,
		) {
		return nil, ErrMyRegistrationProjection
	}
	return &payment.CapacityHold{
		ID:             holdID.UUID,
		TenantID:       current.TenantID,
		OrderID:        orderID.UUID,
		RegistrationID: current.ID,
		SessionID:      current.SessionID,
		HoldStatus:     payment.CapacityHoldStatus(status.String),
		ExpiresAt:      expiresAt.Time,
		Version:        version.Int64,
		CreatedAt:      createdAt.Time,
		UpdatedAt:      updatedAt.Time,
	}, nil
}

func hydrateRefund(
	current registration.Registration,
	orderID uuid.NullUUID,
	refundCaseID uuid.NullUUID,
	status sql.NullString,
	reasonCode sql.NullString,
	requestedRefundCents sql.NullInt64,
	successfulRefundCents sql.NullInt64,
	resolvedAt sql.NullTime,
	version sql.NullInt64,
	createdAt sql.NullTime,
	updatedAt sql.NullTime,
) (*refund.Case, error) {
	if !refundCaseID.Valid {
		if anyNullValueValid(
			status.Valid,
			reasonCode.Valid,
			requestedRefundCents.Valid,
			successfulRefundCents.Valid,
			resolvedAt.Valid,
			version.Valid,
			createdAt.Valid,
			updatedAt.Valid,
		) {
			return nil, ErrMyRegistrationProjection
		}
		return nil, nil
	}
	if !orderID.Valid ||
		!allNullValuesValid(
			status.Valid,
			reasonCode.Valid,
			requestedRefundCents.Valid,
			successfulRefundCents.Valid,
			version.Valid,
			createdAt.Valid,
			updatedAt.Valid,
		) {
		return nil, ErrMyRegistrationProjection
	}
	return &refund.Case{
		ID:                    refundCaseID.UUID,
		TenantID:              current.TenantID,
		OrderID:               orderID.UUID,
		RegistrationID:        current.ID,
		SeriesID:              current.SeriesID,
		InstanceID:            current.InstanceID,
		SessionID:             current.SessionID,
		PrincipalID:           current.PrincipalID,
		RefundStatus:          refund.Status(status.String),
		ReasonCode:            refund.ReasonCode(reasonCode.String),
		RequestedRefundCents:  requestedRefundCents.Int64,
		SuccessfulRefundCents: successfulRefundCents.Int64,
		ResolvedAt:            nullTimePointer(resolvedAt),
		Version:               version.Int64,
		CreatedAt:             createdAt.Time,
		UpdatedAt:             updatedAt.Time,
	}, nil
}

type decodedMyRegistrationsCursor struct {
	Version        int                         `json:"v"`
	TenantID       uuid.UUID                   `json:"tenant_id"`
	PrincipalID    uuid.UUID                   `json:"principal_id"`
	FilterState    booking.MyRegistrationState `json:"filter_state"`
	AsOf           time.Time                   `json:"as_of"`
	ItemState      booking.MyRegistrationState `json:"item_state"`
	SortRank       int                         `json:"sort_rank"`
	SortAt         time.Time                   `json:"sort_at"`
	RegistrationID uuid.UUID                   `json:"registration_id"`
}

func normalizeMyRegistrationsFilter(
	filter booking.MyRegistrationFilter,
) (
	booking.MyRegistrationFilter,
	*decodedMyRegistrationsCursor,
	error,
) {
	if filter.TenantID == uuid.Nil || filter.PrincipalID == uuid.Nil {
		return booking.MyRegistrationFilter{},
			nil,
			ErrInvalidMyRegistrationsFilter
	}
	if filter.State == "" {
		filter.State = booking.MyRegistrationStateAll
	}
	if !booking.ValidMyRegistrationState(filter.State) {
		return booking.MyRegistrationFilter{},
			nil,
			ErrInvalidMyRegistrationsFilter
	}
	switch {
	case filter.Limit == 0:
		filter.Limit = booking.DefaultMyRegistrationsLimit
	case filter.Limit < 1 || filter.Limit > booking.MaxMyRegistrationsLimit:
		return booking.MyRegistrationFilter{},
			nil,
			ErrInvalidMyRegistrationsFilter
	}
	requestedAt := filter.At
	if requestedAt.IsZero() {
		requestedAt = time.Now()
	}
	filter.At = requestedAt.UTC()
	if filter.Cursor == "" {
		return filter, nil, nil
	}

	cursor, err := decodeMyRegistrationsCursor(filter.Cursor)
	if err != nil {
		return booking.MyRegistrationFilter{}, nil, err
	}
	if cursor.TenantID != filter.TenantID ||
		cursor.PrincipalID != filter.PrincipalID ||
		cursor.FilterState != filter.State {
		return booking.MyRegistrationFilter{},
			nil,
			ErrStaleMyRegistrationsCursor
	}
	if filter.At.Sub(cursor.AsOf) > booking.MaxMyRegistrationsCursorAge ||
		cursor.AsOf.After(filter.At.Add(booking.MyRegistrationsFutureSkew)) {
		return booking.MyRegistrationFilter{},
			nil,
			ErrStaleMyRegistrationsCursor
	}
	filter.At = cursor.AsOf.UTC()
	return filter, &cursor, nil
}

func encodeMyRegistrationsCursor(
	filter booking.MyRegistrationFilter,
	item booking.MyRegistrationItem,
) (string, error) {
	encoded, err := json.Marshal(decodedMyRegistrationsCursor{
		Version:        1,
		TenantID:       filter.TenantID,
		PrincipalID:    filter.PrincipalID,
		FilterState:    filter.State,
		AsOf:           filter.At.UTC(),
		ItemState:      item.State,
		SortRank:       item.SortRank,
		SortAt:         item.SortAt.UTC(),
		RegistrationID: item.RegistrationID,
	})
	if err != nil {
		return "", fmt.Errorf(
			"%w: encode",
			ErrInvalidMyRegistrationsCursor,
		)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeMyRegistrationsCursor(
	value string,
) (decodedMyRegistrationsCursor, error) {
	if len(value) > 2048 {
		return decodedMyRegistrationsCursor{}, fmt.Errorf(
			"%w: payload is too large",
			ErrInvalidMyRegistrationsCursor,
		)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return decodedMyRegistrationsCursor{}, fmt.Errorf(
			"%w: malformed base64",
			ErrInvalidMyRegistrationsCursor,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var cursor decodedMyRegistrationsCursor
	if err := decoder.Decode(&cursor); err != nil {
		return decodedMyRegistrationsCursor{}, fmt.Errorf(
			"%w: malformed payload",
			ErrInvalidMyRegistrationsCursor,
		)
	}
	if err := ensureMyRegistrationsCursorEOF(decoder); err != nil {
		return decodedMyRegistrationsCursor{}, err
	}
	if cursor.Version != 1 ||
		cursor.TenantID == uuid.Nil ||
		cursor.PrincipalID == uuid.Nil ||
		!booking.ValidMyRegistrationState(cursor.FilterState) ||
		!booking.ValidMyRegistrationState(cursor.ItemState) ||
		cursor.ItemState == booking.MyRegistrationStateAll ||
		cursor.AsOf.IsZero() ||
		cursor.SortRank != booking.MyRegistrationStateSortRank(cursor.ItemState) ||
		cursor.SortAt.IsZero() ||
		cursor.RegistrationID == uuid.Nil ||
		(cursor.FilterState != booking.MyRegistrationStateAll &&
			cursor.FilterState != cursor.ItemState) {
		return decodedMyRegistrationsCursor{}, fmt.Errorf(
			"%w: invalid fields",
			ErrInvalidMyRegistrationsCursor,
		)
	}
	cursor.AsOf = cursor.AsOf.UTC()
	cursor.SortAt = cursor.SortAt.UTC()
	return cursor, nil
}

func ensureMyRegistrationsCursorEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf(
			"%w: trailing payload",
			ErrInvalidMyRegistrationsCursor,
		)
	}
	return nil
}

func anyNullValueValid(values ...bool) bool {
	for _, value := range values {
		if value {
			return true
		}
	}
	return false
}

func allNullValuesValid(values ...bool) bool {
	for _, value := range values {
		if !value {
			return false
		}
	}
	return true
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	normalized := value.Time.UTC()
	return &normalized
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func nullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func nullDeliveryModePointer(
	value sql.NullString,
) *activity.DeliveryMode {
	if !value.Valid {
		return nil
	}
	result := activity.DeliveryMode(value.String)
	return &result
}

func nullAreaPointer(value sql.NullString) *activity.AreaCode {
	if !value.Valid {
		return nil
	}
	result := activity.AreaCode(value.String)
	return &result
}
