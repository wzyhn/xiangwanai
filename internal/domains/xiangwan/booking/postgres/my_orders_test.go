package bookingpostgres

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestListOrdersUsesOwnedStableKeyset(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 7, 0, 0, 0, time.UTC)
	first := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	second := myRegistrationFacts(asOf, asOf.Add(3*time.Hour))
	lookahead := myRegistrationFacts(asOf, asOf.Add(4*time.Hour))
	alignMyRegistrationOwner(&first, second.Registration)
	alignMyRegistrationOwner(&lookahead, second.Registration)
	addPendingMyOrder(&first, asOf.Add(-time.Minute))
	addPendingMyOrder(&second, asOf.Add(-2*time.Minute))
	addPendingMyOrder(&lookahead, asOf.Add(-3*time.Minute))

	call := 0
	var capturedQueries []string
	var capturedArgs [][]any
	repository := &Repository{db: &fakeQueryExecutor{
		queryRows: func(query string, args ...any) (rowsScanner, error) {
			call++
			capturedQueries = append(capturedQueries, query)
			capturedArgs = append(capturedArgs, append([]any(nil), args...))
			if call == 1 {
				return newFakeRows(
					mustMyOrderScanValues(t, first, asOf),
					mustMyOrderScanValues(t, second, asOf),
					mustMyOrderScanValues(t, lookahead, asOf),
				), nil
			}
			return newFakeRows(), nil
		},
	}}

	page, err := repository.ListOrders(
		context.Background(),
		booking.MyOrderFilter{
			TenantID:    second.Registration.TenantID,
			PrincipalID: second.Registration.PrincipalID,
			Limit:       2,
			At:          asOf,
		},
	)
	if err != nil {
		t.Fatalf("ListOrders() error = %v", err)
	}
	if page.ActiveState != booking.MyOrderStateAll ||
		!page.AsOf.Equal(asOf) ||
		len(page.Items) != 2 ||
		page.Items[0].OrderID != first.Order.ID ||
		page.Items[1].OrderID != second.Order.ID ||
		page.NextCursor == "" {
		t.Fatalf("ListOrders() = %+v", page)
	}
	if !reflect.DeepEqual(capturedArgs[0], []any{
		second.Registration.TenantID,
		second.Registration.PrincipalID,
		asOf,
		booking.MyOrderStateAll,
		3,
	}) {
		t.Fatalf("first query args = %#v", capturedArgs[0])
	}
	for _, fragment := range []string{
		"registration_record.tenant_id = $1",
		"registration_record.principal_id = $2",
		"order_record.principal_id = registration_record.principal_id",
		"WHERE order_id IS NOT NULL",
		"($4 = 'all' OR order_view_state = $4)",
		"order_sort_at DESC",
		"order_id DESC",
		"LIMIT $5",
	} {
		if !strings.Contains(capturedQueries[0], fragment) {
			t.Fatalf("first query does not contain %q", fragment)
		}
	}

	next, err := repository.ListOrders(
		context.Background(),
		booking.MyOrderFilter{
			TenantID:    second.Registration.TenantID,
			PrincipalID: second.Registration.PrincipalID,
			Limit:       2,
			Cursor:      page.NextCursor,
			At:          asOf.Add(time.Minute),
		},
	)
	if err != nil {
		t.Fatalf("ListOrders(next) error = %v", err)
	}
	if len(next.Items) != 0 ||
		next.NextCursor != "" ||
		!next.AsOf.Equal(asOf) {
		t.Fatalf("ListOrders(next) = %+v", next)
	}
	if len(capturedArgs[1]) != 8 ||
		capturedArgs[1][0] != second.Registration.TenantID ||
		capturedArgs[1][1] != second.Registration.PrincipalID ||
		!capturedArgs[1][2].(time.Time).Equal(asOf) ||
		capturedArgs[1][3] != booking.MyOrderStateAll ||
		capturedArgs[1][4] != 1 ||
		!capturedArgs[1][5].(time.Time).Equal(second.Order.UpdatedAt) ||
		capturedArgs[1][6] != second.Order.ID ||
		capturedArgs[1][7] != 3 {
		t.Fatalf("next query args = %#v", capturedArgs[1])
	}
	for _, fragment := range []string{
		"order_sort_rank > $5",
		"(order_sort_at, order_id) < ($6, $7)",
		"LIMIT $8",
	} {
		if !strings.Contains(capturedQueries[1], fragment) {
			t.Fatalf("next query does not contain %q", fragment)
		}
	}
}

func TestScanMyOrderItemChecksSQLDomainProjection(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 7, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	addPendingRefund(&facts, asOf)
	values := mustMyOrderScanValues(t, facts, asOf)

	item, err := scanMyOrderItem(&fakeRow{values: values}, asOf)
	if err != nil {
		t.Fatalf("scanMyOrderItem() error = %v", err)
	}
	if item.State != booking.MyOrderStateRefundProcessing ||
		item.Refund == nil ||
		item.Refund.RefundCaseID != facts.Refund.ID ||
		item.PaymentStatus != payment.OrderStatusPaidConfirmed {
		t.Fatalf("My Order item = %+v", item)
	}

	values[len(values)-3] = booking.MyOrderStateClosed
	if _, err := scanMyOrderItem(
		&fakeRow{values: values},
		asOf,
	); !errors.Is(err, ErrMyOrderProjection) {
		t.Fatalf("scanMyOrderItem(drift) error = %v", err)
	}
}

func TestListOrdersRejectsInvalidAndCrossContextCursors(t *testing.T) {
	t.Parallel()

	asOf := time.Date(2026, time.September, 13, 7, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(asOf, asOf.Add(2*time.Hour))
	addPendingMyOrder(&facts, asOf.Add(-time.Minute))
	reservation, err := booking.ProjectMyRegistration(facts, asOf)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	item, err := booking.ProjectMyOrder(reservation)
	if err != nil {
		t.Fatalf("ProjectMyOrder() error = %v", err)
	}
	validFilter := booking.MyOrderFilter{
		TenantID:    facts.Registration.TenantID,
		PrincipalID: facts.Registration.PrincipalID,
		State:       booking.MyOrderStatePendingPayment,
		Limit:       1,
		At:          asOf,
	}
	cursor, err := encodeMyOrdersCursor(validFilter, item)
	if err != nil {
		t.Fatalf("encodeMyOrdersCursor() error = %v", err)
	}
	unexpectedQuery := func(string, ...any) (rowsScanner, error) {
		t.Fatal("invalid request unexpectedly reached PostgreSQL")
		return nil, nil
	}
	repository := &Repository{db: &fakeQueryExecutor{queryRows: unexpectedQuery}}
	tests := []struct {
		name    string
		filter  booking.MyOrderFilter
		wantErr error
	}{
		{
			name:    "ownership required",
			filter:  booking.MyOrderFilter{},
			wantErr: ErrInvalidMyOrdersFilter,
		},
		{
			name: "unknown state",
			filter: booking.MyOrderFilter{
				TenantID:    validFilter.TenantID,
				PrincipalID: validFilter.PrincipalID,
				State:       "unknown",
			},
			wantErr: ErrInvalidMyOrdersFilter,
		},
		{
			name: "limit too large",
			filter: booking.MyOrderFilter{
				TenantID:    validFilter.TenantID,
				PrincipalID: validFilter.PrincipalID,
				Limit:       booking.MaxMyOrdersLimit + 1,
			},
			wantErr: ErrInvalidMyOrdersFilter,
		},
		{
			name: "malformed cursor",
			filter: withMyOrdersCursor(
				validFilter,
				"not-base64!",
				asOf,
			),
			wantErr: ErrInvalidMyOrdersCursor,
		},
		{
			name: "unknown cursor field",
			filter: withMyOrdersCursor(
				validFilter,
				base64.RawURLEncoding.EncodeToString(
					[]byte(`{"v":1,"unknown":true}`),
				),
				asOf,
			),
			wantErr: ErrInvalidMyOrdersCursor,
		},
		{
			name: "tenant changed",
			filter: func() booking.MyOrderFilter {
				value := withMyOrdersCursor(validFilter, cursor, asOf)
				value.TenantID = uuid.New()
				return value
			}(),
			wantErr: ErrStaleMyOrdersCursor,
		},
		{
			name: "principal changed",
			filter: func() booking.MyOrderFilter {
				value := withMyOrdersCursor(validFilter, cursor, asOf)
				value.PrincipalID = uuid.New()
				return value
			}(),
			wantErr: ErrStaleMyOrdersCursor,
		},
		{
			name: "state changed",
			filter: func() booking.MyOrderFilter {
				value := withMyOrdersCursor(validFilter, cursor, asOf)
				value.State = booking.MyOrderStateAll
				return value
			}(),
			wantErr: ErrStaleMyOrdersCursor,
		},
		{
			name: "cursor expired",
			filter: withMyOrdersCursor(
				validFilter,
				cursor,
				asOf.Add(booking.MaxMyOrdersCursorAge+time.Second),
			),
			wantErr: ErrStaleMyOrdersCursor,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, gotErr := repository.ListOrders(
				context.Background(),
				test.filter,
			)
			if !errors.Is(gotErr, test.wantErr) {
				t.Fatalf("ListOrders() error = %v, want %v", gotErr, test.wantErr)
			}
		})
	}
}

func withMyOrdersCursor(
	filter booking.MyOrderFilter,
	cursor string,
	at time.Time,
) booking.MyOrderFilter {
	filter.Cursor = cursor
	filter.At = at
	return filter
}

func addPendingMyOrder(
	facts *booking.MyRegistrationFacts,
	updatedAt time.Time,
) {
	facts.Registration.ParticipationStatus =
		registration.ParticipationStatusPendingPayment
	facts.Registration.ConfirmedAt = nil
	facts.Registration.Version = 1
	facts.Registration.UpdatedAt = updatedAt
	orderID := uuid.New()
	facts.Order = &payment.Order{
		ID:                 orderID,
		TenantID:           facts.Registration.TenantID,
		RegistrationID:     facts.Registration.ID,
		SeriesID:           facts.Registration.SeriesID,
		InstanceID:         facts.Registration.InstanceID,
		SessionID:          facts.Registration.SessionID,
		PrincipalID:        facts.Registration.PrincipalID,
		PaymentStatus:      payment.OrderStatusPending,
		OriginalPriceCents: 10_000,
		DiscountCents:      1_000,
		PayableCents:       9_000,
		Version:            1,
		CreatedAt:          updatedAt,
		UpdatedAt:          updatedAt,
	}
	facts.Hold = &payment.CapacityHold{
		ID:             uuid.New(),
		TenantID:       facts.Registration.TenantID,
		OrderID:        orderID,
		RegistrationID: facts.Registration.ID,
		SessionID:      facts.Registration.SessionID,
		HoldStatus:     payment.CapacityHoldStatusActive,
		ExpiresAt:      updatedAt.Add(10 * time.Minute),
		Version:        1,
		CreatedAt:      updatedAt,
		UpdatedAt:      updatedAt,
	}
}

func mustMyOrderScanValues(
	t *testing.T,
	facts booking.MyRegistrationFacts,
	asOf time.Time,
) []any {
	t.Helper()
	values := mustMyRegistrationScanValues(t, facts, asOf)
	reservation, err := booking.ProjectMyRegistration(facts, asOf)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	item, err := booking.ProjectMyOrder(reservation)
	if err != nil {
		t.Fatalf("ProjectMyOrder() error = %v", err)
	}
	return append(values, item.State, item.SortRank, item.SortAt)
}
