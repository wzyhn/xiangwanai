package bookingpostgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyRegistrationIdentity = booking.ErrInvalidMyRegistrationIdentity
	ErrMyRegistrationNotFound        = booking.ErrMyRegistrationNotFound
)

const myRegistrationDetailProjection = myRegistrationProjection + `,
    session_cancellation_receipt_id,
    session_cancellation_reason,
    session_cancelled_at,
    instance_cancellation_receipt_id,
    instance_cancellation_reason,
    instance_cancelled_at
`

func (repository *Repository) GetRegistrationDetail(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	registrationID uuid.UUID,
	at time.Time,
) (booking.MyRegistrationDetail, error) {
	if tenantID == uuid.Nil ||
		principalID == uuid.Nil ||
		registrationID == uuid.Nil {
		return booking.MyRegistrationDetail{},
			ErrInvalidMyRegistrationIdentity
	}
	if at.IsZero() {
		at = time.Now()
	}
	asOf := at.UTC()
	query := myRegistrationsQueryPrefix + `
    registration_detail.*,
    registration_snapshot.contact_name,
    registration_snapshot.contact_phone_e164,
    coupon_adjustment.entry_type AS coupon_adjustment_entry_type,
    coupon_adjustment.refund_policy_version
        AS coupon_adjustment_policy_version,
    coupon_adjustment.occurred_at AS coupon_adjustment_occurred_at
FROM (
    SELECT` + myRegistrationDetailProjection + `
    FROM ranked
    WHERE tenant_id = $1
      AND principal_id = $2
      AND registration_id = $4
) AS registration_detail
LEFT JOIN xiangwan_registration_snapshots AS registration_snapshot
  ON registration_snapshot.tenant_id = registration_detail.tenant_id
 AND registration_snapshot.principal_id = registration_detail.principal_id
 AND registration_snapshot.registration_id = registration_detail.registration_id
 AND registration_snapshot.series_id = registration_detail.series_id
 AND registration_snapshot.instance_id = registration_detail.instance_id
 AND registration_snapshot.session_id = registration_detail.session_id
LEFT JOIN xiangwan_coupon_entries AS coupon_adjustment
  ON coupon_adjustment.tenant_id = registration_detail.tenant_id
 AND coupon_adjustment.principal_id = registration_detail.principal_id
 AND coupon_adjustment.order_id = registration_detail.order_id
 AND coupon_adjustment.registration_id = registration_detail.registration_id
 AND coupon_adjustment.entry_type IN ('restored', 'forfeited')
 AND (
      (
          registration_detail.payment_status = 'settled_zero'
          AND coupon_adjustment.refund_case_id IS NULL
      )
      OR (
          registration_detail.payment_status = 'paid_confirmed'
          AND coupon_adjustment.refund_case_id = registration_detail.refund_case_id
      )
 )
LIMIT 2
`
	rows, err := repository.db.queryContext(
		ctx,
		query,
		tenantID,
		principalID,
		asOf,
		registrationID,
	)
	if err != nil {
		return booking.MyRegistrationDetail{}, fmt.Errorf(
			"get xiangwan My Registration detail: %w",
			err,
		)
	}
	defer func() {
		_ = rows.Close()
	}()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return booking.MyRegistrationDetail{}, fmt.Errorf(
				"iterate xiangwan My Registration detail: %w",
				err,
			)
		}
		return booking.MyRegistrationDetail{}, ErrMyRegistrationNotFound
	}
	detail, err := scanMyRegistrationDetail(rows, asOf)
	if err != nil {
		return booking.MyRegistrationDetail{}, fmt.Errorf(
			"scan xiangwan My Registration detail: %w",
			err,
		)
	}
	if rows.Next() {
		return booking.MyRegistrationDetail{}, fmt.Errorf(
			"%w: duplicate Registration identity",
			ErrMyRegistrationProjection,
		)
	}
	if err := rows.Err(); err != nil {
		return booking.MyRegistrationDetail{}, fmt.Errorf(
			"iterate xiangwan My Registration detail: %w",
			err,
		)
	}
	return detail, nil
}

type myRegistrationDetailRowScanner struct {
	row                           rowScanner
	sessionCancellationReceiptID  uuid.NullUUID
	sessionCancellationReason     sql.NullString
	sessionCancelledAt            sql.NullTime
	instanceCancellationReceiptID uuid.NullUUID
	instanceCancellationReason    sql.NullString
	instanceCancelledAt           sql.NullTime
	contactName                   sql.NullString
	contactPhoneE164              sql.NullString
	couponAdjustmentEntryType     sql.NullString
	couponAdjustmentPolicyVersion sql.NullString
	couponAdjustmentOccurredAt    sql.NullTime
}

func (scanner *myRegistrationDetailRowScanner) Scan(
	destinations ...any,
) error {
	allDestinations := make([]any, 0, len(destinations)+11)
	allDestinations = append(allDestinations, destinations...)
	allDestinations = append(
		allDestinations,
		&scanner.sessionCancellationReceiptID,
		&scanner.sessionCancellationReason,
		&scanner.sessionCancelledAt,
		&scanner.instanceCancellationReceiptID,
		&scanner.instanceCancellationReason,
		&scanner.instanceCancelledAt,
		&scanner.contactName,
		&scanner.contactPhoneE164,
		&scanner.couponAdjustmentEntryType,
		&scanner.couponAdjustmentPolicyVersion,
		&scanner.couponAdjustmentOccurredAt,
	)
	return scanner.row.Scan(allDestinations...)
}

func scanMyRegistrationDetail(
	row rowScanner,
	asOf time.Time,
) (booking.MyRegistrationDetail, error) {
	scanner := &myRegistrationDetailRowScanner{row: row}
	item, err := scanMyRegistrationItem(scanner, asOf)
	if err != nil {
		return booking.MyRegistrationDetail{}, err
	}
	sessionCancellation, err := hydrateRegistrationCancellation(
		scanner.sessionCancellationReceiptID,
		scanner.sessionCancellationReason,
		scanner.sessionCancelledAt,
		booking.MyRegistrationCancellationScopeSession,
		item,
	)
	if err != nil {
		return booking.MyRegistrationDetail{}, err
	}
	instanceCancellation, err := hydrateRegistrationCancellation(
		scanner.instanceCancellationReceiptID,
		scanner.instanceCancellationReason,
		scanner.instanceCancelledAt,
		booking.MyRegistrationCancellationScopeInstance,
		item,
	)
	if err != nil {
		return booking.MyRegistrationDetail{}, err
	}
	couponAdjustment, err := hydrateRegistrationCouponAdjustment(
		scanner.couponAdjustmentEntryType,
		scanner.couponAdjustmentPolicyVersion,
		scanner.couponAdjustmentOccurredAt,
	)
	if err != nil {
		return booking.MyRegistrationDetail{}, err
	}
	contact, err := hydrateMyRegistrationContact(
		scanner.contactName,
		scanner.contactPhoneE164,
	)
	if err != nil {
		return booking.MyRegistrationDetail{}, err
	}
	detail, err := booking.ProjectMyRegistrationDetail(
		booking.MyRegistrationDetailFacts{
			Item:                 item,
			Contact:              contact,
			SessionCancellation:  sessionCancellation,
			InstanceCancellation: instanceCancellation,
			CouponAdjustment:     couponAdjustment,
		},
		asOf,
	)
	if err != nil {
		return booking.MyRegistrationDetail{}, fmt.Errorf(
			"%w: %v",
			ErrMyRegistrationProjection,
			err,
		)
	}
	return detail, nil
}

func hydrateMyRegistrationContact(
	name sql.NullString,
	phoneE164 sql.NullString,
) (booking.MyRegistrationContactFacts, error) {
	if !name.Valid && !phoneE164.Valid {
		return booking.MyRegistrationContactFacts{}, nil
	}
	if !name.Valid || !phoneE164.Valid {
		return booking.MyRegistrationContactFacts{}, ErrMyRegistrationProjection
	}
	return booking.MyRegistrationContactFacts{
		Name:      name.String,
		PhoneE164: phoneE164.String,
	}, nil
}

func hydrateRegistrationCouponAdjustment(
	entryType sql.NullString,
	policyVersion sql.NullString,
	occurredAt sql.NullTime,
) (*booking.MyRegistrationCouponAdjustment, error) {
	if !entryType.Valid {
		if policyVersion.Valid || occurredAt.Valid {
			return nil, ErrMyRegistrationProjection
		}
		return nil, nil
	}
	if !policyVersion.Valid || !occurredAt.Valid {
		return nil, ErrMyRegistrationProjection
	}
	var disposition coupon.RefundDisposition
	switch coupon.EntryType(entryType.String) {
	case coupon.EntryTypeRestored:
		disposition = coupon.RefundDispositionRestore
	case coupon.EntryTypeForfeited:
		disposition = coupon.RefundDispositionForfeit
	default:
		return nil, ErrMyRegistrationProjection
	}
	return &booking.MyRegistrationCouponAdjustment{
		Disposition:   disposition,
		PolicyVersion: policyVersion.String,
		OccurredAt:    occurredAt.Time.UTC(),
	}, nil
}

func hydrateRegistrationCancellation(
	receiptID uuid.NullUUID,
	reason sql.NullString,
	cancelledAt sql.NullTime,
	scope booking.MyRegistrationCancellationScope,
	item booking.MyRegistrationItem,
) (*booking.MyRegistrationCancellationFact, error) {
	if !receiptID.Valid {
		if reason.Valid || cancelledAt.Valid {
			return nil, ErrMyRegistrationProjection
		}
		return nil, nil
	}
	if !reason.Valid || !cancelledAt.Valid {
		return nil, ErrMyRegistrationProjection
	}
	fact := &booking.MyRegistrationCancellationFact{
		ReceiptID:  receiptID.UUID,
		Scope:      scope,
		SeriesID:   item.SeriesID,
		InstanceID: item.InstanceID,
		Reason:     reason.String,
		At:         cancelledAt.Time.UTC(),
	}
	if scope == booking.MyRegistrationCancellationScopeSession {
		sessionID := item.SessionID
		fact.SessionID = &sessionID
	}
	return fact, nil
}
