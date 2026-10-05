package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPaymentQueryServiceConfirmsTrustedSuccessThenConvergesLease(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 2, 3, 4, 0, time.UTC)
	acquisition := paymentQueryServiceAcquisition(t, now)
	providerResult := validProviderPaymentQueryResult(
		*acquisition.ProviderRequest,
		ProviderTradeStateSuccess,
		now,
	)
	cancelledContext, cancel := context.WithCancel(context.Background())
	provider := &fakePaymentQueryProvider{
		result: providerResult,
		cancel: cancel,
	}
	store := &fakePaymentQueryStore{
		acquisition: acquisition,
		convergedResult: PaymentQueryResult{
			Order: Order{
				ID:            acquisition.Order.ID,
				TenantID:      acquisition.Order.TenantID,
				PaymentStatus: OrderStatusPaidConfirmed,
			},
			QueryStatus: PaymentQueryStatusConverged,
			Disposition: PaymentConfirmationDispositionParticipationConfirmed,
		},
	}
	confirmer := &fakeTrustedPaymentConfirmer{
		result: PaymentConvergence{
			Order: Order{
				ID:            acquisition.Order.ID,
				TenantID:      acquisition.Order.TenantID,
				PaymentStatus: OrderStatusPaidConfirmed,
			},
			Disposition: PaymentConfirmationDispositionParticipationConfirmed,
		},
	}
	service, err := NewPaymentQueryService(
		store,
		provider,
		confirmer,
		func() time.Time { return now.Add(time.Second) },
	)
	if err != nil {
		t.Fatalf("NewPaymentQueryService() error = %v", err)
	}
	result, err := service.Query(cancelledContext, PaymentQueryCommand{
		TenantID:     acquisition.Order.TenantID,
		GenerationID: acquisition.Lease.GenerationID,
		OrderID:      acquisition.Order.ID,
		PrincipalID:  acquisition.Lease.PrincipalID,
	})
	if err != nil || result.QueryStatus != PaymentQueryStatusConverged ||
		store.convergedCalls != 1 || store.failedCalls != 0 ||
		store.observedCalls != 0 || confirmer.calls != 1 ||
		store.completionContextErr != nil || confirmer.contextErr != nil {
		t.Fatalf(
			"Query(success) result=%+v err=%v store=%+v confirmer=%+v",
			result,
			err,
			store,
			confirmer,
		)
	}
	command := confirmer.command
	if command.MerchantOrderNo != providerResult.OutTradeNo ||
		command.WeChatTransactionID != providerResult.TransactionID ||
		command.ActualPaidCents != providerResult.AmountCents ||
		command.Observation.PayloadDigest == "" ||
		store.convergence.ProviderRequestID != providerResult.ProviderRequestID {
		t.Fatalf("trusted confirmation = %+v convergence=%+v", command, store.convergence)
	}
}

func TestPaymentQueryServicePersistsSuccessfulObservationWhenConfirmationFails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 2, 3, 4, 0, time.UTC)
	acquisition := paymentQueryServiceAcquisition(t, now)
	providerResult := validProviderPaymentQueryResult(
		*acquisition.ProviderRequest,
		ProviderTradeStateSuccess,
		now,
	)
	store := &fakePaymentQueryStore{
		acquisition: acquisition,
		failedResult: PaymentQueryResult{
			Order: acquisition.Order, QueryStatus: PaymentQueryStatusUnknown,
		},
	}
	confirmer := &fakeTrustedPaymentConfirmer{
		err: errors.New("confirmation transaction failed"),
	}
	service, err := NewPaymentQueryService(
		store,
		&fakePaymentQueryProvider{result: providerResult},
		confirmer,
		func() time.Time { return now.Add(time.Second) },
	)
	if err != nil {
		t.Fatalf("NewPaymentQueryService() error = %v", err)
	}
	result, err := service.Query(context.Background(), PaymentQueryCommand{
		TenantID:     acquisition.Order.TenantID,
		GenerationID: acquisition.Lease.GenerationID,
		OrderID:      acquisition.Order.ID,
		PrincipalID:  acquisition.Lease.PrincipalID,
	})
	class, _ := ProviderFailureMetadata(store.failedError)
	if err != nil || result.QueryStatus != PaymentQueryStatusUnknown ||
		store.failedObservedCalls != 1 || store.failedCalls != 0 ||
		store.failedObservation.TradeState != ProviderTradeStateSuccess ||
		store.failedObservation.PayloadDigest == "" || class == "" {
		t.Fatalf("Query(confirm failure) result=%+v err=%v store=%+v", result, err, store)
	}
	if class, _ := ProviderFailureMetadata(store.failedError); class != ProviderFailureAmbiguous {
		t.Fatalf("confirmation failure class = %q, want %q", class, ProviderFailureAmbiguous)
	}
}

func TestPaymentQueryServicePersistsTerminalObservationWhenClosureFails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 2, 3, 4, 0, time.UTC)
	acquisition := paymentQueryServiceAcquisition(t, now)
	providerResult := validProviderPaymentQueryResult(
		*acquisition.ProviderRequest,
		ProviderTradeStateClosed,
		now,
	)
	store := &fakePaymentQueryStore{
		acquisition: acquisition,
		failedResult: PaymentQueryResult{
			Order: acquisition.Order, QueryStatus: PaymentQueryStatusUnknown,
		},
	}
	confirmer := &fakeTrustedPaymentConfirmer{
		closedErr: errors.New("terminal closure transaction failed"),
	}
	service, err := NewPaymentQueryService(
		store,
		&fakePaymentQueryProvider{result: providerResult},
		confirmer,
		func() time.Time { return now.Add(time.Second) },
	)
	if err != nil {
		t.Fatalf("NewPaymentQueryService() error = %v", err)
	}
	result, err := service.Query(context.Background(), PaymentQueryCommand{
		TenantID:     acquisition.Order.TenantID,
		GenerationID: acquisition.Lease.GenerationID,
		OrderID:      acquisition.Order.ID,
		PrincipalID:  acquisition.Lease.PrincipalID,
	})
	class, _ := ProviderFailureMetadata(store.failedError)
	if err != nil || result.QueryStatus != PaymentQueryStatusUnknown ||
		store.failedObservedCalls != 1 || store.failedCalls != 0 ||
		store.failedObservation.TradeState != ProviderTradeStateClosed ||
		store.failedObservation.PayloadDigest == "" || class != ProviderFailureAmbiguous {
		t.Fatalf("Query(closure failure) result=%+v err=%v store=%+v", result, err, store)
	}
}

func TestPaymentQueryServicePersistsPendingAndAmbiguousOutcomes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 2, 3, 4, 0, time.UTC)
	tests := []struct {
		name           string
		provider       *fakePaymentQueryProvider
		wantObserved   int
		wantFailed     int
		wantTradeState ProviderTradeState
	}{
		{
			name: "not pay",
			provider: &fakePaymentQueryProvider{result: ProviderPaymentQueryResult{
				TradeState: ProviderTradeStateNotPay,
			}},
			wantObserved:   1,
			wantTradeState: ProviderTradeStateNotPay,
		},
		{
			name:       "timeout",
			provider:   &fakePaymentQueryProvider{err: context.DeadlineExceeded},
			wantFailed: 1,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			acquisition := paymentQueryServiceAcquisition(t, now)
			if test.provider.err == nil {
				test.provider.result = validProviderPaymentQueryResult(
					*acquisition.ProviderRequest,
					test.wantTradeState,
					now,
				)
			}
			store := &fakePaymentQueryStore{
				acquisition: acquisition,
				observedResult: PaymentQueryResult{
					Order: acquisition.Order, QueryStatus: PaymentQueryStatusPending,
				},
				failedResult: PaymentQueryResult{
					Order: acquisition.Order, QueryStatus: PaymentQueryStatusUnknown,
				},
			}
			confirmer := &fakeTrustedPaymentConfirmer{}
			service, err := NewPaymentQueryService(
				store,
				test.provider,
				confirmer,
				func() time.Time { return now.Add(time.Second) },
			)
			if err != nil {
				t.Fatalf("NewPaymentQueryService() error = %v", err)
			}
			result, err := service.Query(context.Background(), PaymentQueryCommand{
				TenantID:     acquisition.Order.TenantID,
				GenerationID: acquisition.Lease.GenerationID,
				OrderID:      acquisition.Order.ID,
				PrincipalID:  acquisition.Lease.PrincipalID,
			})
			if err != nil || store.observedCalls != test.wantObserved ||
				store.failedCalls != test.wantFailed || confirmer.calls != 0 {
				t.Fatalf("Query() result=%+v err=%v store=%+v", result, err, store)
			}
			if test.wantObserved == 1 &&
				store.observation.TradeState != test.wantTradeState {
				t.Fatalf("observation = %+v", store.observation)
			}
		})
	}
}

func TestPaymentQueryServiceClosesAuthoritativeUnpaidTerminalOutcome(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 2, 3, 4, 0, time.UTC)
	acquisition := paymentQueryServiceAcquisition(t, now)
	providerResult := validProviderPaymentQueryResult(
		*acquisition.ProviderRequest,
		ProviderTradeStateClosed,
		now,
	)
	closedOrder := acquisition.Order
	closedOrder.PaymentStatus = OrderStatusClosedUnpaid
	store := &fakePaymentQueryStore{
		acquisition: acquisition,
		closedResult: PaymentQueryResult{
			Order: closedOrder, QueryStatus: PaymentQueryStatusClosed,
		},
	}
	confirmer := &fakeTrustedPaymentConfirmer{
		closedResult: PaymentConvergence{Order: closedOrder},
	}
	service, err := NewPaymentQueryService(
		store,
		&fakePaymentQueryProvider{result: providerResult},
		confirmer,
		func() time.Time { return now.Add(time.Second) },
	)
	if err != nil {
		t.Fatalf("NewPaymentQueryService() error = %v", err)
	}
	result, err := service.Query(context.Background(), PaymentQueryCommand{
		TenantID:     acquisition.Order.TenantID,
		GenerationID: acquisition.Lease.GenerationID,
		OrderID:      acquisition.Order.ID,
		PrincipalID:  acquisition.Lease.PrincipalID,
	})
	if err != nil || result.QueryStatus != PaymentQueryStatusClosed ||
		result.Order.PaymentStatus != OrderStatusClosedUnpaid ||
		confirmer.closedCalls != 1 || store.closedCalls != 1 ||
		store.observedCalls != 0 || store.convergedCalls != 0 ||
		confirmer.closeCommand.Observation.TradeState !=
			ProviderTradeStateClosed ||
		store.closedObservation.PayloadDigest == "" ||
		store.closedConvergence.ProviderRequestID !=
			providerResult.ProviderRequestID {
		t.Fatalf(
			"Query(closed) result=%+v err=%v store=%+v confirmer=%+v",
			result,
			err,
			store,
			confirmer,
		)
	}
}

func TestPaymentQueryServiceReturnsLeasedLocalStateWithoutProviderCall(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 2, 3, 4, 0, time.UTC)
	acquisition := paymentQueryServiceAcquisition(t, now)
	acquisition.ProviderRequest = nil
	acquisition.InvocationToken = uuid.Nil
	store := &fakePaymentQueryStore{acquisition: acquisition}
	provider := &fakePaymentQueryProvider{}
	service, err := NewPaymentQueryService(
		store,
		provider,
		&fakeTrustedPaymentConfirmer{},
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("NewPaymentQueryService() error = %v", err)
	}
	result, err := service.Query(context.Background(), PaymentQueryCommand{
		TenantID:     acquisition.Order.TenantID,
		GenerationID: acquisition.Lease.GenerationID,
		OrderID:      acquisition.Order.ID,
		PrincipalID:  acquisition.Lease.PrincipalID,
	})
	if err != nil || result.QueryStatus != PaymentQueryStatusInProgress ||
		provider.calls != 0 {
		t.Fatalf("Query(local lease) = %+v, %v provider=%+v", result, err, provider)
	}
}

func TestPaymentQueryCachedNotPayDoesNotAuthorizeAnotherPayment(t *testing.T) {
	t.Parallel()
	acquisition := paymentQueryServiceAcquisition(t, time.Now().UTC())
	acquisition.Order.PaymentStatus = OrderStatusPending
	notPay := ProviderTradeStateNotPay
	acquisition.Lease.QueryStatus = PaymentQueryStatusPending
	acquisition.Lease.LastTradeState = &notPay
	if paymentQueryResultFromAcquisition(acquisition).RetryPaymentAllowed {
		t.Fatal("cached NOTPAY must not authorize another payment-sheet invocation")
	}
	userPaying := ProviderTradeStateUserPaying
	acquisition.Lease.LastTradeState = &userPaying
	if paymentQueryResultFromAcquisition(acquisition).RetryPaymentAllowed {
		t.Fatal("cached USERPAYING must not permit a retry")
	}
}

func paymentQueryServiceAcquisition(
	t *testing.T,
	now time.Time,
) PaymentQueryAcquisition {
	t.Helper()
	lease := validPaymentQueryLease(t, now)
	request := providerQueryRequestFromLease(lease)
	return PaymentQueryAcquisition{
		Lease: lease,
		Order: Order{
			ID:                lease.OrderID,
			TenantID:          lease.TenantID,
			PrincipalID:       lease.PrincipalID,
			PaymentStatus:     OrderStatusUnknown,
			PaymentAppID:      lease.PaymentAppID,
			PaymentMerchantID: lease.PaymentMerchantID,
			MerchantOrderNo:   lease.OutTradeNo,
			PayableCents:      lease.AmountCents,
		},
		InvocationToken: *lease.OwnerToken,
		ProviderRequest: &request,
	}
}

type fakePaymentQueryProvider struct {
	result ProviderPaymentQueryResult
	err    error
	cancel context.CancelFunc
	calls  int
}

func (provider *fakePaymentQueryProvider) QueryPaymentByOutTradeNo(
	_ context.Context,
	_ ProviderPaymentQueryRequest,
) (ProviderPaymentQueryResult, error) {
	provider.calls++
	if provider.cancel != nil {
		provider.cancel()
	}
	return provider.result, provider.err
}

type fakePaymentQueryStore struct {
	acquisition     PaymentQueryAcquisition
	acquisitionErr  error
	observedResult  PaymentQueryResult
	failedResult    PaymentQueryResult
	convergedResult PaymentQueryResult
	closedResult    PaymentQueryResult

	observedCalls        int
	failedCalls          int
	failedObservedCalls  int
	convergedCalls       int
	closedCalls          int
	observation          TransactionObservation
	failedObservation    TransactionObservation
	failedError          error
	convergence          PaymentConvergence
	closedObservation    TransactionObservation
	closedConvergence    PaymentConvergence
	completionContextErr error
}

func (store *fakePaymentQueryStore) AcquirePaymentQuery(
	_ context.Context,
	_ PaymentQueryCommand,
) (PaymentQueryAcquisition, error) {
	return store.acquisition, store.acquisitionErr
}

func (store *fakePaymentQueryStore) CompleteObservedPaymentQuery(
	ctx context.Context,
	_ PaymentQueryAcquisition,
	observation TransactionObservation,
) (PaymentQueryResult, error) {
	store.observedCalls++
	store.observation = observation
	store.completionContextErr = ctx.Err()
	return store.observedResult, nil
}

func (store *fakePaymentQueryStore) CompleteFailedPaymentQuery(
	ctx context.Context,
	_ PaymentQueryAcquisition,
	_ error,
) (PaymentQueryResult, error) {
	store.failedCalls++
	store.completionContextErr = ctx.Err()
	return store.failedResult, nil
}

func (store *fakePaymentQueryStore) CompleteFailedPaymentQueryWithObservation(
	ctx context.Context,
	_ PaymentQueryAcquisition,
	observation TransactionObservation,
	failure error,
) (PaymentQueryResult, error) {
	store.failedObservedCalls++
	store.failedObservation = observation
	store.failedError = failure
	store.completionContextErr = ctx.Err()
	return store.failedResult, nil
}

func (store *fakePaymentQueryStore) CompleteConvergedPaymentQuery(
	ctx context.Context,
	_ PaymentQueryAcquisition,
	convergence PaymentConvergence,
) (PaymentQueryResult, error) {
	store.convergedCalls++
	store.convergence = convergence
	store.completionContextErr = ctx.Err()
	return store.convergedResult, nil
}

func (store *fakePaymentQueryStore) CompleteClosedPaymentQuery(
	ctx context.Context,
	_ PaymentQueryAcquisition,
	observation TransactionObservation,
	convergence PaymentConvergence,
) (PaymentQueryResult, error) {
	store.closedCalls++
	store.closedObservation = observation
	store.closedConvergence = convergence
	store.completionContextErr = ctx.Err()
	return store.closedResult, nil
}

type fakeTrustedPaymentConfirmer struct {
	result     PaymentConvergence
	err        error
	calls      int
	command    TrustedPaymentConfirmation
	contextErr error

	rejectedCalls  int
	rejectedErr    error
	rejectedResult PaymentConvergence
	closedResult   PaymentConvergence
	closedErr      error
	closedCalls    int
	closeCommand   TrustedUnpaidPaymentClosure
}

func (confirmer *fakeTrustedPaymentConfirmer) ConfirmTrustedPayment(
	ctx context.Context,
	command TrustedPaymentConfirmation,
) (PaymentConvergence, error) {
	confirmer.calls++
	confirmer.command = command
	confirmer.contextErr = ctx.Err()
	return confirmer.result, confirmer.err
}

func (confirmer *fakeTrustedPaymentConfirmer) CloseTrustedUnpaidPayment(
	ctx context.Context,
	command TrustedUnpaidPaymentClosure,
) (PaymentConvergence, error) {
	confirmer.closedCalls++
	confirmer.closeCommand = command
	confirmer.contextErr = ctx.Err()
	return confirmer.closedResult, confirmer.closedErr
}

func (confirmer *fakeTrustedPaymentConfirmer) CloseRejectedPrepay(ctx context.Context, _ RejectedPrepayClosure) (PaymentConvergence, error) {
	confirmer.rejectedCalls++
	confirmer.contextErr = ctx.Err()
	return confirmer.rejectedResult, confirmer.rejectedErr
}

func TestPaymentQueryMissingOrderRequiresDurableRejection(t *testing.T) {
	for _, code := range []string{"ORDER_NOT_EXIST", "SYSTEM_ERROR"} {
		t.Run(code, func(t *testing.T) {
			now := time.Now().UTC()
			acquisition := paymentQueryServiceAcquisition(t, now)
			store := &fakePaymentQueryStore{acquisition: acquisition}
			confirmer := &fakeTrustedPaymentConfirmer{rejectedErr: ErrPrepayRejectionUnproven}
			provider := &fakePaymentQueryProvider{err: NewProviderFailure(ProviderFailureRejected, code, nil)}
			service, _ := NewPaymentQueryService(store, provider, confirmer, func() time.Time { return now })
			_, err := service.Query(context.Background(), PaymentQueryCommand{TenantID: acquisition.Order.TenantID, OrderID: acquisition.Order.ID, PrincipalID: acquisition.Lease.PrincipalID, GenerationID: acquisition.Lease.GenerationID})
			if err != nil || (confirmer.rejectedCalls == 1) != (code == "ORDER_NOT_EXIST") || store.failedCalls != 1 || store.closedCalls != 0 {
				t.Fatalf("err=%v recovery_calls=%d failed=%d fabricated_closed=%d", err, confirmer.rejectedCalls, store.failedCalls, store.closedCalls)
			}
		})
	}
}
