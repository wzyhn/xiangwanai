package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

func TestRequireMerchantConfigGenerationAdmitsActiveAndDraining(t *testing.T) {
	tenantID := uuid.New()
	generationID := uuid.New()
	for _, test := range []struct {
		name          string
		status        string
		allowDraining bool
	}{
		{name: "active", status: merchantConfigStatusActive},
		{name: "draining", status: merchantConfigStatusDraining, allowDraining: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var query string
			executor := &fakeQueryExecutor{queryRow: func(value string, _ ...any) rowScanner {
				query = value
				return &fakeRow{values: []any{
					merchantConfigProviderWeChat,
					"wx-xiangwan",
					"1900000109",
					test.status,
				}}
			}}
			if err := requireMerchantConfigGeneration(
				context.Background(),
				executor,
				tenantID,
				generationID,
				"wx-xiangwan",
				"1900000109",
				test.allowDraining,
			); err != nil {
				t.Fatalf("requireMerchantConfigGeneration() error = %v", err)
			}
			for _, fragment := range []string{
				"provider = 'wechat'",
				"payment_app_id = $3",
				"payment_merchant_id = $4",
				"FOR SHARE",
			} {
				if !strings.Contains(query, fragment) {
					t.Fatalf("metadata query missing %q: %s", fragment, query)
				}
			}
		})
	}
}

func TestRequireMerchantConfigGenerationFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		values []any
		err    error
	}{
		{
			name: "missing row",
			err:  sql.ErrNoRows,
		},
		{
			name:   "wrong provider",
			values: []any{"alipay", "wx-xiangwan", "1900000109", merchantConfigStatusActive},
		},
		{
			name:   "wrong identity",
			values: []any{merchantConfigProviderWeChat, "wx-other", "1900000109", merchantConfigStatusActive},
		},
		{
			name:   "revoked",
			values: []any{merchantConfigProviderWeChat, "wx-xiangwan", "1900000109", "revoked"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			executor := &fakeQueryExecutor{queryRow: func(_ string, _ ...any) rowScanner {
				return &fakeRow{values: test.values, err: test.err}
			}}
			if err := requireMerchantConfigGeneration(
				context.Background(),
				executor,
				uuid.New(),
				uuid.New(),
				"wx-xiangwan",
				"1900000109",
				true,
			); !errors.Is(err, ErrPaymentMerchantConfigUnavailable) {
				t.Fatalf("requireMerchantConfigGeneration() error = %v, want unavailable", err)
			}
		})
	}
}

func TestRequireOrderMerchantConfigGenerationRejectsLegacyAndMismatch(t *testing.T) {
	expected := uuid.New()
	for _, test := range []struct {
		name       string
		configured uuid.UUID
		wantError  bool
	}{
		{name: "exact", configured: expected},
		{name: "legacy null", configured: uuid.Nil, wantError: true},
		{name: "different", configured: uuid.New(), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := requireOrderMerchantConfigGeneration(payment.Order{
				MerchantConfigGenerationID: test.configured,
			}, expected)
			if test.wantError && !errors.Is(err, ErrPaymentMerchantConfigUnavailable) {
				t.Fatalf("error = %v, want unavailable", err)
			}
			if !test.wantError && err != nil {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
