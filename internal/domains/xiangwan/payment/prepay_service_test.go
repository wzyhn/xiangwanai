package payment

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPrepayServiceCallsProviderOnlyForLeaseWinner(t *testing.T) {
	command := validCreatePrepayAttemptCommand()
	attempt, _ := newValidPaymentAttempt(t, time.Now().UTC())
	request := ProviderPrepayRequest{
		AppID:        attempt.PaymentAppID,
		MerchantID:   attempt.PaymentMerchantID,
		Description:  "享玩活动报名",
		OutTradeNo:   attempt.OutTradeNo,
		NotifyURL:    "https://xiangwan.example.com/notify",
		OpenID:       "openid-1",
		AmountCents:  attempt.AmountCents,
		TimeExpireAt: time.Now().Add(time.Minute),
	}
	store := &fakePrepayStore{acquisition: PrepayAcquisition{
		Attempt:         attempt,
		HoldExpiresAt:   request.TimeExpireAt,
		InvocationToken: *attempt.OwnerToken,
		ProviderRequest: &request,
	}}
	provider := &fakePrepayProvider{result: validProviderPrepayResult()}
	store.result = PrepayAttemptResult{Attempt: attempt, HoldExpiresAt: request.TimeExpireAt}
	service, err := NewPrepayService(store, provider, &fakeTrustedPaymentConfirmer{})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	_, err = service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if provider.calls != 1 || store.completeCalls != 1 ||
		provider.request.OpenID != "openid-1" {
		t.Fatalf("provider/store calls mismatch: %+v %+v", provider, store)
	}
}

func TestPrepayServiceReplaysWithoutProviderCall(t *testing.T) {
	command := validCreatePrepayAttemptCommand()
	attempt, owner := newValidPaymentAttempt(t, time.Now().UTC())
	ready, err := CompletePaymentAttemptReady(
		attempt,
		owner,
		validProviderPrepayResult(),
		time.Now().UTC().Add(time.Second),
	)
	if err != nil {
		t.Fatalf("ready attempt: %v", err)
	}
	store := &fakePrepayStore{acquisition: PrepayAcquisition{
		Attempt:       ready,
		HoldExpiresAt: time.Now().Add(time.Minute),
	}}
	provider := &fakePrepayProvider{}
	service, _ := NewPrepayService(store, provider, &fakeTrustedPaymentConfirmer{})
	result, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if provider.calls != 0 || store.completeCalls != 0 || result.Parameters == nil {
		t.Fatalf("unexpected replay: result=%+v provider=%+v", result, provider)
	}
}

func TestPrepayServicePersistsAmbiguousProviderOutcome(t *testing.T) {
	command := validCreatePrepayAttemptCommand()
	attempt, _ := newValidPaymentAttempt(t, time.Now().UTC())
	request := validProviderPrepayRequestForService(attempt)
	providerFailure := NewProviderFailure(
		ProviderFailureTimeout,
		"",
		context.DeadlineExceeded,
	)
	store := &fakePrepayStore{acquisition: PrepayAcquisition{
		Attempt:         attempt,
		HoldExpiresAt:   request.TimeExpireAt,
		InvocationToken: *attempt.OwnerToken,
		ProviderRequest: &request,
	}}
	store.result = PrepayAttemptResult{Attempt: PaymentAttempt{
		AttemptStatus: PrepayAttemptStatusUnknown,
	}}
	provider := &fakePrepayProvider{err: providerFailure}
	service, _ := NewPrepayService(store, provider, &fakeTrustedPaymentConfirmer{})
	result, err := service.Create(context.Background(), command)
	if err != nil {
		t.Fatalf("ambiguous create: %v", err)
	}
	if result.Attempt.AttemptStatus != PrepayAttemptStatusUnknown ||
		!errors.Is(store.providerErr, context.DeadlineExceeded) {
		t.Fatalf("provider outcome was not persisted: %+v %v", result, store.providerErr)
	}
}

func TestPrepayServiceDetachesCompletionFromClientCancellation(t *testing.T) {
	command := validCreatePrepayAttemptCommand()
	attempt, _ := newValidPaymentAttempt(t, time.Now().UTC())
	request := validProviderPrepayRequestForService(attempt)
	ctx, cancel := context.WithCancel(context.Background())
	store := &fakePrepayStore{acquisition: PrepayAcquisition{
		Attempt:         attempt,
		HoldExpiresAt:   request.TimeExpireAt,
		InvocationToken: *attempt.OwnerToken,
		ProviderRequest: &request,
	}}
	provider := &fakePrepayProvider{
		err: NewProviderFailure(
			ProviderFailureAmbiguous,
			"",
			context.Canceled,
		),
		afterCall: cancel,
	}
	service, _ := NewPrepayService(store, provider, &fakeTrustedPaymentConfirmer{})

	_, err := service.Create(ctx, command)
	if err != nil {
		t.Fatalf("cancelled client completion: %v", err)
	}
	if store.completeCalls != 1 || store.completeContextErr != nil {
		t.Fatalf(
			"completion calls=%d context error=%v",
			store.completeCalls,
			store.completeContextErr,
		)
	}
}

type fakePrepayStore struct {
	acquisition        PrepayAcquisition
	result             PrepayAttemptResult
	err                error
	completeErr        error
	completeCalls      int
	providerErr        error
	completeContextErr error
}

func (store *fakePrepayStore) AcquirePrepayAttempt(
	context.Context,
	CreatePrepayAttemptCommand,
) (PrepayAcquisition, error) {
	return store.acquisition, store.err
}

func (store *fakePrepayStore) CompletePrepayAttempt(
	ctx context.Context,
	_ PrepayAcquisition,
	_ ProviderPrepayResult,
	providerErr error,
) (PrepayAttemptResult, error) {
	store.completeCalls++
	store.providerErr = providerErr
	store.completeContextErr = ctx.Err()
	return store.result, store.completeErr
}

type fakePrepayProvider struct {
	result    ProviderPrepayResult
	err       error
	request   ProviderPrepayRequest
	calls     int
	afterCall func()
}

func (provider *fakePrepayProvider) CreateMiniProgramPrepay(
	_ context.Context,
	request ProviderPrepayRequest,
) (ProviderPrepayResult, error) {
	provider.calls++
	provider.request = request
	if provider.afterCall != nil {
		provider.afterCall()
	}
	return provider.result, provider.err
}

func validProviderPrepayRequestForService(attempt PaymentAttempt) ProviderPrepayRequest {
	return ProviderPrepayRequest{
		AppID:        attempt.PaymentAppID,
		MerchantID:   attempt.PaymentMerchantID,
		Description:  "享玩活动报名",
		OutTradeNo:   attempt.OutTradeNo,
		NotifyURL:    "https://xiangwan.example.com/notify",
		OpenID:       "openid-1",
		AmountCents:  attempt.AmountCents,
		TimeExpireAt: time.Now().Add(time.Minute),
	}
}

func TestPrepayServiceClosesOnlyPersistedDefinitiveRejection(t *testing.T) {
	for _, tc := range []struct {
		name, class, code               string
		closeErr, errorAfterPersistence error
		wantClose, wantRejected         bool
	}{
		{name: "merchant rejected", class: ProviderFailureRejected, code: "NO_AUTH", wantClose: true, wantRejected: true},
		{name: "rejection after takeover", class: ProviderFailureRejected, code: "NO_AUTH", closeErr: ErrPrepayRejectionUnproven, wantClose: true},
		{name: "ambiguous duplicate", class: ProviderFailureAmbiguous, code: "OUT_TRADE_NO_USED"},
		{name: "timeout", class: ProviderFailureTimeout},
		{name: "unpersisted rejection", class: ProviderFailureRejected, code: "NO_AUTH", errorAfterPersistence: errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := validCreatePrepayAttemptCommand()
			attempt, _ := newValidPaymentAttempt(t, time.Now().UTC())
			request := validProviderPrepayRequestForService(attempt)
			store := &fakePrepayStore{acquisition: PrepayAcquisition{Attempt: attempt, InvocationToken: *attempt.OwnerToken, ProviderRequest: &request}, completeErr: tc.errorAfterPersistence}
			closer := &fakeTrustedPaymentConfirmer{rejectedErr: tc.closeErr, rejectedResult: PaymentConvergence{Order: Order{ID: command.OrderID, TenantID: command.TenantID, PrincipalID: command.PrincipalID, PaymentStatus: OrderStatusClosedUnpaid}}}
			service, _ := NewPrepayService(store, &fakePrepayProvider{err: NewProviderFailure(tc.class, tc.code, nil)}, closer)
			_, err := service.Create(context.Background(), command)
			if (closer.rejectedCalls == 1) != tc.wantClose || errors.Is(err, ErrPrepayRejected) != tc.wantRejected || store.completeCalls != 1 {
				t.Fatalf("err=%v close_calls=%d persist_calls=%d", err, closer.rejectedCalls, store.completeCalls)
			}
		})
	}
}
