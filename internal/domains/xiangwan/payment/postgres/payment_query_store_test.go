package paymentpostgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

func TestPaymentQueryStoreAcquiresOneServerOwnedProviderLease(t *testing.T) {
	t.Parallel()

	fixture := newPaymentQueryStoreFixture(t)
	lease, err := payment.NewPaymentQueryLease(
		fixture.createLeaseCommand(fixture.now),
	)
	if err != nil {
		t.Fatalf("NewPaymentQueryLease() error = %v", err)
	}
	providerCalls := 0
	tx := &fakePrepayTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_runtime_generations"):
				return &fakeRow{values: []any{fixture.generationID, int64(2)}}
			case strings.Contains(query, "FROM xiangwan_orders"):
				return &fakeRow{values: orderScanValues(fixture.order)}
			case strings.Contains(query, "FROM xiangwan_payment_attempts"):
				return &fakeRow{values: []any{true}}
			case strings.Contains(query, "FROM xiangwan_payment_query_leases"):
				return &fakeRow{err: sql.ErrNoRows}
			case strings.Contains(query, "INSERT INTO xiangwan_payment_query_leases"):
				providerCalls++
				return &fakeRow{values: paymentQueryLeaseScanValues(lease)}
			default:
				t.Fatalf("unexpected query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	store := fixture.store(t, tx, fixture.now)
	acquisition, err := store.AcquirePaymentQuery(context.Background(), fixture.command)
	if err != nil || !tx.committed || providerCalls != 1 ||
		acquisition.ProviderRequest == nil ||
		acquisition.InvocationToken != fixture.ownerToken ||
		acquisition.ProviderRequest.AppID != fixture.order.PaymentAppID ||
		acquisition.ProviderRequest.MerchantID != fixture.order.PaymentMerchantID ||
		acquisition.ProviderRequest.OutTradeNo != fixture.order.MerchantOrderNo ||
		acquisition.ProviderRequest.AmountCents != fixture.order.PayableCents {
		t.Fatalf("AcquirePaymentQuery() = %+v, %v committed=%t", acquisition, err, tx.committed)
	}
}

func TestPaymentQueryStoreReturnsActiveLeaseWithoutSecondProviderCall(t *testing.T) {
	t.Parallel()

	fixture := newPaymentQueryStoreFixture(t)
	lease, err := payment.NewPaymentQueryLease(
		fixture.createLeaseCommand(fixture.now.Add(-time.Second)),
	)
	if err != nil {
		t.Fatalf("NewPaymentQueryLease() error = %v", err)
	}
	tx := &fakePrepayTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_runtime_generations"):
				return &fakeRow{values: []any{fixture.generationID, int64(2)}}
			case strings.Contains(query, "FROM xiangwan_orders"):
				return &fakeRow{values: orderScanValues(fixture.order)}
			case strings.Contains(query, "FROM xiangwan_payment_attempts"):
				return &fakeRow{values: []any{true}}
			case strings.Contains(query, "FROM xiangwan_payment_query_leases"):
				return &fakeRow{values: paymentQueryLeaseScanValues(lease)}
			default:
				t.Fatalf("unexpected query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	store := fixture.store(t, tx, fixture.now)
	acquisition, err := store.AcquirePaymentQuery(context.Background(), fixture.command)
	if err != nil || !tx.committed || acquisition.ProviderRequest != nil ||
		acquisition.InvocationToken != uuid.Nil ||
		acquisition.Lease.QueryStatus != payment.PaymentQueryStatusInProgress {
		t.Fatalf("AcquirePaymentQuery(active) = %+v, %v", acquisition, err)
	}
}

func TestPaymentQueryStoreReturnsAlreadyClosedOrderWithoutProviderCall(t *testing.T) {
	t.Parallel()

	fixture := newPaymentQueryStoreFixture(t)
	closedOrder, _, err := payment.CloseOrderUnpaid(
		fixture.order,
		fixture.now,
	)
	if err != nil {
		t.Fatalf("CloseOrderUnpaid() error = %v", err)
	}
	tx := &fakePrepayTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_runtime_generations"):
				return &fakeRow{values: []any{fixture.generationID, int64(2)}}
			case strings.Contains(query, "FROM xiangwan_orders"):
				return &fakeRow{values: orderScanValues(closedOrder)}
			default:
				t.Fatalf("unexpected query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	store := fixture.store(t, tx, fixture.now)
	acquisition, err := store.AcquirePaymentQuery(
		context.Background(),
		fixture.command,
	)
	if err != nil || !tx.committed || acquisition.ProviderRequest != nil ||
		acquisition.Order.PaymentStatus != payment.OrderStatusClosedUnpaid {
		t.Fatalf("AcquirePaymentQuery(closed) = %+v, %v", acquisition, err)
	}
}

func TestPaymentQueryStoreFailureMarksLeaseAndOrderUnknownAtomically(t *testing.T) {
	t.Parallel()

	fixture := newPaymentQueryStoreFixture(t)
	lease, err := payment.NewPaymentQueryLease(fixture.createLeaseCommand(fixture.now))
	if err != nil {
		t.Fatalf("NewPaymentQueryLease() error = %v", err)
	}
	completedAt := fixture.now.Add(time.Second)
	unknownLease, err := payment.CompletePaymentQueryFailed(
		lease,
		fixture.ownerToken,
		payment.NewProviderFailure(
			payment.ProviderFailureTimeout,
			"",
			context.DeadlineExceeded,
		),
		completedAt,
	)
	if err != nil {
		t.Fatalf("CompletePaymentQueryFailed() error = %v", err)
	}
	unknownOrder, _, err := payment.MarkOrderUnknown(fixture.order, completedAt)
	if err != nil {
		t.Fatalf("MarkOrderUnknown() error = %v", err)
	}
	leaseUpdates := 0
	orderUpdates := 0
	tx := &fakePrepayTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_orders"):
				return &fakeRow{values: orderScanValues(fixture.order)}
			case strings.Contains(query, "FROM xiangwan_payment_query_leases"):
				return &fakeRow{values: paymentQueryLeaseScanValues(lease)}
			case strings.Contains(query, "UPDATE xiangwan_payment_query_leases"):
				leaseUpdates++
				return &fakeRow{values: paymentQueryLeaseScanValues(unknownLease)}
			case strings.Contains(query, "UPDATE xiangwan_orders"):
				orderUpdates++
				return &fakeRow{values: orderScanValues(unknownOrder)}
			default:
				t.Fatalf("unexpected query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	store := fixture.store(t, tx, completedAt)
	result, err := store.CompleteFailedPaymentQuery(
		context.Background(),
		fixture.acquisition(lease),
		payment.NewProviderFailure(
			payment.ProviderFailureTimeout,
			"",
			context.DeadlineExceeded,
		),
	)
	if err != nil || !tx.committed || leaseUpdates != 1 || orderUpdates != 1 ||
		result.QueryStatus != payment.PaymentQueryStatusUnknown ||
		result.Order.PaymentStatus != payment.OrderStatusUnknown {
		t.Fatalf("CompleteFailedPaymentQuery() = %+v, %v tx=%+v", result, err, tx)
	}
}

func TestPaymentQueryStoreFinalizesClosedLeaseAfterBusinessConvergence(t *testing.T) {
	t.Parallel()

	fixture := newPaymentQueryStoreFixture(t)
	lease, err := payment.NewPaymentQueryLease(fixture.createLeaseCommand(fixture.now))
	if err != nil {
		t.Fatalf("NewPaymentQueryLease() error = %v", err)
	}
	acquisition := fixture.acquisition(lease)
	observation, err := payment.NewTransactionObservation(
		acquisition,
		payment.ProviderPaymentQueryResult{
			AppID:             lease.PaymentAppID,
			MerchantID:        lease.PaymentMerchantID,
			OutTradeNo:        lease.OutTradeNo,
			TradeType:         payment.PaymentQueryTradeType,
			TradeState:        payment.ProviderTradeStateRevoked,
			AmountCents:       lease.AmountCents,
			Currency:          lease.Currency,
			ProviderRequestID: "provider-query-terminal-1",
		},
		fixture.now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("NewTransactionObservation() error = %v", err)
	}
	closedOrder, _, err := payment.CloseOrderUnpaid(
		fixture.order,
		fixture.now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("CloseOrderUnpaid() error = %v", err)
	}
	closedLease, err := payment.CompletePaymentQueryClosed(
		lease,
		fixture.ownerToken,
		observation,
		fixture.now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("CompletePaymentQueryClosed() error = %v", err)
	}
	updates := 0
	tx := &fakePrepayTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_orders"):
				return &fakeRow{values: orderScanValues(closedOrder)}
			case strings.Contains(query, "FROM xiangwan_payment_query_leases"):
				return &fakeRow{values: paymentQueryLeaseScanValues(lease)}
			case strings.Contains(query, "UPDATE xiangwan_payment_query_leases"):
				updates++
				return &fakeRow{values: paymentQueryLeaseScanValues(closedLease)}
			default:
				t.Fatalf("unexpected query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	store := fixture.store(t, tx, fixture.now.Add(time.Second))
	result, err := store.CompleteClosedPaymentQuery(
		context.Background(),
		acquisition,
		observation,
		payment.PaymentConvergence{Order: closedOrder},
	)
	if err != nil || !tx.committed || updates != 1 ||
		result.QueryStatus != payment.PaymentQueryStatusClosed ||
		result.Order.PaymentStatus != payment.OrderStatusClosedUnpaid ||
		result.NextQueryAt != nil {
		t.Fatalf("CompleteClosedPaymentQuery() = %+v, %v tx=%+v", result, err, tx)
	}
}

func TestPaymentQueryStoreRollsBackObservationAndLeaseTogether(t *testing.T) {
	t.Parallel()

	fixture := newPaymentQueryStoreFixture(t)
	lease, err := payment.NewPaymentQueryLease(fixture.createLeaseCommand(fixture.now))
	if err != nil {
		t.Fatalf("NewPaymentQueryLease() error = %v", err)
	}
	request := fixture.acquisition(lease)
	result := payment.ProviderPaymentQueryResult{
		AppID:             lease.PaymentAppID,
		MerchantID:        lease.PaymentMerchantID,
		OutTradeNo:        lease.OutTradeNo,
		TradeType:         payment.PaymentQueryTradeType,
		TradeState:        payment.ProviderTradeStateNotPay,
		AmountCents:       lease.AmountCents,
		Currency:          lease.Currency,
		ProviderRequestID: "provider-query-1",
	}
	completedAt := fixture.now.Add(time.Second)
	observation, err := payment.NewTransactionObservation(request, result, completedAt)
	if err != nil {
		t.Fatalf("NewTransactionObservation() error = %v", err)
	}
	writeErr := errors.New("observation unavailable")
	tx := &fakePrepayTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_orders"):
				return &fakeRow{values: orderScanValues(fixture.order)}
			case strings.Contains(query, "FROM xiangwan_payment_query_leases"):
				return &fakeRow{values: paymentQueryLeaseScanValues(lease)}
			case strings.Contains(query, "INSERT INTO xiangwan_payment_transaction_observations"):
				return &fakeRow{err: writeErr}
			default:
				t.Fatalf("unexpected query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	store := fixture.store(t, tx, completedAt)
	_, err = store.CompleteObservedPaymentQuery(
		context.Background(),
		request,
		observation,
	)
	if !errors.Is(err, writeErr) || tx.committed || !tx.rolledBack {
		t.Fatalf("CompleteObservedPaymentQuery() error=%v tx=%+v", err, tx)
	}
}

func TestPaymentTransactionObservationAcceptsExactProviderReplay(t *testing.T) {
	t.Parallel()

	observation := validPaymentNotificationObservation()
	existing := observation
	existing.ID = uuid.New()
	existing.ObservedAt = observation.ObservedAt.Add(-time.Second)
	executor := &fakeQueryExecutor{queryRow: func(query string, _ ...any) rowScanner {
		switch {
		case strings.Contains(query, "INSERT INTO xiangwan_payment_transaction_observations"):
			return &fakeRow{err: sql.ErrNoRows}
		case strings.Contains(query, "FROM xiangwan_payment_transaction_observations"):
			return &fakeRow{values: paymentTransactionObservationScanValues(existing)}
		default:
			t.Fatalf("unexpected query: %s", query)
			return &fakeRow{}
		}
	}}
	if err := insertPaymentTransactionObservation(
		context.Background(),
		executor,
		observation,
	); err != nil {
		t.Fatalf("insertPaymentTransactionObservation(replay) error = %v", err)
	}
}

func TestPaymentTransactionObservationRejectsConflictingProviderReplay(t *testing.T) {
	t.Parallel()

	observation := validPaymentNotificationObservation()
	existing := observation
	existing.PayloadDigest = strings.Repeat("d", 64)
	executor := &fakeQueryExecutor{queryRow: func(query string, _ ...any) rowScanner {
		switch {
		case strings.Contains(query, "INSERT INTO xiangwan_payment_transaction_observations"):
			return &fakeRow{err: sql.ErrNoRows}
		case strings.Contains(query, "FROM xiangwan_payment_transaction_observations"):
			return &fakeRow{values: paymentTransactionObservationScanValues(existing)}
		default:
			t.Fatalf("unexpected query: %s", query)
			return &fakeRow{}
		}
	}}
	if err := insertPaymentTransactionObservation(
		context.Background(),
		executor,
		observation,
	); !errors.Is(err, ErrPaymentConfirmationConflict) {
		t.Fatalf("insertPaymentTransactionObservation(conflict) error = %v", err)
	}
}

type paymentQueryStoreFixture struct {
	now          time.Time
	tenantID     uuid.UUID
	generationID uuid.UUID
	principalID  uuid.UUID
	leaseID      uuid.UUID
	ownerToken   uuid.UUID
	order        payment.Order
	command      payment.PaymentQueryCommand
}

func newPaymentQueryStoreFixture(t *testing.T) paymentQueryStoreFixture {
	t.Helper()
	now := time.Date(2026, time.September, 14, 4, 5, 6, 0, time.UTC)
	fixture := paymentQueryStoreFixture{
		now:          now,
		tenantID:     uuid.New(),
		generationID: uuid.New(),
		principalID:  uuid.New(),
		leaseID:      uuid.New(),
		ownerToken:   uuid.New(),
	}
	fixture.order = payment.Order{
		ID:                 uuid.New(),
		TenantID:           fixture.tenantID,
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		InstanceID:         uuid.New(),
		SessionID:          uuid.New(),
		PrincipalID:        fixture.principalID,
		PaymentStatus:      payment.OrderStatusPending,
		IdempotencyKey:     "registration:query:1",
		MerchantOrderNo:    "XW-PAYMENT-QUERY-0001",
		PaymentAppID:       "wxXiangwan123",
		PaymentMerchantID:  "1900000109",
		OriginalPriceCents: 9900,
		PayableCents:       9900,
		Version:            1,
		CreatedAt:          now.Add(-time.Minute),
		UpdatedAt:          now.Add(-time.Minute),
	}
	fixture.command = payment.PaymentQueryCommand{
		TenantID:     fixture.tenantID,
		GenerationID: fixture.generationID,
		OrderID:      fixture.order.ID,
		PrincipalID:  fixture.principalID,
	}
	return fixture
}

func (fixture paymentQueryStoreFixture) createLeaseCommand(
	at time.Time,
) payment.CreatePaymentQueryLeaseCommand {
	return payment.CreatePaymentQueryLeaseCommand{
		ID:                fixture.leaseID,
		TenantID:          fixture.tenantID,
		OrderID:           fixture.order.ID,
		PrincipalID:       fixture.principalID,
		GenerationID:      fixture.generationID,
		PaymentAppID:      fixture.order.PaymentAppID,
		PaymentMerchantID: fixture.order.PaymentMerchantID,
		OutTradeNo:        fixture.order.MerchantOrderNo,
		AmountCents:       fixture.order.PayableCents,
		OwnerToken:        fixture.ownerToken,
		Now:               at,
	}
}

func (fixture paymentQueryStoreFixture) acquisition(
	lease payment.PaymentQueryLease,
) payment.PaymentQueryAcquisition {
	request := payment.ProviderPaymentQueryRequest{
		AppID:       lease.PaymentAppID,
		MerchantID:  lease.PaymentMerchantID,
		OutTradeNo:  lease.OutTradeNo,
		AmountCents: lease.AmountCents,
		Currency:    lease.Currency,
	}
	return payment.PaymentQueryAcquisition{
		Lease:           lease,
		Order:           fixture.order,
		InvocationToken: fixture.ownerToken,
		ProviderRequest: &request,
	}
}

func (fixture paymentQueryStoreFixture) store(
	t *testing.T,
	tx prepayTransaction,
	now time.Time,
) *PaymentQueryStore {
	t.Helper()
	ids := []uuid.UUID{fixture.leaseID, fixture.ownerToken}
	index := 0
	store, err := newPaymentQueryStore(
		&fakePrepayTransactionStarter{transactions: []prepayTransaction{tx}},
		PaymentQueryStoreConfig{
			Now: func() time.Time { return now },
			NewUUID: func() uuid.UUID {
				if index >= len(ids) {
					return uuid.New()
				}
				value := ids[index]
				index++
				return value
			},
		},
	)
	if err != nil {
		t.Fatalf("newPaymentQueryStore() error = %v", err)
	}
	return store
}

func paymentQueryLeaseScanValues(value payment.PaymentQueryLease) []any {
	var tradeState sql.NullString
	if value.LastTradeState != nil {
		tradeState = sql.NullString{String: string(*value.LastTradeState), Valid: true}
	}
	return []any{
		value.ID,
		value.TenantID,
		value.OrderID,
		value.PrincipalID,
		value.GenerationID,
		value.PaymentAppID,
		value.PaymentMerchantID,
		value.OutTradeNo,
		value.AmountCents,
		value.Currency,
		value.QueryStatus,
		paymentNullUUID(value.OwnerToken),
		nullTime(value.LeaseExpiresAt),
		nullTime(value.NextQueryAt),
		tradeState,
		nullString(value.LastErrorClass),
		nullString(value.LastProviderRequestID),
		nullTime(value.CompletedAt),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func validPaymentNotificationObservation() payment.TransactionObservation {
	observedAt := time.Date(2026, time.September, 14, 8, 0, 0, 0, time.UTC)
	successAt := observedAt.Add(-time.Minute)
	return payment.TransactionObservation{
		ID:                uuid.New(),
		TenantID:          uuid.New(),
		OrderID:           uuid.New(),
		PrincipalID:       uuid.New(),
		ObservationSource: payment.TransactionObservationSourcePaymentNotification,
		SourceKey:         "notify-payment-success-store-01",
		PaymentAppID:      "wxXiangwan123",
		PaymentMerchantID: "1900000109",
		OutTradeNo:        "XW-NOTIFY-ORDER-0001",
		TransactionID:     "wechat-notify-transaction-01",
		TradeType:         payment.PaymentQueryTradeType,
		TradeState:        payment.ProviderTradeStateSuccess,
		AmountCents:       9_900,
		Currency:          payment.PaymentQueryCurrency,
		SuccessAt:         &successAt,
		PayloadDigest:     strings.Repeat("c", 64),
		ObservedAt:        observedAt,
	}
}

func paymentTransactionObservationScanValues(
	value payment.TransactionObservation,
) []any {
	providerRequestID := sql.NullString{}
	if value.ProviderRequestID != "" {
		providerRequestID = sql.NullString{
			String: value.ProviderRequestID,
			Valid:  true,
		}
	}
	transactionID := sql.NullString{}
	if value.TransactionID != "" {
		transactionID = sql.NullString{String: value.TransactionID, Valid: true}
	}
	return []any{
		value.ID,
		value.TenantID,
		value.OrderID,
		value.PrincipalID,
		value.ObservationSource,
		value.SourceKey,
		providerRequestID,
		value.PaymentAppID,
		value.PaymentMerchantID,
		value.OutTradeNo,
		transactionID,
		value.TradeType,
		value.TradeState,
		value.AmountCents,
		value.Currency,
		nullTime(value.SuccessAt),
		value.PayloadDigest,
		value.ObservedAt,
	}
}
