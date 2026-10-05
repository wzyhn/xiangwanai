package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

var (
	ErrCouponNotFound      = errors.New(`xiangwan Coupon not found`)
	ErrLedgerEntryNotFound = errors.New(`xiangwan Coupon ledger entry not found`)
)

type Ledger struct {
	Instrument coupon.Coupon
	Entries    []coupon.Entry
}

type PendingCorrectionSource struct {
	TenantID  uuid.UUID
	CheckinID uuid.UUID
}

// ReleaseOrderHold appends the deterministic release fact inside the caller's
// transaction. Orders without a selected Coupon are a successful no-op.
func (repository *Repository) ReleaseOrderHold(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
	reason string,
	at time.Time,
) (*coupon.Entry, error) {
	ledger, err := repository.GetLedgerByOrderForUpdate(ctx, tenantID, orderID)
	if errors.Is(err, ErrCouponNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for index := len(ledger.Entries) - 1; index >= 0; index-- {
		entry := ledger.Entries[index]
		if entry.EntryType == coupon.EntryTypeReleased &&
			entry.OrderID != nil && *entry.OrderID == orderID {
			return &entry, nil
		}
	}
	entry, err := coupon.Release(coupon.ReleaseCommand{
		Instrument: ledger.Instrument,
		History:    ledger.Entries,
		OrderID:    orderID,
		Reason:     reason,
		At:         at,
		RecordedAt: at,
	})
	if err != nil {
		return nil, err
	}
	created, err := repository.AppendLifecycleEntry(ctx, entry)
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// GetLedgerByOrderForUpdate resolves the single Coupon selected for an Order,
// then locks the instrument before its ordered ledger. The initial lookup is
// deliberately lock-free so every writer keeps the same Order -> Coupon ->
// ledger lock order.
func (repository *Repository) GetLedgerByOrderForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (Ledger, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || orderID == uuid.Nil {
		return Ledger{}, coupon.ErrInvalidLedger
	}
	var couponID uuid.UUID
	var principalID uuid.UUID
	err := repository.db.QueryRowContext(ctx, `
SELECT coupon_id, principal_id
FROM xiangwan_coupon_entries
WHERE tenant_id = $1
  AND order_id = $2
  AND entry_type = 'held'
ORDER BY entry_sequence
LIMIT 1
`, tenantID, orderID).Scan(&couponID, &principalID)
	if errors.Is(err, sql.ErrNoRows) {
		return Ledger{}, ErrCouponNotFound
	}
	if err != nil {
		return Ledger{}, fmt.Errorf(
			`resolve xiangwan Order Coupon ledger: %w`,
			err,
		)
	}
	ledger, err := repository.GetLedgerForUpdate(
		ctx,
		tenantID,
		principalID,
		couponID,
	)
	if err != nil {
		return Ledger{}, err
	}
	for _, entry := range ledger.Entries {
		if entry.EntryType == coupon.EntryTypeHeld &&
			entry.OrderID != nil && *entry.OrderID == orderID {
			return ledger, nil
		}
	}
	return Ledger{}, ErrGrantFactsConflict
}

func (repository *Repository) GetLedgerForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	couponID uuid.UUID,
) (Ledger, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || principalID == uuid.Nil ||
		couponID == uuid.Nil {
		return Ledger{}, coupon.ErrInvalidLedger
	}
	instrument, err := scanCoupon(repository.db.QueryRowContext(ctx, `
SELECT
    id, tenant_id, principal_id, benefit_type, face_value_cents,
    scope_type, scope_activity_type, scope_series_id, minimum_order_cents,
    valid_from, expires_at, grant_kind, grant_business_key, grant_ordinal,
    policy_version, source_people_profile_id, source_people_binding_id,
    source_role_binding_id, source_checkin_id, source_checkin_event_id,
    granted_by, grant_reason, grant_context, granted_at, created_at
FROM xiangwan_coupons
WHERE tenant_id = $1 AND principal_id = $2 AND id = $3
FOR UPDATE
`, tenantID, principalID, couponID))
	if errors.Is(err, sql.ErrNoRows) {
		return Ledger{}, ErrCouponNotFound
	}
	if err != nil {
		return Ledger{}, fmt.Errorf(`lock xiangwan Coupon: %w`, err)
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT
    id, tenant_id, coupon_id, principal_id, entry_sequence, entry_type,
    business_key, order_id, registration_id, related_entry_id,
    refund_case_id, source_checkin_event_id, actor_id, reason,
    refund_policy_version, occurred_at, recorded_at
FROM xiangwan_coupon_entries
WHERE tenant_id = $1 AND coupon_id = $2
ORDER BY entry_sequence
FOR UPDATE
`, tenantID, couponID)
	if err != nil {
		return Ledger{}, fmt.Errorf(`lock xiangwan Coupon ledger: %w`, err)
	}
	defer rows.Close()
	entries := make([]coupon.Entry, 0)
	for rows.Next() {
		entry, scanErr := scanLifecycleEntry(rows)
		if scanErr != nil {
			return Ledger{}, fmt.Errorf(
				`scan xiangwan Coupon ledger: %w`,
				scanErr,
			)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return Ledger{}, fmt.Errorf(
			`iterate xiangwan Coupon ledger: %w`,
			err,
		)
	}
	if len(entries) == 0 {
		return Ledger{}, coupon.ErrInvalidLedger
	}
	projectionAt := entries[0].OccurredAt
	for index := 1; index < len(entries); index++ {
		if entries[index].OccurredAt.After(projectionAt) {
			projectionAt = entries[index].OccurredAt
		}
	}
	if _, err := coupon.Project(instrument, entries, projectionAt); err != nil {
		return Ledger{}, ErrGrantFactsConflict
	}
	return Ledger{Instrument: instrument, Entries: entries}, nil
}

func (repository *Repository) GetLifecycleEntryForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	couponID uuid.UUID,
	entryType coupon.EntryType,
	businessKey string,
) (coupon.Entry, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || couponID == uuid.Nil ||
		businessKey == `` {
		return coupon.Entry{}, coupon.ErrInvalidLedger
	}
	value, err := scanLifecycleEntry(repository.db.QueryRowContext(ctx, `
SELECT
    id, tenant_id, coupon_id, principal_id, entry_sequence, entry_type,
    business_key, order_id, registration_id, related_entry_id,
    refund_case_id, source_checkin_event_id, actor_id, reason,
    refund_policy_version, occurred_at, recorded_at
FROM xiangwan_coupon_entries
WHERE tenant_id = $1
  AND coupon_id = $2
  AND entry_type = $3
  AND business_key = $4
FOR UPDATE
`, tenantID, couponID, entryType, businessKey))
	if errors.Is(err, sql.ErrNoRows) {
		return coupon.Entry{}, ErrLedgerEntryNotFound
	}
	if err != nil {
		return coupon.Entry{}, fmt.Errorf(
			`get xiangwan Coupon ledger entry: %w`,
			err,
		)
	}
	return value, nil
}

func (repository *Repository) AppendLifecycleEntry(
	ctx context.Context,
	value coupon.Entry,
) (coupon.Entry, error) {
	if repository == nil || repository.db == nil ||
		value.EntryType == coupon.EntryTypeGranted ||
		coupon.ValidateEntry(value) != nil {
		return coupon.Entry{}, coupon.ErrInvalidEntry
	}
	result, err := repository.db.ExecContext(ctx, `
INSERT INTO xiangwan_coupon_entries (
    id, tenant_id, coupon_id, principal_id, entry_sequence, entry_type,
    business_key, order_id, registration_id, related_entry_id,
    refund_case_id, source_checkin_event_id, actor_id, reason,
    refund_policy_version, occurred_at, recorded_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17
)
ON CONFLICT DO NOTHING
`,
		value.ID,
		value.TenantID,
		value.CouponID,
		value.PrincipalID,
		value.EntrySequence,
		value.EntryType,
		value.BusinessKey,
		value.OrderID,
		value.RegistrationID,
		value.RelatedEntryID,
		value.RefundCaseID,
		value.SourceCheckinEventID,
		value.ActorID,
		value.Reason,
		value.RefundPolicyVersion,
		value.OccurredAt,
		value.RecordedAt,
	)
	if err != nil {
		return coupon.Entry{}, fmt.Errorf(
			`append xiangwan Coupon ledger entry: %w`,
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return coupon.Entry{}, fmt.Errorf(
			`read xiangwan Coupon ledger insert result: %w`,
			err,
		)
	}
	if affected != 1 {
		return coupon.Entry{}, ErrGrantExists
	}
	return value, nil
}

func (repository *Repository) ListPendingCorrectionSources(
	ctx context.Context,
	tenantID uuid.UUID,
	limit int,
) ([]PendingCorrectionSource, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || limit < 1 || limit > 500 {
		return nil, ErrInvalidCorrectionCommand
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT
    instrument.tenant_id,
    instrument.source_checkin_id
FROM xiangwan_coupons AS instrument
JOIN xiangwan_checkins AS current_checkin
  ON current_checkin.tenant_id = instrument.tenant_id
 AND current_checkin.id = instrument.source_checkin_id
 AND current_checkin.checkin_status = 'revoked'
JOIN xiangwan_checkin_events AS revoked_event
  ON revoked_event.tenant_id = current_checkin.tenant_id
 AND revoked_event.checkin_id = current_checkin.id
 AND revoked_event.event_type = 'revoked'
WHERE instrument.tenant_id = $1
  AND instrument.grant_kind = 'initial_guest'
  AND (
      NOT EXISTS (
          SELECT 1
          FROM xiangwan_coupon_entries AS correction
          WHERE correction.tenant_id = instrument.tenant_id
            AND correction.coupon_id = instrument.id
            AND correction.source_checkin_event_id = revoked_event.id
            AND correction.entry_type IN (
                'invalidated', 'correction_required'
            )
      )
      OR (
          EXISTS (
              SELECT 1
              FROM xiangwan_coupon_entries AS correction
              WHERE correction.tenant_id = instrument.tenant_id
                AND correction.coupon_id = instrument.id
                AND correction.source_checkin_event_id = revoked_event.id
                AND correction.entry_type = 'correction_required'
          )
          AND NOT EXISTS (
              SELECT 1
              FROM xiangwan_coupon_entries AS terminal_entry
              WHERE terminal_entry.tenant_id = instrument.tenant_id
                AND terminal_entry.coupon_id = instrument.id
                AND terminal_entry.entry_type IN ('redeemed', 'invalidated')
          )
          AND (
              SELECT COUNT(*)
              FROM xiangwan_coupon_entries AS held_entry
              WHERE held_entry.tenant_id = instrument.tenant_id
                AND held_entry.coupon_id = instrument.id
                AND held_entry.entry_type = 'held'
          ) = (
              SELECT COUNT(*)
              FROM xiangwan_coupon_entries AS released_entry
              WHERE released_entry.tenant_id = instrument.tenant_id
                AND released_entry.coupon_id = instrument.id
                AND released_entry.entry_type = 'released'
          )
      )
  )
GROUP BY
    instrument.tenant_id,
    instrument.source_checkin_id
ORDER BY MIN(revoked_event.occurred_at), instrument.source_checkin_id
LIMIT $2
`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf(
			`list pending xiangwan Coupon corrections: %w`,
			err,
		)
	}
	defer rows.Close()
	result := make([]PendingCorrectionSource, 0)
	for rows.Next() {
		var source PendingCorrectionSource
		if err := rows.Scan(&source.TenantID, &source.CheckinID); err != nil {
			return nil, fmt.Errorf(
				`scan pending xiangwan Coupon correction: %w`,
				err,
			)
		}
		if source.TenantID != tenantID || source.CheckinID == uuid.Nil {
			return nil, ErrGrantFactsConflict
		}
		result = append(result, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			`iterate pending xiangwan Coupon corrections: %w`,
			err,
		)
	}
	return result, nil
}

func scanCoupon(row rowScanner) (coupon.Coupon, error) {
	var value coupon.Coupon
	var scopeActivity sql.NullString
	var scopeSeries uuid.NullUUID
	var sourceProfile uuid.NullUUID
	var sourceBinding uuid.NullUUID
	var sourceRole uuid.NullUUID
	var sourceCheckin uuid.NullUUID
	var sourceEvent uuid.NullUUID
	var grantedBy uuid.NullUUID
	var reason sql.NullString
	var grantContext sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.PrincipalID,
		&value.BenefitType,
		&value.FaceValueCents,
		&value.ScopeType,
		&scopeActivity,
		&scopeSeries,
		&value.MinimumOrderCents,
		&value.ValidFrom,
		&value.ExpiresAt,
		&value.GrantKind,
		&value.GrantBusinessKey,
		&value.GrantOrdinal,
		&value.PolicyVersion,
		&sourceProfile,
		&sourceBinding,
		&sourceRole,
		&sourceCheckin,
		&sourceEvent,
		&grantedBy,
		&reason,
		&grantContext,
		&value.GrantedAt,
		&value.CreatedAt,
	)
	if err != nil {
		return coupon.Coupon{}, err
	}
	if scopeActivity.Valid {
		activityType := activity.ActivityType(scopeActivity.String)
		value.ScopeActivityType = &activityType
	}
	value.ScopeSeriesID = nullableUUID(scopeSeries)
	value.SourcePeopleProfile = nullableUUID(sourceProfile)
	value.SourcePeopleBinding = nullableUUID(sourceBinding)
	value.SourceRoleBinding = nullableUUID(sourceRole)
	value.SourceCheckin = nullableUUID(sourceCheckin)
	value.SourceCheckinEvent = nullableUUID(sourceEvent)
	value.GrantedBy = nullableUUID(grantedBy)
	value.GrantReason = nullableString(reason)
	value.GrantContext = nullableString(grantContext)
	if coupon.ValidateCoupon(value) != nil {
		return coupon.Coupon{}, coupon.ErrInvalidCoupon
	}
	return value, nil
}

func scanLifecycleEntry(row rowScanner) (coupon.Entry, error) {
	var value coupon.Entry
	var orderID uuid.NullUUID
	var registrationID uuid.NullUUID
	var relatedEntryID uuid.NullUUID
	var refundCaseID uuid.NullUUID
	var sourceEventID uuid.NullUUID
	var actorID uuid.NullUUID
	var reason sql.NullString
	var refundPolicyVersion sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.CouponID,
		&value.PrincipalID,
		&value.EntrySequence,
		&value.EntryType,
		&value.BusinessKey,
		&orderID,
		&registrationID,
		&relatedEntryID,
		&refundCaseID,
		&sourceEventID,
		&actorID,
		&reason,
		&refundPolicyVersion,
		&value.OccurredAt,
		&value.RecordedAt,
	)
	if err != nil {
		return coupon.Entry{}, err
	}
	value.OrderID = nullableUUID(orderID)
	value.RegistrationID = nullableUUID(registrationID)
	value.RelatedEntryID = nullableUUID(relatedEntryID)
	value.RefundCaseID = nullableUUID(refundCaseID)
	value.SourceCheckinEventID = nullableUUID(sourceEventID)
	value.ActorID = nullableUUID(actorID)
	value.Reason = nullableString(reason)
	value.RefundPolicyVersion = nullableString(refundPolicyVersion)
	if coupon.ValidateEntry(value) != nil {
		return coupon.Entry{}, coupon.ErrInvalidEntry
	}
	return value, nil
}
