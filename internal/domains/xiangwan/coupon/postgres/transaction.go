package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/checkin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/google/uuid"
)

type sqlGrantTransactionStarter struct {
	db *sql.DB
}

func (starter sqlGrantTransactionStarter) beginGrantTx(
	ctx context.Context,
	options *sql.TxOptions,
) (grantTransaction, error) {
	if starter.db == nil {
		return nil, ErrInvalidGrantCommand
	}
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlGrantTransaction{
		tx:         tx,
		repository: NewRepository(tx),
	}, nil
}

type sqlGrantTransaction struct {
	tx         *sql.Tx
	repository *Repository
}

func (tx *sqlGrantTransaction) authorizationQuery() CouponAuthorizationQuery {
	return tx.tx
}

func (tx *sqlGrantTransaction) loadCouponOwner(
	ctx context.Context, tenantID, couponID uuid.UUID,
) (uuid.UUID, error) {
	var principalID uuid.UUID
	err := tx.tx.QueryRowContext(ctx, `
SELECT principal_id
FROM xiangwan_coupons
WHERE tenant_id = $1 AND id = $2
  AND benefit_type = 'roundtable_coupon'
`, tenantID, couponID).Scan(&principalID)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, ErrGrantSourceNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("load source Coupon owner: %w", err)
	}
	return principalID, nil
}

func (tx *sqlGrantTransaction) auditManualGrant(
	ctx context.Context, command ManualReplenishmentCommand,
	principalID uuid.UUID, grant coupon.Grant,
) error {
	if len(grant.Coupons) != coupon.GrantQuantity {
		return ErrGrantFactsConflict
	}
	requestID := requestctx.RequestID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	_, err := tx.tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1,$2,$3,'coupon.manual_replenished','coupon',$4,$5,
    jsonb_build_object('identity_link_id',$6::TEXT,'principal_id',$7::TEXT,
        'business_key',$8::TEXT,'policy_version',$9::TEXT,'quantity',$10::INTEGER),
    $11,$11)
`, uuid.New(), command.TenantID, command.ActorID, command.SourceCouponID,
		requestID, command.IdentityLinkID.String(), principalID.String(),
		command.BusinessKey, grant.Coupons[0].PolicyVersion,
		len(grant.Coupons), grant.Coupons[0].GrantedAt)
	if err != nil {
		return fmt.Errorf("audit manual Coupon replenishment: %w", err)
	}
	return nil
}

func (tx *sqlGrantTransaction) lockInitialGuestSource(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
) (initialGuestSource, error) {
	current, err := scanCheckin(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, registration_id, series_id, instance_id, session_id,
    principal_id, checkin_status, checked_in_by, checked_in_at,
    revoked_by, revoked_at, revocation_reason, version, created_at, updated_at
FROM xiangwan_checkins
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, checkinID))
	if errors.Is(err, sql.ErrNoRows) {
		return initialGuestSource{}, ErrGrantSourceNotFound
	}
	if err != nil {
		return initialGuestSource{}, fmt.Errorf(
			`lock xiangwan Coupon Checkin source: %w`,
			err,
		)
	}
	checkedInEvent, err := scanCheckinEvent(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, checkin_id, registration_id, session_id,
    event_sequence, event_type, idempotency_key, from_status, to_status,
    actor_id, reason, occurred_at, resulting_checkin_version, created_at
FROM xiangwan_checkin_events
WHERE tenant_id = $1
  AND checkin_id = $2
  AND event_type = 'checked_in'
  AND event_sequence = 1
`, tenantID, checkinID))
	if errors.Is(err, sql.ErrNoRows) {
		return initialGuestSource{}, ErrGrantFactsConflict
	}
	if err != nil {
		return initialGuestSource{}, fmt.Errorf(
			`load xiangwan Coupon Checkin event: %w`,
			err,
		)
	}
	result := initialGuestSource{
		Checkin:        current,
		CheckedInEvent: checkedInEvent,
	}
	facts, err := tx.loadInitialGuestFacts(ctx, current, checkedInEvent)
	if err != nil {
		return initialGuestSource{}, err
	}
	if facts != nil {
		result.Eligible = true
		result.Facts = *facts
	}
	return result, nil
}

func (tx *sqlGrantTransaction) loadInitialGuestFacts(
	ctx context.Context,
	current checkin.Checkin,
	event checkin.Event,
) (*coupon.SourceFacts, error) {
	rows, err := tx.tx.QueryContext(ctx, `
SELECT
    people_binding.people_profile_id,
    people_binding.id,
    role_binding.id
FROM xiangwan_people_bindings AS people_binding
JOIN xiangwan_instance_role_bindings AS role_binding
  ON role_binding.tenant_id = people_binding.tenant_id
 AND role_binding.principal_id = people_binding.principal_id
 AND role_binding.series_id = $3
 AND role_binding.instance_id = $4
 AND role_binding.role_code = 'invited_guest'
 AND role_binding.granted_at <= $5
 AND (
     role_binding.revoked_at IS NULL
     OR role_binding.revoked_at > $5
 )
WHERE people_binding.tenant_id = $1
  AND people_binding.principal_id = $2
  AND people_binding.bound_at <= $5
  AND (
      people_binding.revoked_at IS NULL
      OR people_binding.revoked_at > $5
  )
ORDER BY people_binding.id, role_binding.id
LIMIT 2
`,
		current.TenantID,
		current.PrincipalID,
		current.SeriesID,
		current.InstanceID,
		event.OccurredAt,
	)
	if err != nil {
		return nil, fmt.Errorf(
			`load xiangwan Coupon invited-guest facts: %w`,
			err,
		)
	}
	defer rows.Close()
	var result *coupon.SourceFacts
	for rows.Next() {
		if result != nil {
			return nil, ErrGrantFactsConflict
		}
		value := &coupon.SourceFacts{
			CheckinID:      current.ID,
			CheckinEventID: event.ID,
		}
		if err := rows.Scan(
			&value.PeopleProfileID,
			&value.PeopleBindingID,
			&value.RoleBindingID,
		); err != nil {
			return nil, fmt.Errorf(
				`scan xiangwan Coupon invited-guest facts: %w`,
				err,
			)
		}
		result = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			`iterate xiangwan Coupon invited-guest facts: %w`,
			err,
		)
	}
	return result, nil
}

func (tx *sqlGrantTransaction) lockPrincipal(
	ctx context.Context,
	principalID uuid.UUID,
) error {
	var lockedID uuid.UUID
	err := tx.tx.QueryRowContext(ctx, `
SELECT id
FROM principals
WHERE id = $1
FOR UPDATE
`, principalID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrGrantSourceNotFound
	}
	if err != nil {
		return fmt.Errorf(`lock xiangwan Coupon principal: %w`, err)
	}
	return nil
}

func (tx *sqlGrantTransaction) getGrantForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	kind coupon.GrantKind,
	businessKey string,
) (coupon.Grant, error) {
	return tx.repository.GetGrantForUpdate(
		ctx,
		tenantID,
		principalID,
		kind,
		businessKey,
	)
}

func (tx *sqlGrantTransaction) currentGrantState(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	at time.Time,
) (GrantState, error) {
	return tx.repository.CurrentGrantState(ctx, tenantID, principalID, at)
}

func (tx *sqlGrantTransaction) createGrant(
	ctx context.Context,
	value coupon.Grant,
) (coupon.Grant, error) {
	return tx.repository.CreateGrant(ctx, value)
}

func (tx *sqlGrantTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlGrantTransaction) Rollback() error {
	return tx.tx.Rollback()
}

type rowScanner interface {
	Scan(...any) error
}

func scanCheckin(row rowScanner) (checkin.Checkin, error) {
	var value checkin.Checkin
	var revokedBy uuid.NullUUID
	var revokedAt sql.NullTime
	var revocationReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.RegistrationID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PrincipalID,
		&value.CheckinStatus,
		&value.CheckedInBy,
		&value.CheckedInAt,
		&revokedBy,
		&revokedAt,
		&revocationReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return checkin.Checkin{}, err
	}
	if revokedBy.Valid {
		value.RevokedBy = &revokedBy.UUID
	}
	if revokedAt.Valid {
		value.RevokedAt = &revokedAt.Time
	}
	if revocationReason.Valid {
		value.RevocationReason = &revocationReason.String
	}
	return value, nil
}

func scanCheckinEvent(row rowScanner) (checkin.Event, error) {
	var value checkin.Event
	var fromStatus sql.NullString
	var reason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.CheckinID,
		&value.RegistrationID,
		&value.SessionID,
		&value.EventSequence,
		&value.EventType,
		&value.IdempotencyKey,
		&fromStatus,
		&value.ToStatus,
		&value.ActorID,
		&reason,
		&value.OccurredAt,
		&value.ResultingCheckinVersion,
		&value.CreatedAt,
	)
	if err != nil {
		return checkin.Event{}, err
	}
	if fromStatus.Valid {
		status := checkin.Status(fromStatus.String)
		value.FromStatus = &status
	}
	if reason.Valid {
		value.Reason = &reason.String
	}
	return value, nil
}
