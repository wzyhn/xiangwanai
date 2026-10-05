// Package paymentpostgres persists Xiangwan Order payment facts and capacity
// holds in the customer PostgreSQL database.
package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

var (
	ErrOrderNotFound               = errors.New("xiangwan Order not found")
	ErrOrderVersionConflict        = errors.New("xiangwan Order version conflict")
	ErrCapacityHoldNotFound        = errors.New("xiangwan capacity hold not found")
	ErrCapacityHoldVersionConflict = errors.New("xiangwan capacity hold version conflict")
)

type DBTX interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Repository struct {
	db queryExecutor
}

func NewRepository(db DBTX) *Repository {
	return &Repository{db: sqlQueryExecutor{db: db}}
}

type rowScanner interface {
	Scan(...any) error
}

type queryExecutor interface {
	queryRowContext(context.Context, string, ...any) rowScanner
}

type sqlQueryExecutor struct {
	db DBTX
}

func (executor sqlQueryExecutor) queryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) rowScanner {
	return executor.db.QueryRowContext(ctx, query, args...)
}

func (repository *Repository) CreateOrder(
	ctx context.Context,
	value payment.Order,
) (payment.Order, error) {
	created, err := scanOrder(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_orders (
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17, $18, $19, $20,
    $21, $22, $23
)
RETURNING
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
`,
		value.ID,
		value.TenantID,
		value.RegistrationID,
		value.SeriesID,
		value.InstanceID,
		value.SessionID,
		value.PrincipalID,
		value.PaymentStatus,
		value.IdempotencyKey,
		value.MerchantOrderNo,
		value.PaymentAppID,
		value.PaymentMerchantID,
		nullableUUID(value.MerchantConfigGenerationID),
		value.OriginalPriceCents,
		value.DiscountCents,
		value.PayableCents,
		value.ActualPaidCents,
		value.WeChatTransactionID,
		value.PaidAt,
		value.ClosedAt,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return payment.Order{}, fmt.Errorf("create xiangwan Order: %w", err)
	}
	return created, nil
}

func (repository *Repository) GetOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (payment.Order, error) {
	return repository.getOrder(ctx, `
SELECT
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
FROM xiangwan_orders
WHERE tenant_id = $1 AND id = $2
`, tenantID, orderID)
}

func (repository *Repository) GetOrderForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (payment.Order, error) {
	return repository.getOrder(ctx, `
SELECT
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
FROM xiangwan_orders
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, tenantID, orderID)
}

func (repository *Repository) GetOrderByIdempotencyKey(
	ctx context.Context,
	tenantID uuid.UUID,
	idempotencyKey string,
) (payment.Order, error) {
	return repository.getOrder(ctx, `
SELECT
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
FROM xiangwan_orders
WHERE tenant_id = $1 AND idempotency_key = $2
`, tenantID, idempotencyKey)
}

func (repository *Repository) GetOrderByRegistration(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (payment.Order, error) {
	return repository.getOrder(ctx, `
SELECT
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
FROM xiangwan_orders
WHERE tenant_id = $1 AND registration_id = $2
`, tenantID, registrationID)
}

func (repository *Repository) GetOrderByRegistrationForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	registrationID uuid.UUID,
) (payment.Order, error) {
	return repository.getOrder(ctx, `
SELECT
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
FROM xiangwan_orders
WHERE tenant_id = $1 AND registration_id = $2
FOR UPDATE
`, tenantID, registrationID)
}

func (repository *Repository) GetOrderByMerchantIdentity(
	ctx context.Context,
	tenantID uuid.UUID,
	paymentAppID string,
	paymentMerchantID string,
	merchantOrderNo string,
) (payment.Order, error) {
	return repository.getOrderByMerchantIdentity(
		ctx,
		tenantID,
		paymentAppID,
		paymentMerchantID,
		merchantOrderNo,
		false,
	)
}

func (repository *Repository) GetOrderByMerchantIdentityForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	paymentAppID string,
	paymentMerchantID string,
	merchantOrderNo string,
) (payment.Order, error) {
	return repository.getOrderByMerchantIdentity(
		ctx,
		tenantID,
		paymentAppID,
		paymentMerchantID,
		merchantOrderNo,
		true,
	)
}

func (repository *Repository) UpdateOrderPayment(
	ctx context.Context,
	value payment.Order,
	expectedVersion int64,
) (payment.Order, error) {
	updated, err := scanOrder(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_orders
SET payment_status = $3,
    actual_paid_cents = $4,
    wechat_transaction_id = $5,
    paid_at = $6,
    closed_at = $7,
    version = version + 1,
    updated_at = $8
WHERE tenant_id = $1
  AND id = $2
  AND version = $9
RETURNING
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
`,
		value.TenantID,
		value.ID,
		value.PaymentStatus,
		value.ActualPaidCents,
		value.WeChatTransactionID,
		value.PaidAt,
		value.ClosedAt,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.Order{}, ErrOrderVersionConflict
	}
	if err != nil {
		return payment.Order{}, fmt.Errorf("update xiangwan Order payment: %w", err)
	}
	return updated, nil
}

func (repository *Repository) CreateCapacityHold(
	ctx context.Context,
	value payment.CapacityHold,
) (payment.CapacityHold, error) {
	created, err := scanCapacityHold(repository.db.queryRowContext(ctx, `
INSERT INTO xiangwan_capacity_holds (
    id, tenant_id, order_id, registration_id, session_id,
    hold_status, expires_at, converted_at, released_at, release_reason,
    version, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    $11, $12, $13
)
RETURNING
    id, tenant_id, order_id, registration_id, session_id,
    hold_status, expires_at, converted_at, released_at, release_reason,
    version, created_at, updated_at
`,
		value.ID,
		value.TenantID,
		value.OrderID,
		value.RegistrationID,
		value.SessionID,
		value.HoldStatus,
		value.ExpiresAt,
		value.ConvertedAt,
		value.ReleasedAt,
		value.ReleaseReason,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	))
	if err != nil {
		return payment.CapacityHold{}, fmt.Errorf("create xiangwan capacity hold: %w", err)
	}
	return created, nil
}

func (repository *Repository) GetCapacityHold(
	ctx context.Context,
	tenantID uuid.UUID,
	holdID uuid.UUID,
) (payment.CapacityHold, error) {
	return repository.getCapacityHold(ctx, `
SELECT
    id, tenant_id, order_id, registration_id, session_id,
    hold_status, expires_at, converted_at, released_at, release_reason,
    version, created_at, updated_at
FROM xiangwan_capacity_holds
WHERE tenant_id = $1 AND id = $2
`, tenantID, holdID)
}

func (repository *Repository) GetCapacityHoldByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (payment.CapacityHold, error) {
	return repository.getCapacityHoldByOrder(ctx, tenantID, orderID, false)
}

func (repository *Repository) GetCapacityHoldByOrderForUpdate(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
) (payment.CapacityHold, error) {
	return repository.getCapacityHoldByOrder(ctx, tenantID, orderID, true)
}

func (repository *Repository) UpdateCapacityHold(
	ctx context.Context,
	value payment.CapacityHold,
	expectedVersion int64,
) (payment.CapacityHold, error) {
	updated, err := scanCapacityHold(repository.db.queryRowContext(ctx, `
UPDATE xiangwan_capacity_holds
SET hold_status = $3,
    converted_at = $4,
    released_at = $5,
    release_reason = $6,
    version = version + 1,
    updated_at = $7
WHERE tenant_id = $1
  AND id = $2
  AND version = $8
RETURNING
    id, tenant_id, order_id, registration_id, session_id,
    hold_status, expires_at, converted_at, released_at, release_reason,
    version, created_at, updated_at
`,
		value.TenantID,
		value.ID,
		value.HoldStatus,
		value.ConvertedAt,
		value.ReleasedAt,
		value.ReleaseReason,
		value.UpdatedAt,
		expectedVersion,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.CapacityHold{}, ErrCapacityHoldVersionConflict
	}
	if err != nil {
		return payment.CapacityHold{}, fmt.Errorf("update xiangwan capacity hold: %w", err)
	}
	return updated, nil
}

func (repository *Repository) getOrderByMerchantIdentity(
	ctx context.Context,
	tenantID uuid.UUID,
	paymentAppID string,
	paymentMerchantID string,
	merchantOrderNo string,
	forUpdate bool,
) (payment.Order, error) {
	query := `
SELECT
    id, tenant_id, registration_id, series_id, instance_id, session_id, principal_id,
    payment_status, idempotency_key, merchant_order_no, payment_app_id,
    payment_merchant_id, merchant_config_generation_id,
    original_price_cents, discount_cents, payable_cents,
    actual_paid_cents, wechat_transaction_id, paid_at, closed_at,
    version, created_at, updated_at
FROM xiangwan_orders
WHERE tenant_id = $1
  AND payment_app_id = $2
  AND payment_merchant_id = $3
  AND merchant_order_no = $4
`
	if forUpdate {
		query += "FOR UPDATE\n"
	}
	return repository.getOrder(
		ctx,
		query,
		tenantID,
		paymentAppID,
		paymentMerchantID,
		merchantOrderNo,
	)
}

func (repository *Repository) getOrder(
	ctx context.Context,
	query string,
	args ...any,
) (payment.Order, error) {
	value, err := scanOrder(repository.db.queryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.Order{}, ErrOrderNotFound
	}
	if err != nil {
		return payment.Order{}, fmt.Errorf("get xiangwan Order: %w", err)
	}
	return value, nil
}

func (repository *Repository) getCapacityHoldByOrder(
	ctx context.Context,
	tenantID uuid.UUID,
	orderID uuid.UUID,
	forUpdate bool,
) (payment.CapacityHold, error) {
	query := `
SELECT
    id, tenant_id, order_id, registration_id, session_id,
    hold_status, expires_at, converted_at, released_at, release_reason,
    version, created_at, updated_at
FROM xiangwan_capacity_holds
WHERE tenant_id = $1 AND order_id = $2
`
	if forUpdate {
		query += "FOR UPDATE\n"
	}
	return repository.getCapacityHold(ctx, query, tenantID, orderID)
}

func (repository *Repository) getCapacityHold(
	ctx context.Context,
	query string,
	args ...any,
) (payment.CapacityHold, error) {
	value, err := scanCapacityHold(repository.db.queryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return payment.CapacityHold{}, ErrCapacityHoldNotFound
	}
	if err != nil {
		return payment.CapacityHold{}, fmt.Errorf("get xiangwan capacity hold: %w", err)
	}
	return value, nil
}

func scanOrder(row rowScanner) (payment.Order, error) {
	var value payment.Order
	var actualPaidCents sql.NullInt64
	var merchantConfigGenerationID uuid.NullUUID
	var weChatTransactionID sql.NullString
	var paidAt sql.NullTime
	var closedAt sql.NullTime
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.RegistrationID,
		&value.SeriesID,
		&value.InstanceID,
		&value.SessionID,
		&value.PrincipalID,
		&value.PaymentStatus,
		&value.IdempotencyKey,
		&value.MerchantOrderNo,
		&value.PaymentAppID,
		&value.PaymentMerchantID,
		&merchantConfigGenerationID,
		&value.OriginalPriceCents,
		&value.DiscountCents,
		&value.PayableCents,
		&actualPaidCents,
		&weChatTransactionID,
		&paidAt,
		&closedAt,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return payment.Order{}, err
	}
	value.ActualPaidCents = nullInt64Pointer(actualPaidCents)
	if merchantConfigGenerationID.Valid {
		value.MerchantConfigGenerationID = merchantConfigGenerationID.UUID
	}
	value.WeChatTransactionID = nullStringPointer(weChatTransactionID)
	value.PaidAt = nullTimePointer(paidAt)
	value.ClosedAt = nullTimePointer(closedAt)
	return value, nil
}

func nullableUUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}

func scanCapacityHold(row rowScanner) (payment.CapacityHold, error) {
	var value payment.CapacityHold
	var convertedAt sql.NullTime
	var releasedAt sql.NullTime
	var releaseReason sql.NullString
	err := row.Scan(
		&value.ID,
		&value.TenantID,
		&value.OrderID,
		&value.RegistrationID,
		&value.SessionID,
		&value.HoldStatus,
		&value.ExpiresAt,
		&convertedAt,
		&releasedAt,
		&releaseReason,
		&value.Version,
		&value.CreatedAt,
		&value.UpdatedAt,
	)
	if err != nil {
		return payment.CapacityHold{}, err
	}
	value.ConvertedAt = nullTimePointer(convertedAt)
	value.ReleasedAt = nullTimePointer(releasedAt)
	value.ReleaseReason = nullStringPointer(releaseReason)
	return value, nil
}

func nullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}
