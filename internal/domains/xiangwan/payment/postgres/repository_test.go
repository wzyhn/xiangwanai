package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

func TestCreateOrderPersistsCompletePaymentFact(t *testing.T) {
	t.Parallel()

	want := pendingOrder(time.Date(2026, time.September, 12, 5, 0, 0, 0, time.UTC))
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: orderScanValues(want)}
		},
	}}

	got, err := repository.CreateOrder(context.Background(), want)
	if err != nil {
		t.Fatalf("CreateOrder() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CreateOrder() = %+v, want %+v", got, want)
	}
	if !strings.Contains(capturedQuery, "INSERT INTO xiangwan_orders") || len(capturedArgs) != 23 {
		t.Fatalf("CreateOrder query/args = %s %#v", capturedQuery, capturedArgs)
	}
	if capturedArgs[0] != want.ID ||
		capturedArgs[2] != want.RegistrationID ||
		capturedArgs[7] != payment.OrderStatusPending ||
		capturedArgs[15] != want.PayableCents ||
		capturedArgs[20] != int64(1) {
		t.Fatalf("CreateOrder args = %#v", capturedArgs)
	}
}

func TestOrderGetMethodsUseStableTenantScopesAndLocks(t *testing.T) {
	t.Parallel()

	want := pendingOrder(time.Now().UTC())
	tests := []struct {
		name          string
		call          func(*Repository) (payment.Order, error)
		wantFragments []string
		wantArgs      []any
	}{
		{
			name: "identity",
			call: func(repository *Repository) (payment.Order, error) {
				return repository.GetOrder(context.Background(), want.TenantID, want.ID)
			},
			wantFragments: []string{"tenant_id = $1 AND id = $2"},
			wantArgs:      []any{want.TenantID, want.ID},
		},
		{
			name: "identity lock",
			call: func(repository *Repository) (payment.Order, error) {
				return repository.GetOrderForUpdate(context.Background(), want.TenantID, want.ID)
			},
			wantFragments: []string{"tenant_id = $1 AND id = $2", "FOR UPDATE"},
			wantArgs:      []any{want.TenantID, want.ID},
		},
		{
			name: "idempotency",
			call: func(repository *Repository) (payment.Order, error) {
				return repository.GetOrderByIdempotencyKey(
					context.Background(),
					want.TenantID,
					want.IdempotencyKey,
				)
			},
			wantFragments: []string{"tenant_id = $1 AND idempotency_key = $2"},
			wantArgs:      []any{want.TenantID, want.IdempotencyKey},
		},
		{
			name: "Registration identity",
			call: func(repository *Repository) (payment.Order, error) {
				return repository.GetOrderByRegistration(
					context.Background(),
					want.TenantID,
					want.RegistrationID,
				)
			},
			wantFragments: []string{"tenant_id = $1 AND registration_id = $2"},
			wantArgs:      []any{want.TenantID, want.RegistrationID},
		},
		{
			name: "Registration identity lock",
			call: func(repository *Repository) (payment.Order, error) {
				return repository.GetOrderByRegistrationForUpdate(
					context.Background(),
					want.TenantID,
					want.RegistrationID,
				)
			},
			wantFragments: []string{"tenant_id = $1 AND registration_id = $2", "FOR UPDATE"},
			wantArgs:      []any{want.TenantID, want.RegistrationID},
		},
		{
			name: "merchant identity",
			call: func(repository *Repository) (payment.Order, error) {
				return repository.GetOrderByMerchantIdentity(
					context.Background(),
					want.TenantID,
					want.PaymentAppID,
					want.PaymentMerchantID,
					want.MerchantOrderNo,
				)
			},
			wantFragments: []string{
				"tenant_id = $1",
				"payment_app_id = $2",
				"payment_merchant_id = $3",
				"merchant_order_no = $4",
			},
			wantArgs: []any{
				want.TenantID,
				want.PaymentAppID,
				want.PaymentMerchantID,
				want.MerchantOrderNo,
			},
		},
		{
			name: "merchant identity lock",
			call: func(repository *Repository) (payment.Order, error) {
				return repository.GetOrderByMerchantIdentityForUpdate(
					context.Background(),
					want.TenantID,
					want.PaymentAppID,
					want.PaymentMerchantID,
					want.MerchantOrderNo,
				)
			},
			wantFragments: []string{
				"tenant_id = $1",
				"payment_app_id = $2",
				"payment_merchant_id = $3",
				"merchant_order_no = $4",
				"FOR UPDATE",
			},
			wantArgs: []any{
				want.TenantID,
				want.PaymentAppID,
				want.PaymentMerchantID,
				want.MerchantOrderNo,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var capturedQuery string
			var capturedArgs []any
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(query string, args ...any) rowScanner {
					capturedQuery = query
					capturedArgs = append([]any(nil), args...)
					return &fakeRow{values: orderScanValues(want)}
				},
			}}
			got, err := test.call(repository)
			if err != nil {
				t.Fatalf("Order get error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Order get = %+v, want %+v", got, want)
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(capturedQuery, fragment) {
					t.Fatalf("query %q does not contain %q", capturedQuery, fragment)
				}
			}
			if !reflect.DeepEqual(capturedArgs, test.wantArgs) {
				t.Fatalf("query args = %#v, want %#v", capturedArgs, test.wantArgs)
			}
		})
	}
}

func TestUpdateOrderPaymentUsesOptimisticVersion(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	current := pendingOrder(now)
	paidAt := now.Add(time.Minute)
	paidCents := current.PayableCents
	transactionID := "wx-transaction-1"
	updated := current
	updated.PaymentStatus = payment.OrderStatusPaidConfirmed
	updated.ActualPaidCents = &paidCents
	updated.WeChatTransactionID = &transactionID
	updated.PaidAt = &paidAt
	updated.Version = 2
	updated.UpdatedAt = paidAt

	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: orderScanValues(updated)}
		},
	}}
	got, err := repository.UpdateOrderPayment(context.Background(), updated, current.Version)
	if err != nil {
		t.Fatalf("UpdateOrderPayment() error = %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("UpdateOrderPayment() = %+v, want %+v", got, updated)
	}
	if !strings.Contains(capturedQuery, "version = version + 1") ||
		!strings.Contains(capturedQuery, "AND version = $9") ||
		capturedArgs[0] != updated.TenantID ||
		capturedArgs[1] != updated.ID ||
		capturedArgs[7] != updated.UpdatedAt ||
		capturedArgs[8] != int64(1) {
		t.Fatalf("UpdateOrderPayment query/args = %s %#v", capturedQuery, capturedArgs)
	}

	conflictRepository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner { return &fakeRow{err: sql.ErrNoRows} },
	}}
	if _, err := conflictRepository.UpdateOrderPayment(
		context.Background(),
		updated,
		current.Version,
	); !errors.Is(err, ErrOrderVersionConflict) {
		t.Fatalf("UpdateOrderPayment(conflict) error = %v", err)
	}
}

func TestCreateAndGetCapacityHold(t *testing.T) {
	t.Parallel()

	want := activeHold(time.Now().UTC())
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: holdScanValues(want)}
		},
	}}

	got, err := repository.CreateCapacityHold(context.Background(), want)
	if err != nil {
		t.Fatalf("CreateCapacityHold() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CreateCapacityHold() = %+v, want %+v", got, want)
	}
	if !strings.Contains(capturedQuery, "INSERT INTO xiangwan_capacity_holds") ||
		len(capturedArgs) != 13 ||
		capturedArgs[0] != want.ID ||
		capturedArgs[2] != want.OrderID ||
		capturedArgs[5] != payment.CapacityHoldStatusActive ||
		capturedArgs[10] != int64(1) {
		t.Fatalf("CreateCapacityHold query/args = %s %#v", capturedQuery, capturedArgs)
	}
}

func TestCapacityHoldGetMethodsUseStableTenantScopesAndLocks(t *testing.T) {
	t.Parallel()

	want := activeHold(time.Now().UTC())
	tests := []struct {
		name          string
		call          func(*Repository) (payment.CapacityHold, error)
		wantFragments []string
		wantArgs      []any
	}{
		{
			name: "identity",
			call: func(repository *Repository) (payment.CapacityHold, error) {
				return repository.GetCapacityHold(context.Background(), want.TenantID, want.ID)
			},
			wantFragments: []string{"tenant_id = $1 AND id = $2"},
			wantArgs:      []any{want.TenantID, want.ID},
		},
		{
			name: "Order identity",
			call: func(repository *Repository) (payment.CapacityHold, error) {
				return repository.GetCapacityHoldByOrder(context.Background(), want.TenantID, want.OrderID)
			},
			wantFragments: []string{"tenant_id = $1 AND order_id = $2"},
			wantArgs:      []any{want.TenantID, want.OrderID},
		},
		{
			name: "Order identity lock",
			call: func(repository *Repository) (payment.CapacityHold, error) {
				return repository.GetCapacityHoldByOrderForUpdate(
					context.Background(),
					want.TenantID,
					want.OrderID,
				)
			},
			wantFragments: []string{"tenant_id = $1 AND order_id = $2", "FOR UPDATE"},
			wantArgs:      []any{want.TenantID, want.OrderID},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var capturedQuery string
			var capturedArgs []any
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(query string, args ...any) rowScanner {
					capturedQuery = query
					capturedArgs = append([]any(nil), args...)
					return &fakeRow{values: holdScanValues(want)}
				},
			}}
			got, err := test.call(repository)
			if err != nil {
				t.Fatalf("capacity hold get error = %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("capacity hold get = %+v, want %+v", got, want)
			}
			for _, fragment := range test.wantFragments {
				if !strings.Contains(capturedQuery, fragment) {
					t.Fatalf("query %q does not contain %q", capturedQuery, fragment)
				}
			}
			if !reflect.DeepEqual(capturedArgs, test.wantArgs) {
				t.Fatalf("query args = %#v, want %#v", capturedArgs, test.wantArgs)
			}
		})
	}
}

func TestUpdateCapacityHoldUsesOptimisticVersion(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	current := activeHold(now)
	releasedAt := now.Add(time.Minute)
	reason := "payment_closed"
	updated := current
	updated.HoldStatus = payment.CapacityHoldStatusReleased
	updated.ReleasedAt = &releasedAt
	updated.ReleaseReason = &reason
	updated.Version = 2
	updated.UpdatedAt = releasedAt

	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return &fakeRow{values: holdScanValues(updated)}
		},
	}}
	got, err := repository.UpdateCapacityHold(context.Background(), updated, current.Version)
	if err != nil {
		t.Fatalf("UpdateCapacityHold() error = %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("UpdateCapacityHold() = %+v, want %+v", got, updated)
	}
	if !strings.Contains(capturedQuery, "version = version + 1") ||
		!strings.Contains(capturedQuery, "AND version = $8") ||
		capturedArgs[0] != updated.TenantID ||
		capturedArgs[1] != updated.ID ||
		capturedArgs[6] != updated.UpdatedAt ||
		capturedArgs[7] != int64(1) {
		t.Fatalf("UpdateCapacityHold query/args = %s %#v", capturedQuery, capturedArgs)
	}

	conflictRepository := &Repository{db: &fakeQueryExecutor{
		queryRow: func(string, ...any) rowScanner { return &fakeRow{err: sql.ErrNoRows} },
	}}
	if _, err := conflictRepository.UpdateCapacityHold(
		context.Background(),
		updated,
		current.Version,
	); !errors.Is(err, ErrCapacityHoldVersionConflict) {
		t.Fatalf("UpdateCapacityHold(conflict) error = %v", err)
	}
}

func TestRepositoryTranslatesOnlyNoRows(t *testing.T) {
	t.Parallel()

	scanFailure := errors.New("scan failed")
	tests := []struct {
		name    string
		row     rowScanner
		call    func(*Repository) error
		wantErr error
	}{
		{
			name: "Order not found",
			row:  &fakeRow{err: sql.ErrNoRows},
			call: func(repository *Repository) error {
				_, err := repository.GetOrder(context.Background(), uuid.New(), uuid.New())
				return err
			},
			wantErr: ErrOrderNotFound,
		},
		{
			name: "Order scan",
			row:  &fakeRow{err: scanFailure},
			call: func(repository *Repository) error {
				_, err := repository.GetOrder(context.Background(), uuid.New(), uuid.New())
				return err
			},
			wantErr: scanFailure,
		},
		{
			name: "hold not found",
			row:  &fakeRow{err: sql.ErrNoRows},
			call: func(repository *Repository) error {
				_, err := repository.GetCapacityHold(context.Background(), uuid.New(), uuid.New())
				return err
			},
			wantErr: ErrCapacityHoldNotFound,
		},
		{
			name: "hold scan",
			row:  &fakeRow{err: scanFailure},
			call: func(repository *Repository) error {
				_, err := repository.GetCapacityHold(context.Background(), uuid.New(), uuid.New())
				return err
			},
			wantErr: scanFailure,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &Repository{db: &fakeQueryExecutor{
				queryRow: func(string, ...any) rowScanner { return test.row },
			}}
			if err := test.call(repository); !errors.Is(err, test.wantErr) {
				t.Fatalf("repository error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

type fakeQueryExecutor struct {
	queryRow func(string, ...any) rowScanner
}

func (executor *fakeQueryExecutor) queryRowContext(
	_ context.Context,
	query string,
	args ...any,
) rowScanner {
	return executor.queryRow(query, args...)
}

type fakeRow struct {
	values []any
	err    error
}

func (row *fakeRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != len(row.values) {
		return errors.New("fake row destination count mismatch")
	}
	for index, destination := range destinations {
		target := reflect.ValueOf(destination)
		value := reflect.ValueOf(row.values[index])
		if target.Kind() != reflect.Pointer ||
			!value.IsValid() ||
			!value.Type().AssignableTo(target.Elem().Type()) {
			return errors.New("fake row value type mismatch")
		}
		target.Elem().Set(value)
	}
	return nil
}

func orderScanValues(value payment.Order) []any {
	return []any{
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
		nullUUID(value.MerchantConfigGenerationID),
		value.OriginalPriceCents,
		value.DiscountCents,
		value.PayableCents,
		nullInt64(value.ActualPaidCents),
		nullString(value.WeChatTransactionID),
		nullTime(value.PaidAt),
		nullTime(value.ClosedAt),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func holdScanValues(value payment.CapacityHold) []any {
	return []any{
		value.ID,
		value.TenantID,
		value.OrderID,
		value.RegistrationID,
		value.SessionID,
		value.HoldStatus,
		value.ExpiresAt,
		nullTime(value.ConvertedAt),
		nullTime(value.ReleasedAt),
		nullString(value.ReleaseReason),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func pendingOrder(now time.Time) payment.Order {
	return payment.Order{
		ID:                 uuid.New(),
		TenantID:           uuid.New(),
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		InstanceID:         uuid.New(),
		SessionID:          uuid.New(),
		PrincipalID:        uuid.New(),
		PaymentStatus:      payment.OrderStatusPending,
		IdempotencyKey:     "payment:test:1",
		MerchantOrderNo:    "merchant-order-1",
		PaymentAppID:       "wx-app-1",
		PaymentMerchantID:  "wx-merchant-1",
		OriginalPriceCents: 10_000,
		DiscountCents:      1_000,
		PayableCents:       9_000,
		Version:            1,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}

func activeHold(now time.Time) payment.CapacityHold {
	return payment.CapacityHold{
		ID:             uuid.New(),
		TenantID:       uuid.New(),
		OrderID:        uuid.New(),
		RegistrationID: uuid.New(),
		SessionID:      uuid.New(),
		HoldStatus:     payment.CapacityHoldStatusActive,
		ExpiresAt:      now.Add(payment.CapacityHoldDuration),
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

func nullInt64(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}

func nullString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func nullTime(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func nullUUID(value uuid.UUID) uuid.NullUUID {
	if value == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: value, Valid: true}
}
