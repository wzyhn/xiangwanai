package payment

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPaymentQueryAllowsRetryOnlyAfterTrustedNotPay(t *testing.T) {
	t.Parallel()
	notPay := ProviderTradeStateNotPay
	userPaying := ProviderTradeStateUserPaying
	order := Order{PaymentStatus: OrderStatusPending}
	lease := PaymentQueryLease{QueryStatus: PaymentQueryStatusPending, LastTradeState: &notPay}
	if !PaymentQueryAllowsRetry(order, lease) {
		t.Fatal("verified NOTPAY should permit exact prepay replay")
	}
	lease.LastTradeState = &userPaying
	if PaymentQueryAllowsRetry(order, lease) {
		t.Fatal("USERPAYING must not reopen the payment sheet")
	}
	lease.LastTradeState = &notPay
	lease.QueryStatus = PaymentQueryStatusUnknown
	if PaymentQueryAllowsRetry(order, lease) {
		t.Fatal("unknown query must not reopen the payment sheet")
	}
	lease.QueryStatus = PaymentQueryStatusPending
	order.PaymentStatus = OrderStatusUnknown
	if PaymentQueryAllowsRetry(order, lease) {
		t.Fatal("unknown Order must not reopen the payment sheet")
	}
}

func TestPaymentQueryLeaseLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 1, 2, 3, 0, time.UTC)
	lease, err := NewPaymentQueryLease(CreatePaymentQueryLeaseCommand{
		ID:                uuid.New(),
		TenantID:          uuid.New(),
		OrderID:           uuid.New(),
		PrincipalID:       uuid.New(),
		GenerationID:      uuid.New(),
		PaymentAppID:      "wx-xiangwan-app",
		PaymentMerchantID: "1900000109",
		OutTradeNo:        "XW-QUERY-ORDER-0001",
		AmountCents:       9900,
		OwnerToken:        uuid.New(),
		Now:               now,
	})
	if err != nil || lease.QueryStatus != PaymentQueryStatusInProgress ||
		lease.OwnerToken == nil || lease.LeaseExpiresAt == nil ||
		!lease.LeaseExpiresAt.Equal(now.Add(PaymentQueryLeaseDuration)) {
		t.Fatalf("NewPaymentQueryLease() = %+v, %v", lease, err)
	}
	if _, err := AcquirePaymentQueryLease(
		lease,
		uuid.New(),
		uuid.New(),
		now.Add(time.Second),
	); !errors.Is(err, ErrPaymentQueryLeaseActive) {
		t.Fatalf("active lease error = %v", err)
	}

	request := providerQueryRequestFromLease(lease)
	result := validProviderPaymentQueryResult(request, ProviderTradeStateNotPay, now)
	acquisition := PaymentQueryAcquisition{
		Lease:           lease,
		Order:           Order{ID: lease.OrderID, TenantID: lease.TenantID},
		InvocationToken: *lease.OwnerToken,
		ProviderRequest: &request,
	}
	observation, err := NewTransactionObservation(acquisition, result, now.Add(time.Second))
	if err != nil {
		t.Fatalf("NewTransactionObservation() error = %v", err)
	}
	pending, err := CompletePaymentQueryObserved(
		lease,
		*lease.OwnerToken,
		observation,
		now.Add(time.Second),
	)
	if err != nil || pending.QueryStatus != PaymentQueryStatusPending ||
		pending.NextQueryAt == nil ||
		!pending.NextQueryAt.Equal(now.Add(time.Second+PaymentQueryRetryDelay)) ||
		pending.LastTradeState == nil ||
		*pending.LastTradeState != ProviderTradeStateNotPay {
		t.Fatalf("CompletePaymentQueryObserved() = %+v, %v", pending, err)
	}
	if _, err := AcquirePaymentQueryLease(
		pending,
		uuid.New(),
		uuid.New(),
		now.Add(2*time.Second),
	); !errors.Is(err, ErrPaymentQueryNotDue) {
		t.Fatalf("cooldown error = %v", err)
	}

	newGeneration := uuid.New()
	newOwner := uuid.New()
	reacquired, err := AcquirePaymentQueryLease(
		pending,
		newGeneration,
		newOwner,
		*pending.NextQueryAt,
	)
	if err != nil || reacquired.GenerationID != newGeneration ||
		reacquired.OwnerToken == nil || *reacquired.OwnerToken != newOwner ||
		reacquired.QueryStatus != PaymentQueryStatusInProgress ||
		reacquired.LastTradeState != nil {
		t.Fatalf("AcquirePaymentQueryLease(due) = %+v, %v", reacquired, err)
	}
	converged, err := CompletePaymentQueryConverged(
		reacquired,
		newOwner,
		"provider-request-2",
		reacquired.UpdatedAt.Add(time.Second),
	)
	if err != nil || converged.QueryStatus != PaymentQueryStatusConverged ||
		converged.CompletedAt == nil || converged.LastTradeState == nil ||
		*converged.LastTradeState != ProviderTradeStateSuccess {
		t.Fatalf("CompletePaymentQueryConverged() = %+v, %v", converged, err)
	}
	if _, err := AcquirePaymentQueryLease(
		converged,
		uuid.New(),
		uuid.New(),
		now.Add(time.Minute),
	); !errors.Is(err, ErrPaymentQueryTerminal) {
		t.Fatalf("terminal lease error = %v", err)
	}
}

func TestPaymentQueryFailureBecomesRetryableUnknown(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 1, 2, 3, 0, time.UTC)
	lease := validPaymentQueryLease(t, now)
	unknown, err := CompletePaymentQueryFailed(
		lease,
		*lease.OwnerToken,
		NewProviderFailure(ProviderFailureTimeout, "", contextDeadlineError{}),
		now.Add(time.Second),
	)
	if err != nil || unknown.QueryStatus != PaymentQueryStatusUnknown ||
		unknown.LastErrorClass == nil || *unknown.LastErrorClass != ProviderFailureTimeout ||
		unknown.NextQueryAt == nil || unknown.OwnerToken != nil {
		t.Fatalf("CompletePaymentQueryFailed() = %+v, %v", unknown, err)
	}
}

func TestPaymentQueryTerminalUnpaidOutcomeClosesLease(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 1, 2, 3, 0, time.UTC)
	lease := validPaymentQueryLease(t, now)
	request := providerQueryRequestFromLease(lease)
	acquisition := PaymentQueryAcquisition{
		Lease:           lease,
		Order:           Order{ID: lease.OrderID, TenantID: lease.TenantID},
		InvocationToken: *lease.OwnerToken,
		ProviderRequest: &request,
	}
	for _, state := range []ProviderTradeState{
		ProviderTradeStateClosed,
		ProviderTradeStateRevoked,
		ProviderTradeStatePayError,
	} {
		state := state
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()
			observation, err := NewTransactionObservation(
				acquisition,
				validProviderPaymentQueryResult(request, state, now),
				now.Add(time.Second),
			)
			if err != nil {
				t.Fatalf("NewTransactionObservation() error = %v", err)
			}
			closed, err := CompletePaymentQueryClosed(
				lease,
				*lease.OwnerToken,
				observation,
				now.Add(time.Second),
			)
			if err != nil || closed.QueryStatus != PaymentQueryStatusClosed ||
				closed.OwnerToken != nil || closed.LeaseExpiresAt != nil ||
				closed.NextQueryAt != nil || closed.CompletedAt == nil ||
				closed.LastTradeState == nil ||
				*closed.LastTradeState != state {
				t.Fatalf("CompletePaymentQueryClosed() = %+v, %v", closed, err)
			}
			if _, err = AcquirePaymentQueryLease(
				closed,
				uuid.New(),
				uuid.New(),
				now.Add(time.Minute),
			); !errors.Is(err, ErrPaymentQueryTerminal) {
				t.Fatalf("closed lease acquisition error = %v", err)
			}
			if _, err = CompletePaymentQueryObserved(
				lease,
				*lease.OwnerToken,
				observation,
				now.Add(time.Second),
			); !errors.Is(err, ErrInvalidPaymentQuery) {
				t.Fatalf("terminal observation remained retryable: %v", err)
			}
		})
	}
}

func TestTransactionObservationRejectsProviderFactMismatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 19, 1, 2, 3, 0, time.UTC)
	lease := validPaymentQueryLease(t, now)
	request := providerQueryRequestFromLease(lease)
	acquisition := PaymentQueryAcquisition{
		Lease:           lease,
		Order:           Order{ID: lease.OrderID, TenantID: lease.TenantID},
		InvocationToken: *lease.OwnerToken,
		ProviderRequest: &request,
	}
	result := validProviderPaymentQueryResult(request, ProviderTradeStateSuccess, now)
	observation, err := NewTransactionObservation(acquisition, result, now.Add(time.Second))
	if err != nil || ValidateTransactionObservation(observation) != nil ||
		len(observation.PayloadDigest) != 64 {
		t.Fatalf("observation = %+v, %v", observation, err)
	}

	tampered := observation
	tampered.AmountCents++
	if !errors.Is(ValidateTransactionObservation(tampered), ErrInvalidPaymentQuery) {
		t.Fatal("tampered observation passed digest/amount validation")
	}
	mismatched := result
	mismatched.MerchantID = "1900000110"
	if !errors.Is(
		ValidateProviderPaymentQueryResult(mismatched, request),
		ErrInvalidPaymentQuery,
	) {
		t.Fatal("mismatched merchant identity passed validation")
	}
	incomplete := result
	incomplete.TransactionID = ""
	if !errors.Is(
		ValidateProviderPaymentQueryResult(incomplete, request),
		ErrInvalidPaymentQuery,
	) {
		t.Fatal("SUCCESS without transaction id passed validation")
	}
	contradictoryTerminal := validProviderPaymentQueryResult(
		request,
		ProviderTradeStateClosed,
		now,
	)
	terminalSuccessAt := now.UTC()
	contradictoryTerminal.TransactionID = "unexpected-provider-transaction"
	contradictoryTerminal.SuccessAt = &terminalSuccessAt
	if !errors.Is(
		ValidateProviderPaymentQueryResult(contradictoryTerminal, request),
		ErrInvalidPaymentQuery,
	) {
		t.Fatal("terminal unpaid state with payment facts passed validation")
	}
}

func validPaymentQueryLease(t *testing.T, now time.Time) PaymentQueryLease {
	t.Helper()
	lease, err := NewPaymentQueryLease(CreatePaymentQueryLeaseCommand{
		ID:                uuid.New(),
		TenantID:          uuid.New(),
		OrderID:           uuid.New(),
		PrincipalID:       uuid.New(),
		GenerationID:      uuid.New(),
		PaymentAppID:      "wx-xiangwan-app",
		PaymentMerchantID: "1900000109",
		OutTradeNo:        "XW-QUERY-ORDER-0001",
		AmountCents:       9900,
		OwnerToken:        uuid.New(),
		Now:               now,
	})
	if err != nil {
		t.Fatalf("NewPaymentQueryLease() error = %v", err)
	}
	return lease
}

func providerQueryRequestFromLease(lease PaymentQueryLease) ProviderPaymentQueryRequest {
	return ProviderPaymentQueryRequest{
		AppID:       lease.PaymentAppID,
		MerchantID:  lease.PaymentMerchantID,
		OutTradeNo:  lease.OutTradeNo,
		AmountCents: lease.AmountCents,
		Currency:    lease.Currency,
	}
}

func validProviderPaymentQueryResult(
	request ProviderPaymentQueryRequest,
	state ProviderTradeState,
	successAt time.Time,
) ProviderPaymentQueryResult {
	result := ProviderPaymentQueryResult{
		AppID:             request.AppID,
		MerchantID:        request.MerchantID,
		OutTradeNo:        request.OutTradeNo,
		TradeType:         PaymentQueryTradeType,
		TradeState:        state,
		AmountCents:       request.AmountCents,
		Currency:          request.Currency,
		ProviderRequestID: "provider-request-1",
	}
	if state == ProviderTradeStateSuccess {
		paidAt := successAt.UTC()
		result.TransactionID = "wechat-transaction-1"
		result.SuccessAt = &paidAt
	}
	return result
}

type contextDeadlineError struct{}

func (contextDeadlineError) Error() string { return "deadline" }
