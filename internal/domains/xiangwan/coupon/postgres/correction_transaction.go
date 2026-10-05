package couponpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/google/uuid"
)

type sqlCorrectionTransactionStarter struct {
	db *sql.DB
}

func (starter sqlCorrectionTransactionStarter) beginCorrectionTx(
	ctx context.Context,
	options *sql.TxOptions,
) (correctionTransaction, error) {
	if starter.db == nil {
		return nil, ErrInvalidCorrectionCommand
	}
	tx, err := starter.db.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &sqlCorrectionTransaction{
		tx:         tx,
		repository: NewRepository(tx),
	}, nil
}

type sqlCorrectionTransaction struct {
	tx         *sql.Tx
	repository *Repository
}

func (tx *sqlCorrectionTransaction) authorizationQuery() CouponAuthorizationQuery {
	return tx.tx
}

func (tx *sqlCorrectionTransaction) lockRevokedCheckin(
	ctx context.Context,
	tenantID uuid.UUID,
	checkinID uuid.UUID,
) (revokedCouponSource, error) {
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
		return revokedCouponSource{}, ErrCorrectionSourceNotFound
	}
	if err != nil {
		return revokedCouponSource{}, fmt.Errorf(
			`lock xiangwan Coupon correction Checkin: %w`,
			err,
		)
	}
	revokedEvent, err := scanCheckinEvent(tx.tx.QueryRowContext(ctx, `
SELECT
    id, tenant_id, checkin_id, registration_id, session_id,
    event_sequence, event_type, idempotency_key, from_status, to_status,
    actor_id, reason, occurred_at, resulting_checkin_version, created_at
FROM xiangwan_checkin_events
WHERE tenant_id = $1
  AND checkin_id = $2
  AND event_type = 'revoked'
`, tenantID, checkinID))
	if errors.Is(err, sql.ErrNoRows) {
		return revokedCouponSource{}, ErrGrantFactsConflict
	}
	if err != nil {
		return revokedCouponSource{}, fmt.Errorf(
			`load xiangwan Coupon revocation event: %w`,
			err,
		)
	}
	return revokedCouponSource{
		Checkin:      current,
		RevokedEvent: revokedEvent,
	}, nil
}

func (tx *sqlCorrectionTransaction) listSourceCouponIDsForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	checkinID uuid.UUID,
) ([]uuid.UUID, error) {
	rows, err := tx.tx.QueryContext(ctx, `
SELECT id
FROM xiangwan_coupons
WHERE tenant_id = $1
  AND principal_id = $2
  AND source_checkin_id = $3
  AND grant_kind = 'initial_guest'
ORDER BY grant_ordinal
FOR UPDATE
`, tenantID, principalID, checkinID)
	if err != nil {
		return nil, fmt.Errorf(
			`lock xiangwan Coupon correction instruments: %w`,
			err,
		)
	}
	defer rows.Close()
	result := make([]uuid.UUID, 0, coupon.GrantQuantity)
	for rows.Next() {
		var couponID uuid.UUID
		if err := rows.Scan(&couponID); err != nil {
			return nil, fmt.Errorf(
				`scan xiangwan Coupon correction instrument: %w`,
				err,
			)
		}
		if couponID == uuid.Nil {
			return nil, ErrGrantFactsConflict
		}
		result = append(result, couponID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			`iterate xiangwan Coupon correction instruments: %w`,
			err,
		)
	}
	return result, nil
}

func (tx *sqlCorrectionTransaction) getLedgerForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
	couponID uuid.UUID,
) (Ledger, error) {
	return tx.repository.GetLedgerForUpdate(
		ctx,
		tenantID,
		principalID,
		couponID,
	)
}

func (tx *sqlCorrectionTransaction) appendLifecycleEntry(
	ctx context.Context,
	value coupon.Entry,
) (coupon.Entry, error) {
	return tx.repository.AppendLifecycleEntry(ctx, value)
}

func (tx *sqlCorrectionTransaction) Commit() error {
	return tx.tx.Commit()
}

func (tx *sqlCorrectionTransaction) Rollback() error {
	return tx.tx.Rollback()
}
