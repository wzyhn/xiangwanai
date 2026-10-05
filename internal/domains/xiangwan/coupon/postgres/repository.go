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
	ErrGrantNotFound = errors.New(`xiangwan Coupon grant not found`)
	ErrGrantExists   = errors.New(`xiangwan Coupon grant already exists`)
)

type DBTX interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type PendingInitialGuestSource struct {
	TenantID  uuid.UUID
	CheckinID uuid.UUID
}

type GrantState struct {
	HasHistory       bool
	CurrentAvailable int
}

type Repository struct {
	db DBTX
}

func NewRepository(db DBTX) *Repository {
	return &Repository{db: db}
}

func (repository *Repository) GetGrantForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	kind coupon.GrantKind,
	businessKey string,
) (coupon.Grant, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || principalID == uuid.Nil {
		return coupon.Grant{}, coupon.ErrInvalidGrant
	}
	rows, err := repository.db.QueryContext(ctx, grantSelectSQL+`
WHERE instrument.tenant_id = $1
  AND instrument.principal_id = $2
  AND instrument.grant_kind = $3
  AND instrument.grant_business_key = $4
ORDER BY instrument.grant_ordinal
FOR UPDATE OF instrument
`, tenantID, principalID, kind, businessKey)
	if err != nil {
		return coupon.Grant{}, fmt.Errorf(`get xiangwan Coupon grant: %w`, err)
	}
	defer rows.Close()
	return scanGrantRows(rows)
}

func (repository *Repository) CreateGrant(
	ctx context.Context,
	value coupon.Grant,
) (coupon.Grant, error) {
	if repository == nil || repository.db == nil ||
		coupon.ValidateGrant(value) != nil {
		return coupon.Grant{}, coupon.ErrInvalidGrant
	}
	for index := range value.Coupons {
		if err := repository.createCoupon(ctx, value.Coupons[index]); err != nil {
			return coupon.Grant{}, err
		}
	}
	for index := range value.Entries {
		if err := repository.createEntry(ctx, value.Entries[index]); err != nil {
			return coupon.Grant{}, err
		}
	}
	return value, nil
}

func (repository *Repository) CurrentGrantState(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	at time.Time,
) (GrantState, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || principalID == uuid.Nil || at.IsZero() {
		return GrantState{}, coupon.ErrInvalidGrant
	}
	var result GrantState
	err := repository.db.QueryRowContext(ctx, `
SELECT
    COUNT(*) > 0,
    COUNT(*) FILTER (
        WHERE NOT EXISTS (
            SELECT 1
            FROM xiangwan_coupon_entries AS terminal_entry
            WHERE terminal_entry.tenant_id = xiangwan_coupons.tenant_id
              AND terminal_entry.coupon_id = xiangwan_coupons.id
              AND terminal_entry.entry_type IN (
                  'invalidated', 'forfeited', 'correction_required'
              )
        )
          AND (
              SELECT COUNT(*)
              FROM xiangwan_coupon_entries AS redeemed_entry
              WHERE redeemed_entry.tenant_id = xiangwan_coupons.tenant_id
                AND redeemed_entry.coupon_id = xiangwan_coupons.id
                AND redeemed_entry.entry_type = 'redeemed'
          ) = (
              SELECT COUNT(*)
              FROM xiangwan_coupon_entries AS restored_entry
              WHERE restored_entry.tenant_id = xiangwan_coupons.tenant_id
                AND restored_entry.coupon_id = xiangwan_coupons.id
                AND restored_entry.entry_type = 'restored'
          )
          AND (
              (
                  valid_from <= $3
                  AND expires_at > $3
              )
              OR (
                  SELECT COUNT(*)
                  FROM xiangwan_coupon_entries AS held_entry
                  WHERE held_entry.tenant_id = xiangwan_coupons.tenant_id
                    AND held_entry.coupon_id = xiangwan_coupons.id
                    AND held_entry.entry_type = 'held'
              ) > (
                  SELECT COUNT(*)
                  FROM xiangwan_coupon_entries AS closing_entry
                  WHERE closing_entry.tenant_id = xiangwan_coupons.tenant_id
                    AND closing_entry.coupon_id = xiangwan_coupons.id
                    AND closing_entry.entry_type IN ('released', 'redeemed')
              )
          )
    )
FROM xiangwan_coupons
WHERE tenant_id = $1
  AND principal_id = $2
  AND benefit_type = 'roundtable_coupon'
`, tenantID, principalID, at).Scan(
		&result.HasHistory,
		&result.CurrentAvailable,
	)
	if err != nil {
		return GrantState{}, fmt.Errorf(
			`read xiangwan Coupon grant state: %w`,
			err,
		)
	}
	return result, nil
}

// ListPendingInitialGuestSources reconstructs retryable work entirely from
// PostgreSQL facts. DISTINCT ON selects the first eligible Checkin per
// Principal, while the anti-join makes a completed five-Coupon batch terminal.
func (repository *Repository) ListPendingInitialGuestSources(
	ctx context.Context,
	tenantID uuid.UUID,
	limit int,
) ([]PendingInitialGuestSource, error) {
	if repository == nil || repository.db == nil ||
		tenantID == uuid.Nil || limit < 1 || limit > 500 {
		return nil, ErrInvalidGrantCommand
	}
	rows, err := repository.db.QueryContext(ctx, `
SELECT pending.tenant_id, pending.checkin_id
FROM (
    SELECT DISTINCT ON (current_checkin.principal_id)
        current_checkin.tenant_id,
        current_checkin.id AS checkin_id,
        checked_in_event.occurred_at,
        current_checkin.principal_id
    FROM xiangwan_checkins AS current_checkin
    JOIN xiangwan_checkin_events AS checked_in_event
      ON checked_in_event.tenant_id = current_checkin.tenant_id
     AND checked_in_event.checkin_id = current_checkin.id
     AND checked_in_event.event_type = 'checked_in'
     AND checked_in_event.event_sequence = 1
    JOIN xiangwan_people_bindings AS people_binding
      ON people_binding.tenant_id = current_checkin.tenant_id
     AND people_binding.principal_id = current_checkin.principal_id
     AND people_binding.bound_at <= checked_in_event.occurred_at
     AND (
         people_binding.revoked_at IS NULL
         OR people_binding.revoked_at > checked_in_event.occurred_at
     )
    JOIN xiangwan_instance_role_bindings AS role_binding
      ON role_binding.tenant_id = current_checkin.tenant_id
     AND role_binding.series_id = current_checkin.series_id
     AND role_binding.instance_id = current_checkin.instance_id
     AND role_binding.principal_id = current_checkin.principal_id
     AND role_binding.role_code = 'invited_guest'
     AND role_binding.granted_at <= checked_in_event.occurred_at
     AND (
         role_binding.revoked_at IS NULL
         OR role_binding.revoked_at > checked_in_event.occurred_at
     )
    LEFT JOIN xiangwan_coupons AS initial_coupon
      ON initial_coupon.tenant_id = current_checkin.tenant_id
     AND initial_coupon.principal_id = current_checkin.principal_id
     AND initial_coupon.benefit_type = 'roundtable_coupon'
     AND initial_coupon.grant_kind = 'initial_guest'
     AND initial_coupon.grant_business_key = 'initial_guest_grant'
    WHERE current_checkin.tenant_id = $1
      AND current_checkin.checkin_status = 'checked_in'
      AND initial_coupon.id IS NULL
    ORDER BY
        current_checkin.principal_id,
        checked_in_event.occurred_at,
        current_checkin.id
) AS pending
WHERE (
    SELECT policy.enabled
    FROM xiangwan_coupon_grant_policy_versions AS policy
    WHERE policy.tenant_id = pending.tenant_id
      AND policy.effective_at <= pending.occurred_at
    ORDER BY policy.effective_at DESC
    LIMIT 1
) IS TRUE
ORDER BY pending.occurred_at, pending.checkin_id
LIMIT $2
`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf(
			`list pending xiangwan Coupon sources: %w`,
			err,
		)
	}
	defer rows.Close()

	result := make([]PendingInitialGuestSource, 0)
	for rows.Next() {
		var source PendingInitialGuestSource
		if err := rows.Scan(&source.TenantID, &source.CheckinID); err != nil {
			return nil, fmt.Errorf(
				`scan pending xiangwan Coupon source: %w`,
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
			`iterate pending xiangwan Coupon sources: %w`,
			err,
		)
	}
	return result, nil
}

func (repository *Repository) createCoupon(
	ctx context.Context,
	value coupon.Coupon,
) error {
	if coupon.ValidateCoupon(value) != nil {
		return coupon.ErrInvalidCoupon
	}
	result, err := repository.db.ExecContext(ctx, `
INSERT INTO xiangwan_coupons (
    id, tenant_id, principal_id, benefit_type, face_value_cents,
    scope_type, scope_activity_type, scope_series_id, minimum_order_cents,
    valid_from, expires_at, grant_kind, grant_business_key, grant_ordinal,
    policy_version, source_people_profile_id, source_people_binding_id,
    source_role_binding_id, source_checkin_id, source_checkin_event_id,
    granted_by, grant_reason, grant_context, granted_at, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12, $13, $14,
    $15, $16, $17,
    $18, $19, $20,
    $21, $22, $23, $24, $25
)
ON CONFLICT DO NOTHING
`,
		value.ID, value.TenantID, value.PrincipalID, value.BenefitType,
		value.FaceValueCents, value.ScopeType, value.ScopeActivityType,
		value.ScopeSeriesID, value.MinimumOrderCents, value.ValidFrom,
		value.ExpiresAt, value.GrantKind, value.GrantBusinessKey,
		value.GrantOrdinal, value.PolicyVersion, value.SourcePeopleProfile,
		value.SourcePeopleBinding, value.SourceRoleBinding,
		value.SourceCheckin, value.SourceCheckinEvent, value.GrantedBy,
		value.GrantReason, value.GrantContext, value.GrantedAt,
		value.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf(`create xiangwan Coupon instrument: %w`, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(`read xiangwan Coupon insert result: %w`, err)
	}
	if affected != 1 {
		return ErrGrantExists
	}
	return nil
}

func (repository *Repository) createEntry(
	ctx context.Context,
	value coupon.Entry,
) error {
	if coupon.ValidateEntry(value) != nil {
		return coupon.ErrInvalidEntry
	}
	result, err := repository.db.ExecContext(ctx, `
INSERT INTO xiangwan_coupon_entries (
    id, tenant_id, coupon_id, principal_id, entry_type,
    business_key, actor_id, occurred_at, recorded_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT DO NOTHING
`,
		value.ID, value.TenantID, value.CouponID, value.PrincipalID,
		value.EntryType, value.BusinessKey, value.ActorID,
		value.OccurredAt, value.RecordedAt,
	)
	if err != nil {
		return fmt.Errorf(`create xiangwan Coupon grant entry: %w`, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf(`read xiangwan Coupon entry insert result: %w`, err)
	}
	if affected != 1 {
		return ErrGrantExists
	}
	return nil
}

const grantSelectSQL = `
SELECT
    instrument.id, instrument.tenant_id, instrument.principal_id,
    instrument.benefit_type, instrument.face_value_cents,
    instrument.scope_type, instrument.scope_activity_type,
    instrument.scope_series_id, instrument.minimum_order_cents,
    instrument.valid_from, instrument.expires_at, instrument.grant_kind,
    instrument.grant_business_key, instrument.grant_ordinal,
    instrument.policy_version, instrument.source_people_profile_id,
    instrument.source_people_binding_id, instrument.source_role_binding_id,
    instrument.source_checkin_id, instrument.source_checkin_event_id,
    instrument.granted_by, instrument.grant_reason, instrument.grant_context,
    instrument.granted_at, instrument.created_at,
    entry.id, entry.tenant_id, entry.coupon_id, entry.principal_id,
    entry.entry_type, entry.business_key, entry.actor_id,
    entry.occurred_at, entry.recorded_at
FROM xiangwan_coupons AS instrument
JOIN xiangwan_coupon_entries AS entry
  ON entry.tenant_id = instrument.tenant_id
 AND entry.principal_id = instrument.principal_id
 AND entry.coupon_id = instrument.id
 AND entry.entry_type = 'granted'
`

type rowsScanner interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}

func scanGrantRows(rows rowsScanner) (coupon.Grant, error) {
	result := coupon.Grant{
		Coupons: make([]coupon.Coupon, 0, coupon.GrantQuantity),
		Entries: make([]coupon.Entry, 0, coupon.GrantQuantity),
	}
	for rows.Next() {
		instrument, entry, err := scanGrantRow(rows)
		if err != nil {
			return coupon.Grant{}, fmt.Errorf(
				`scan xiangwan Coupon grant: %w`,
				err,
			)
		}
		if len(result.Coupons) == 0 {
			result.Kind = instrument.GrantKind
			result.BusinessKey = instrument.GrantBusinessKey
		}
		result.Coupons = append(result.Coupons, instrument)
		result.Entries = append(result.Entries, entry)
	}
	if err := rows.Err(); err != nil {
		return coupon.Grant{}, fmt.Errorf(
			`iterate xiangwan Coupon grant: %w`,
			err,
		)
	}
	if len(result.Coupons) == 0 {
		return coupon.Grant{}, ErrGrantNotFound
	}
	if coupon.ValidateGrant(result) != nil {
		return coupon.Grant{}, ErrGrantFactsConflict
	}
	return result, nil
}

func scanGrantRow(
	row interface{ Scan(...any) error },
) (coupon.Coupon, coupon.Entry, error) {
	var instrument coupon.Coupon
	var entry coupon.Entry
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
	var entryActor uuid.NullUUID
	err := row.Scan(
		&instrument.ID, &instrument.TenantID, &instrument.PrincipalID,
		&instrument.BenefitType, &instrument.FaceValueCents,
		&instrument.ScopeType, &scopeActivity, &scopeSeries,
		&instrument.MinimumOrderCents, &instrument.ValidFrom,
		&instrument.ExpiresAt, &instrument.GrantKind,
		&instrument.GrantBusinessKey, &instrument.GrantOrdinal,
		&instrument.PolicyVersion, &sourceProfile, &sourceBinding,
		&sourceRole, &sourceCheckin, &sourceEvent, &grantedBy,
		&reason, &grantContext, &instrument.GrantedAt,
		&instrument.CreatedAt, &entry.ID, &entry.TenantID,
		&entry.CouponID, &entry.PrincipalID, &entry.EntryType,
		&entry.BusinessKey, &entryActor, &entry.OccurredAt,
		&entry.RecordedAt,
	)
	if err != nil {
		return coupon.Coupon{}, coupon.Entry{}, err
	}
	entry.EntrySequence = 1
	if scopeActivity.Valid {
		value := activity.ActivityType(scopeActivity.String)
		instrument.ScopeActivityType = &value
	}
	instrument.ScopeSeriesID = nullableUUID(scopeSeries)
	instrument.SourcePeopleProfile = nullableUUID(sourceProfile)
	instrument.SourcePeopleBinding = nullableUUID(sourceBinding)
	instrument.SourceRoleBinding = nullableUUID(sourceRole)
	instrument.SourceCheckin = nullableUUID(sourceCheckin)
	instrument.SourceCheckinEvent = nullableUUID(sourceEvent)
	instrument.GrantedBy = nullableUUID(grantedBy)
	instrument.GrantReason = nullableString(reason)
	instrument.GrantContext = nullableString(grantContext)
	entry.ActorID = nullableUUID(entryActor)
	return instrument, entry, nil
}

func nullableUUID(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	result := value.UUID
	return &result
}

func nullableString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}
