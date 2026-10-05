package bookingpostgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/google/uuid"
)

func TestGetOrderDetailRequiresExactOwnerAndOrder(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 8, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	addPendingRefund(&facts, asOf)
	var capturedQuery string
	var capturedArgs []any
	rows := newFakeRows(mustMyOrderScanValues(t, facts, asOf))
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return rows, nil
		},
	}}

	detail, err := repository.GetOrderDetail(
		context.Background(),
		facts.Registration.TenantID,
		facts.Registration.PrincipalID,
		facts.Order.ID,
		asOf,
	)
	if err != nil {
		t.Fatalf("GetOrderDetail() error = %v", err)
	}
	if detail.Item.OrderID != facts.Order.ID ||
		detail.Item.RegistrationID != facts.Registration.ID ||
		detail.Item.SessionID != facts.Session.ID ||
		detail.Item.Outcome != booking.MyOrderOutcomeRefundPendingManual ||
		!detail.AsOf.Equal(asOf) ||
		!rows.closed {
		t.Fatalf("GetOrderDetail() = %+v, rows closed=%t", detail, rows.closed)
	}
	for _, fragment := range []string{
		"registration_record.tenant_id = $1",
		"registration_record.principal_id = $2",
		"WHERE tenant_id = $1",
		"AND principal_id = $2",
		"AND order_id = $4",
		"LIMIT 2",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("detail query does not contain %q", fragment)
		}
	}
	if !reflect.DeepEqual(capturedArgs, []any{
		facts.Registration.TenantID,
		facts.Registration.PrincipalID,
		asOf,
		facts.Order.ID,
	}) {
		t.Fatalf("detail args = %#v", capturedArgs)
	}
}

func TestGetOrderDetailRejectsInvalidIdentity(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	principalID := uuid.New()
	orderID := uuid.New()
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			t.Fatal("invalid identity unexpectedly reached PostgreSQL")
			return nil, nil
		},
	}}
	tests := []struct {
		name        string
		tenantID    uuid.UUID
		principalID uuid.UUID
		orderID     uuid.UUID
	}{
		{
			name:        "tenant absent",
			principalID: principalID,
			orderID:     orderID,
		},
		{
			name:     "principal absent",
			tenantID: tenantID,
			orderID:  orderID,
		},
		{
			name:        "Order absent",
			tenantID:    tenantID,
			principalID: principalID,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := repository.GetOrderDetail(
				context.Background(),
				test.tenantID,
				test.principalID,
				test.orderID,
				time.Now(),
			)
			if !errors.Is(err, ErrInvalidMyOrderIdentity) {
				t.Fatalf("GetOrderDetail() error = %v", err)
			}
		})
	}
}

func TestGetOrderDetailPreservesReadFailures(t *testing.T) {
	t.Parallel()

	readFailure := errors.New("read failed")
	tenantID := uuid.New()
	principalID := uuid.New()
	orderID := uuid.New()
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return nil, readFailure
		},
	}}
	if _, err := repository.GetOrderDetail(
		context.Background(),
		tenantID,
		principalID,
		orderID,
		time.Now(),
	); !errors.Is(err, readFailure) {
		t.Fatalf("GetOrderDetail(query failure) error = %v", err)
	}

	repository.db = &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeRows(), nil
		},
	}
	if _, err := repository.GetOrderDetail(
		context.Background(),
		tenantID,
		principalID,
		orderID,
		time.Now(),
	); !errors.Is(err, ErrMyOrderNotFound) {
		t.Fatalf("GetOrderDetail(not found) error = %v", err)
	}

	failedRows := newFakeRows()
	failedRows.err = readFailure
	repository.db = &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return failedRows, nil
		},
	}
	if _, err := repository.GetOrderDetail(
		context.Background(),
		tenantID,
		principalID,
		orderID,
		time.Now(),
	); !errors.Is(err, readFailure) {
		t.Fatalf("GetOrderDetail(iteration failure) error = %v", err)
	}
}

func TestGetOrderDetailFailsClosedOnDuplicateIdentity(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 8, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	addPendingMyOrder(&facts, asOf.Add(-time.Minute))
	values := mustMyOrderScanValues(t, facts, asOf)
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(string, ...any) (rowsScanner, error) {
			return newFakeRows(values, values), nil
		},
	}}

	if _, err := repository.GetOrderDetail(
		context.Background(),
		facts.Registration.TenantID,
		facts.Registration.PrincipalID,
		facts.Order.ID,
		asOf,
	); !errors.Is(err, ErrMyOrderProjection) {
		t.Fatalf("GetOrderDetail(duplicate) error = %v", err)
	}
}
