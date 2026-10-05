package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrPaymentMerchantConfigUnavailable = errors.New(
	"xiangwan payment merchant configuration is unavailable",
)

const (
	merchantConfigProviderWeChat = "wechat"
	merchantConfigStatusActive   = "active"
	merchantConfigStatusDraining = "draining"
)

// requireMerchantConfigGeneration verifies the database-owned merchant
// configuration identity in the same transaction that will admit a provider
// operation. A zero expected ID is retained only for internal legacy callers
// such as the close worker; the enabled HTTP composition root always supplies
// the configured UUID.
func requireMerchantConfigGeneration(
	ctx context.Context,
	tx queryExecutor,
	tenantID uuid.UUID,
	generationID uuid.UUID,
	paymentAppID string,
	paymentMerchantID string,
	allowDraining bool,
) error {
	if generationID == uuid.Nil {
		return nil
	}
	if ctx == nil || tx == nil || tenantID == uuid.Nil ||
		paymentAppID == "" || paymentMerchantID == "" {
		return ErrPaymentMerchantConfigUnavailable
	}
	var provider string
	var configuredAppID string
	var configuredMerchantID string
	var status string
	var row rowScanner
	if allowDraining {
		row = tx.queryRowContext(ctx, `
SELECT provider, payment_app_id, payment_merchant_id, status
FROM xiangwan_payment_merchant_config_generations
WHERE tenant_id = $1
  AND id = $2
  AND provider = 'wechat'
  AND payment_app_id = $3
  AND payment_merchant_id = $4
  AND status IN ('active', 'draining')
FOR SHARE
`, tenantID, generationID, paymentAppID, paymentMerchantID)
	} else {
		row = tx.queryRowContext(ctx, `
SELECT provider, payment_app_id, payment_merchant_id, status
FROM xiangwan_payment_merchant_config_generations
WHERE tenant_id = $1
  AND id = $2
  AND provider = 'wechat'
  AND payment_app_id = $3
  AND payment_merchant_id = $4
  AND status = 'active'
FOR SHARE
`, tenantID, generationID, paymentAppID, paymentMerchantID)
	}
	err := row.Scan(
		&provider,
		&configuredAppID,
		&configuredMerchantID,
		&status,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPaymentMerchantConfigUnavailable
	}
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			(postgresError.Code == "42P01" || postgresError.Code == "42703") {
			return fmt.Errorf("%w: merchant configuration schema is unavailable", ErrPaymentMerchantConfigUnavailable)
		}
		return fmt.Errorf("%w: read xiangwan payment merchant configuration: %v", ErrPaymentMerchantConfigUnavailable, err)
	}
	if provider != merchantConfigProviderWeChat ||
		configuredAppID != paymentAppID ||
		configuredMerchantID != paymentMerchantID ||
		(status != merchantConfigStatusActive &&
			(!allowDraining || status != merchantConfigStatusDraining)) {
		return ErrPaymentMerchantConfigUnavailable
	}
	return nil
}

func requireOrderMerchantConfigGeneration(
	order payment.Order,
	expectedGenerationID uuid.UUID,
) error {
	if expectedGenerationID == uuid.Nil ||
		order.MerchantConfigGenerationID == expectedGenerationID {
		return nil
	}
	return ErrPaymentMerchantConfigUnavailable
}

func requireOrderPaymentIdentity(
	order payment.Order,
	expectedAppID string,
	expectedMerchantID string,
) error {
	if (expectedAppID == "" && expectedMerchantID == "") ||
		(order.PaymentAppID == expectedAppID && order.PaymentMerchantID == expectedMerchantID) {
		return nil
	}
	return ErrPaymentMerchantConfigUnavailable
}
