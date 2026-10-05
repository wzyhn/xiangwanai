package paymentpostgres

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

func TestPaymentCloseJobStoreClaimsDueJobAndProjectsProviderRequests(t *testing.T) {
	t.Parallel()

	fixture := newPaymentCloseJobStoreFixture(t)
	acquired, err := payment.AcquirePaymentCloseJob(
		fixture.job,
		fixture.generationID,
		fixture.ownerToken,
		fixture.now,
	)
	if err != nil {
		t.Fatalf("AcquirePaymentCloseJob() error = %v", err)
	}
	updates := 0
	var acquisitionArgs []any
	var acquisitionQuery string
	tx := &fakePrepayTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, args ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_runtime_generations"):
				return &fakeRow{values: []any{fixture.generationID, int64(2)}}
			case strings.Contains(query, "FROM xiangwan_payment_close_jobs"):
				acquisitionArgs = append([]any(nil), args...)
				acquisitionQuery = query
				return &fakeRow{values: paymentCloseJobScanValues(fixture.job)}
			case strings.Contains(query, "UPDATE xiangwan_payment_close_jobs"):
				updates++
				return &fakeRow{values: paymentCloseJobScanValues(acquired)}
			case strings.Contains(query, "FROM xiangwan_orders"):
				return &fakeRow{values: orderScanValues(fixture.order)}
			default:
				t.Fatalf("unexpected query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	store := fixture.store(t, tx, fixture.now)
	result, err := store.AcquirePaymentCloseJob(
		context.Background(),
		fixture.tenantID,
		fixture.generationID,
		fixture.merchantConfigGenerationID,
		fixture.order.PaymentAppID,
		fixture.order.PaymentMerchantID,
	)
	if err != nil || !tx.committed || updates != 1 || !result.Found ||
		result.InvocationToken != fixture.ownerToken ||
		result.QueryRequest == nil || result.CloseRequest == nil ||
		result.QueryRequest.OutTradeNo != fixture.order.MerchantOrderNo ||
		result.QueryRequest.AmountCents != fixture.order.PayableCents ||
		result.CloseRequest.AppID != fixture.order.PaymentAppID ||
		result.Job.JobStatus != payment.PaymentCloseJobStatusInProgress {
		t.Fatalf("AcquirePaymentCloseJob() = %+v, %v committed=%t", result, err, tx.committed)
	}
	if len(acquisitionArgs) != 5 ||
		acquisitionArgs[0] != fixture.tenantID ||
		acquisitionArgs[1] != fixture.now ||
		acquisitionArgs[2] != fixture.merchantConfigGenerationID ||
		acquisitionArgs[3] != fixture.order.PaymentAppID ||
		acquisitionArgs[4] != fixture.order.PaymentMerchantID {
		t.Fatalf("AcquirePaymentCloseJob() filter args = %#v", acquisitionArgs)
	}
	if !strings.Contains(acquisitionQuery, "merchant_config_generation_id IS NULL OR merchant_config_generation_id = $3") ||
		!strings.Contains(acquisitionQuery, "payment_app_id = $4") ||
		!strings.Contains(acquisitionQuery, "payment_merchant_id = $5") {
		t.Fatalf("AcquirePaymentCloseJob() query lacks merchant scope: %q", acquisitionQuery)
	}
}

func TestPaymentCloseJobStoreCompletesLocallyPaidOrderWithoutProvider(t *testing.T) {
	t.Parallel()

	fixture := newPaymentCloseJobStoreFixture(t)
	paidOrder := fixture.order
	paidOrder.PaymentStatus = payment.OrderStatusPaidConfirmed
	acquired, err := payment.AcquirePaymentCloseJob(
		fixture.job,
		fixture.generationID,
		fixture.ownerToken,
		fixture.now,
	)
	if err != nil {
		t.Fatalf("AcquirePaymentCloseJob() error = %v", err)
	}
	completed, err := payment.CompletePaymentCloseJob(
		acquired,
		fixture.ownerToken,
		payment.PaymentCloseJobCompletion{
			Resolution: payment.PaymentCloseResolutionPaymentConverged,
		},
		fixture.now,
	)
	if err != nil {
		t.Fatalf("CompletePaymentCloseJob() error = %v", err)
	}
	updates := 0
	tx := &fakePrepayTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_runtime_generations"):
				return &fakeRow{values: []any{fixture.generationID, int64(2)}}
			case strings.Contains(query, "FROM xiangwan_payment_close_jobs"):
				return &fakeRow{values: paymentCloseJobScanValues(fixture.job)}
			case strings.Contains(query, "UPDATE xiangwan_payment_close_jobs"):
				updates++
				if updates == 1 {
					return &fakeRow{values: paymentCloseJobScanValues(acquired)}
				}
				return &fakeRow{values: paymentCloseJobScanValues(completed)}
			case strings.Contains(query, "FROM xiangwan_orders"):
				return &fakeRow{values: orderScanValues(paidOrder)}
			default:
				t.Fatalf("unexpected query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	store := fixture.store(t, tx, fixture.now)
	result, err := store.AcquirePaymentCloseJob(
		context.Background(),
		fixture.tenantID,
		fixture.generationID,
		fixture.merchantConfigGenerationID,
		fixture.order.PaymentAppID,
		fixture.order.PaymentMerchantID,
	)
	if err != nil || !tx.committed || updates != 2 || !result.Found ||
		result.QueryRequest != nil || result.CloseRequest != nil ||
		result.Job.JobStatus != payment.PaymentCloseJobStatusCompleted ||
		result.Job.Resolution != payment.PaymentCloseResolutionPaymentConverged {
		t.Fatalf("AcquirePaymentCloseJob(paid) = %+v, %v tx=%+v", result, err, tx)
	}
}

func TestPaymentCloseJobStoreCommitsObservationAndTerminalJobTogether(t *testing.T) {
	t.Parallel()

	fixture := newPaymentCloseJobStoreFixture(t)
	acquired, err := payment.AcquirePaymentCloseJob(
		fixture.job,
		fixture.generationID,
		fixture.ownerToken,
		fixture.now,
	)
	if err != nil {
		t.Fatalf("AcquirePaymentCloseJob() error = %v", err)
	}
	request := payment.ProviderPaymentQueryRequest{
		AppID:       acquired.PaymentAppID,
		MerchantID:  acquired.PaymentMerchantID,
		OutTradeNo:  acquired.OutTradeNo,
		AmountCents: acquired.AmountCents,
		Currency:    payment.PaymentQueryCurrency,
	}
	providerResult := payment.ProviderPaymentQueryResult{
		AppID:             request.AppID,
		MerchantID:        request.MerchantID,
		OutTradeNo:        request.OutTradeNo,
		TradeType:         payment.PaymentQueryTradeType,
		TradeState:        payment.ProviderTradeStateNotPay,
		AmountCents:       request.AmountCents,
		Currency:          request.Currency,
		ProviderRequestID: "query-request-1",
	}
	observation, err := payment.NewMerchantQueryObservation(
		payment.MerchantQueryObservationContext{
			ObservationID: fixture.ownerToken,
			TenantID:      acquired.TenantID,
			OrderID:       acquired.OrderID,
			PrincipalID:   acquired.PrincipalID,
			Request:       request,
		},
		providerResult,
		fixture.now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("NewMerchantQueryObservation() error = %v", err)
	}
	completion := payment.PaymentCloseJobCompletion{
		Resolution:        payment.PaymentCloseResolutionProviderClosed,
		TradeState:        providerTradeStatePointerForStore(payment.ProviderTradeStateNotPay),
		ProviderRequestID: "close-request-1",
	}
	completed, err := payment.CompletePaymentCloseJob(
		acquired,
		fixture.ownerToken,
		completion,
		fixture.now.Add(2*time.Second),
	)
	if err != nil {
		t.Fatalf("CompletePaymentCloseJob() error = %v", err)
	}
	observationWrites := 0
	jobWrites := 0
	tx := &fakePrepayTransaction{fakeQueryExecutor: &fakeQueryExecutor{
		queryRow: func(query string, _ ...any) rowScanner {
			switch {
			case strings.Contains(query, "FROM xiangwan_payment_close_jobs"):
				return &fakeRow{values: paymentCloseJobScanValues(acquired)}
			case strings.Contains(query, "FROM xiangwan_orders"):
				return &fakeRow{values: orderScanValues(fixture.order)}
			case strings.Contains(query, "INSERT INTO xiangwan_payment_transaction_observations"):
				observationWrites++
				return &fakeRow{values: []any{observation.ID}}
			case strings.Contains(query, "UPDATE xiangwan_payment_close_jobs"):
				jobWrites++
				return &fakeRow{values: paymentCloseJobScanValues(completed)}
			default:
				t.Fatalf("unexpected query: %s", query)
				return &fakeRow{}
			}
		},
	}}
	store := fixture.store(t, tx, fixture.now.Add(2*time.Second))
	acquisition := payment.PaymentCloseJobAcquisition{
		Found:           true,
		Job:             acquired,
		Order:           fixture.order,
		InvocationToken: fixture.ownerToken,
		QueryRequest:    &request,
	}
	result, err := store.CompletePaymentCloseJob(
		context.Background(),
		acquisition,
		&observation,
		completion,
	)
	if err != nil || !tx.committed || observationWrites != 1 || jobWrites != 1 ||
		result.JobStatus != payment.PaymentCloseJobStatusCompleted ||
		result.Resolution != payment.PaymentCloseResolutionProviderClosed {
		t.Fatalf("CompletePaymentCloseJob() = %+v, %v tx=%+v", result, err, tx)
	}
}

type paymentCloseJobStoreFixture struct {
	now                        time.Time
	tenantID                   uuid.UUID
	generationID               uuid.UUID
	merchantConfigGenerationID uuid.UUID
	ownerToken                 uuid.UUID
	order                      payment.Order
	job                        payment.PaymentCloseJob
}

func newPaymentCloseJobStoreFixture(t *testing.T) paymentCloseJobStoreFixture {
	t.Helper()
	now := time.Date(2026, time.September, 20, 8, 9, 10, 0, time.UTC)
	order := pendingOrder(now.Add(-2 * time.Minute))
	closedOrder, _, err := payment.CloseOrderUnpaid(order, now.Add(-time.Minute))
	if err != nil {
		t.Fatalf("CloseOrderUnpaid() error = %v", err)
	}
	nextAttemptAt := now.Add(-time.Minute)
	return paymentCloseJobStoreFixture{
		now:                        now,
		tenantID:                   closedOrder.TenantID,
		generationID:               uuid.New(),
		merchantConfigGenerationID: uuid.New(),
		ownerToken:                 uuid.New(),
		order:                      closedOrder,
		job: payment.PaymentCloseJob{
			ID:                uuid.New(),
			TenantID:          closedOrder.TenantID,
			OrderID:           closedOrder.ID,
			PrincipalID:       closedOrder.PrincipalID,
			PaymentAppID:      closedOrder.PaymentAppID,
			PaymentMerchantID: closedOrder.PaymentMerchantID,
			OutTradeNo:        closedOrder.MerchantOrderNo,
			AmountCents:       closedOrder.PayableCents,
			JobStatus:         payment.PaymentCloseJobStatusPending,
			NextAttemptAt:     &nextAttemptAt,
			Version:           1,
			CreatedAt:         nextAttemptAt,
			UpdatedAt:         nextAttemptAt,
		},
	}
}

func (fixture paymentCloseJobStoreFixture) store(
	t *testing.T,
	tx prepayTransaction,
	now time.Time,
) *PaymentCloseJobStore {
	t.Helper()
	store, err := newPaymentCloseJobStore(
		&fakePrepayTransactionStarter{transactions: []prepayTransaction{tx}},
		PaymentCloseJobStoreConfig{
			Now:     func() time.Time { return now },
			NewUUID: func() uuid.UUID { return fixture.ownerToken },
		},
	)
	if err != nil {
		t.Fatalf("newPaymentCloseJobStore() error = %v", err)
	}
	return store
}

func paymentCloseJobScanValues(value payment.PaymentCloseJob) []any {
	var tradeState sql.NullString
	if value.LastTradeState != nil {
		tradeState = sql.NullString{
			String: string(*value.LastTradeState),
			Valid:  true,
		}
	}
	resolution := sql.NullString{}
	if value.Resolution != payment.PaymentCloseResolutionNone {
		resolution = sql.NullString{
			String: string(value.Resolution),
			Valid:  true,
		}
	}
	return []any{
		value.ID,
		value.TenantID,
		value.OrderID,
		value.PrincipalID,
		value.PaymentAppID,
		value.PaymentMerchantID,
		paymentNullUUIDValue(value.MerchantConfigGenerationID),
		value.OutTradeNo,
		value.AmountCents,
		value.JobStatus,
		paymentNullUUID(value.GenerationID),
		paymentNullUUID(value.OwnerToken),
		nullTime(value.LeaseExpiresAt),
		nullTime(value.NextAttemptAt),
		value.AttemptCount,
		tradeState,
		nullString(value.LastErrorClass),
		nullString(value.LastErrorCode),
		nullString(value.LastProviderRequestID),
		resolution,
		nullTime(value.CompletedAt),
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	}
}

func paymentNullUUIDValue(value uuid.UUID) uuid.NullUUID {
	if value == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: value, Valid: true}
}

func providerTradeStatePointerForStore(
	value payment.ProviderTradeState,
) *payment.ProviderTradeState {
	return &value
}
