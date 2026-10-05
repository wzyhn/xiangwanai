package xiangwanruntime

import (
	"strings"

	"github.com/google/uuid"
)

const CouponReconciliationDatabaseDSNEnv = "XIANGWAN_COUPON_RECONCILIATION_DATABASE_DSN"

// The Coupon worker needs no HTTP, OIDC, WeChat or customer signing key. Its
// PostgreSQL role is limited to generation, Checkin/People/policy reads and
// Coupon/Contribution ledger inserts and Checkin correction outbox updates.
type CouponReconciliationWorkerConfig struct {
	DatabaseDSN  string
	TenantID     uuid.UUID
	GenerationID uuid.UUID
}

func LoadCouponReconciliationWorkerConfig(
	lookup EnvironmentLookup,
) (CouponReconciliationWorkerConfig, error) {
	if lookup == nil {
		return CouponReconciliationWorkerConfig{}, ErrInvalidRuntimeConfig
	}
	dsn, err := loadMediaCleanupValue(lookup, CouponReconciliationDatabaseDSNEnv)
	if err != nil {
		return CouponReconciliationWorkerConfig{}, err
	}
	tenantID, err := loadCanonicalUUID(lookup, TenantIDEnv)
	if err != nil {
		return CouponReconciliationWorkerConfig{}, err
	}
	generationID, err := loadCanonicalUUID(lookup, GenerationIDEnv)
	if err != nil {
		return CouponReconciliationWorkerConfig{}, err
	}
	return CouponReconciliationWorkerConfig{
		DatabaseDSN: dsn, TenantID: tenantID, GenerationID: generationID,
	}, nil
}

func validCouponReconciliationWorkerConfig(config CouponReconciliationWorkerConfig) bool {
	return config.DatabaseDSN != "" &&
		config.DatabaseDSN == strings.TrimSpace(config.DatabaseDSN) &&
		!strings.ContainsAny(config.DatabaseDSN, "\r\n\x00") &&
		config.TenantID != uuid.Nil && config.GenerationID != uuid.Nil
}
