package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPaymentCloseServiceQueriesBeforeClosingProviderOrder(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 5, 6, 7, 0, time.UTC)
	acquisition := paymentCloseServiceAcquisition(t, now)
	provider := &fakePaymentCloseProvider{
		queryResult: validProviderPaymentQueryResult(
			*acquisition.QueryRequest,
			ProviderTradeStateNotPay,
			now,
		),
		closeResult: ProviderPaymentCloseResult{
			ProviderRequestID: "close-request-1",
		},
	}
	store := &fakePaymentCloseJobStore{
		acquisition: acquisition,
		completedAt: now.Add(2 * time.Second),
	}
	service, err := NewPaymentCloseService(
		store,
		provider,
		&fakeTrustedPaymentConfirmer{},
		func() time.Time { return now.Add(time.Second) },
	)
	if err != nil {
		t.Fatalf("NewPaymentCloseService() error = %v", err)
	}
	job, found, err := service.ProcessNext(
		context.Background(),
		acquisition.Job.TenantID,
		*acquisition.Job.GenerationID,
	)
	if err != nil || !found || provider.queryCalls != 1 ||
		provider.closeCalls != 1 || store.completeCalls != 1 ||
		store.observation == nil ||
		store.observation.TradeState != ProviderTradeStateNotPay ||
		store.completion.Resolution != PaymentCloseResolutionProviderClosed ||
		store.completion.ProviderRequestID != "close-request-1" ||
		job.JobStatus != PaymentCloseJobStatusCompleted {
		t.Fatalf(
			"ProcessNext() job=%+v found=%t err=%v provider=%+v store=%+v",
			job,
			found,
			err,
			provider,
			store,
		)
	}
	if provider.operations != "query,close" ||
		provider.closeRequest != *acquisition.CloseRequest {
		t.Fatalf("provider operations=%q close request=%+v", provider.operations, provider.closeRequest)
	}
}

func TestPaymentCloseServiceRejectsMerchantGenerationMismatchBeforeProvider(
	t *testing.T,
) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 5, 6, 7, 0, time.UTC)
	acquisition := paymentCloseServiceAcquisition(t, now)
	acquisition.Job.MerchantConfigGenerationID = uuid.New()
	provider := &fakePaymentCloseProvider{}
	store := &fakePaymentCloseJobStore{acquisition: acquisition}
	expectedGeneration := uuid.New()
	service, err := NewPaymentCloseServiceForMerchantConfig(
		store,
		provider,
		&fakeTrustedPaymentConfirmer{},
		nil,
		expectedGeneration,
		acquisition.Job.PaymentAppID,
		acquisition.Job.PaymentMerchantID,
	)
	if err != nil {
		t.Fatalf("NewPaymentCloseServiceForMerchantConfig() error = %v", err)
	}
	job, found, err := service.ProcessNext(
		context.Background(),
		acquisition.Job.TenantID,
		*acquisition.Job.GenerationID,
	)
	if !errors.Is(err, ErrPaymentCloseMerchantConfigGenerationMismatch) ||
		!found || job.ID != acquisition.Job.ID || provider.queryCalls != 0 ||
		provider.closeCalls != 0 || store.completeCalls != 0 {
		t.Fatalf(
			"ProcessNext() job=%+v found=%t err=%v provider=%+v store=%+v",
			job,
			found,
			err,
			provider,
			store,
		)
	}

	acquisition.Job.MerchantConfigGenerationID = expectedGeneration
	acquisition.Job.PaymentAppID = "wx-other-app"
	identityStore := &fakePaymentCloseJobStore{acquisition: acquisition}
	identityService, err := NewPaymentCloseServiceForMerchantConfig(
		identityStore,
		provider,
		&fakeTrustedPaymentConfirmer{},
		nil,
		expectedGeneration,
		"wx1234567890abcdef",
		"1900000109",
	)
	if err != nil {
		t.Fatalf("NewPaymentCloseServiceForMerchantConfig(identity) error = %v", err)
	}
	_, found, err = identityService.ProcessNext(
		context.Background(),
		acquisition.Job.TenantID,
		*acquisition.Job.GenerationID,
	)
	if !errors.Is(err, ErrPaymentCloseMerchantIdentityMismatch) || !found ||
		provider.queryCalls != 0 || provider.closeCalls != 0 ||
		identityStore.completeCalls != 0 {
		t.Fatalf("identity ProcessNext() found=%t err=%v provider=%+v store=%+v", found, err, provider, identityStore)
	}
}

func TestPaymentCloseServiceConvergesVerifiedQueryOutcomes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 6, 7, 8, 0, time.UTC)
	tests := []struct {
		name             string
		state            ProviderTradeState
		queryErr         error
		confirmErr       error
		closeErr         error
		resolution       PaymentCloseResolution
		wantStatus       PaymentCloseJobStatus
		wantConfirmation bool
		wantUnpaidClose  bool
		wantObservation  bool
		wantFailure      bool
	}{
		{
			name:             "success",
			state:            ProviderTradeStateSuccess,
			resolution:       PaymentCloseResolutionPaymentConverged,
			wantStatus:       PaymentCloseJobStatusCompleted,
			wantConfirmation: true,
		},
		{
			name:             "confirmation failure is retried",
			state:            ProviderTradeStateSuccess,
			confirmErr:       errors.New("confirmation transaction failed"),
			wantStatus:       PaymentCloseJobStatusRetry,
			wantConfirmation: true,
			wantObservation:  true,
			wantFailure:      true,
		},
		{
			name:            "provider terminal",
			state:           ProviderTradeStateClosed,
			resolution:      PaymentCloseResolutionProviderTerminal,
			wantStatus:      PaymentCloseJobStatusCompleted,
			wantUnpaidClose: true,
		},
		{
			name:            "terminal closure failure is retried",
			state:           ProviderTradeStateClosed,
			closeErr:        errors.New("terminal closure transaction failed"),
			wantStatus:      PaymentCloseJobStatusRetry,
			wantUnpaidClose: true,
			wantObservation: true,
			wantFailure:     true,
		},
		{
			name:            "refund requires review",
			state:           ProviderTradeStateRefund,
			resolution:      PaymentCloseResolutionManualReview,
			wantStatus:      PaymentCloseJobStatusCompleted,
			wantObservation: true,
		},
		{
			name:            "user still paying",
			state:           ProviderTradeStateUserPaying,
			wantStatus:      PaymentCloseJobStatusRetry,
			wantObservation: true,
		},
		{
			name: "provider order absent",
			queryErr: NewProviderFailure(
				ProviderFailureRejected,
				"ORDER_NOT_EXIST",
				context.Canceled,
			),
			resolution:  PaymentCloseResolutionProviderAbsent,
			wantStatus:  PaymentCloseJobStatusCompleted,
			wantFailure: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			acquisition := paymentCloseServiceAcquisition(t, now)
			provider := &fakePaymentCloseProvider{queryErr: test.queryErr}
			if test.queryErr == nil {
				provider.queryResult = validProviderPaymentQueryResult(
					*acquisition.QueryRequest,
					test.state,
					now,
				)
			}
			store := &fakePaymentCloseJobStore{
				acquisition: acquisition,
				completedAt: now.Add(2 * time.Second),
			}
			paidOrder := acquisition.Order
			paidOrder.PaymentStatus = OrderStatusPaidConfirmed
			confirmer := &fakeTrustedPaymentConfirmer{
				result: PaymentConvergence{
					Order:       paidOrder,
					Disposition: PaymentConfirmationDispositionParticipationConfirmed,
				},
				err: test.confirmErr,
				closedResult: PaymentConvergence{
					Order: acquisition.Order,
				},
				closedErr: test.closeErr,
			}
			service, err := NewPaymentCloseService(
				store,
				provider,
				confirmer,
				func() time.Time { return now.Add(time.Second) },
			)
			if err != nil {
				t.Fatalf("NewPaymentCloseService() error = %v", err)
			}
			job, found, err := service.ProcessNext(
				context.Background(),
				acquisition.Job.TenantID,
				*acquisition.Job.GenerationID,
			)
			if err != nil || !found || job.JobStatus != test.wantStatus ||
				store.completion.Resolution != test.resolution ||
				(store.completion.Failure != nil) != test.wantFailure ||
				(confirmer.calls == 1) != test.wantConfirmation ||
				(confirmer.closedCalls == 1) != test.wantUnpaidClose ||
				(store.observation != nil) != test.wantObservation ||
				provider.closeCalls != 0 {
				t.Fatalf(
					"ProcessNext(%s) job=%+v found=%t err=%v provider=%+v store=%+v confirmer=%+v",
					test.name,
					job,
					found,
					err,
					provider,
					store,
					confirmer,
				)
			}
		})
	}
}

func TestPaymentCloseServiceRetriesAmbiguousProviderClose(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 7, 8, 9, 0, time.UTC)
	acquisition := paymentCloseServiceAcquisition(t, now)
	provider := &fakePaymentCloseProvider{
		queryResult: validProviderPaymentQueryResult(
			*acquisition.QueryRequest,
			ProviderTradeStateNotPay,
			now,
		),
		closeErr: NewProviderFailure(
			ProviderFailureTimeout,
			"",
			context.DeadlineExceeded,
		),
	}
	store := &fakePaymentCloseJobStore{
		acquisition: acquisition,
		completedAt: now.Add(2 * time.Second),
	}
	service, err := NewPaymentCloseService(
		store,
		provider,
		&fakeTrustedPaymentConfirmer{},
		func() time.Time { return now.Add(time.Second) },
	)
	if err != nil {
		t.Fatalf("NewPaymentCloseService() error = %v", err)
	}
	job, found, err := service.ProcessNext(
		context.Background(),
		acquisition.Job.TenantID,
		*acquisition.Job.GenerationID,
	)
	if err != nil || !found || job.JobStatus != PaymentCloseJobStatusRetry ||
		store.completion.Failure == nil ||
		store.completion.Resolution != PaymentCloseResolutionNone {
		t.Fatalf("ProcessNext(close timeout) job=%+v found=%t err=%v store=%+v", job, found, err, store)
	}
}

type fakePaymentCloseProvider struct {
	queryResult  ProviderPaymentQueryResult
	queryErr     error
	closeResult  ProviderPaymentCloseResult
	closeErr     error
	queryCalls   int
	closeCalls   int
	operations   string
	closeRequest ProviderPaymentCloseRequest
}

func (provider *fakePaymentCloseProvider) QueryPaymentByOutTradeNo(
	_ context.Context,
	_ ProviderPaymentQueryRequest,
) (ProviderPaymentQueryResult, error) {
	provider.queryCalls++
	provider.operations = "query"
	return provider.queryResult, provider.queryErr
}

func (provider *fakePaymentCloseProvider) ClosePaymentByOutTradeNo(
	_ context.Context,
	request ProviderPaymentCloseRequest,
) (ProviderPaymentCloseResult, error) {
	provider.closeCalls++
	provider.operations += ",close"
	provider.closeRequest = request
	return provider.closeResult, provider.closeErr
}

type fakePaymentCloseJobStore struct {
	acquisition PaymentCloseJobAcquisition
	acquireErr  error
	completedAt time.Time
	completeErr error

	completeCalls int
	observation   *TransactionObservation
	completion    PaymentCloseJobCompletion
	contextErr    error
}

func (store *fakePaymentCloseJobStore) AcquirePaymentCloseJob(
	_ context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	_ uuid.UUID,
	_ string,
	_ string,
) (PaymentCloseJobAcquisition, error) {
	return store.acquisition, store.acquireErr
}

func (store *fakePaymentCloseJobStore) CompletePaymentCloseJob(
	ctx context.Context,
	acquisition PaymentCloseJobAcquisition,
	observation *TransactionObservation,
	completion PaymentCloseJobCompletion,
) (PaymentCloseJob, error) {
	store.completeCalls++
	store.observation = observation
	store.completion = completion
	store.contextErr = ctx.Err()
	if store.completeErr != nil {
		return PaymentCloseJob{}, store.completeErr
	}
	if completion.Resolution == PaymentCloseResolutionNone {
		return RetryPaymentCloseJob(
			acquisition.Job,
			acquisition.InvocationToken,
			completion,
			store.completedAt,
		)
	}
	return CompletePaymentCloseJob(
		acquisition.Job,
		acquisition.InvocationToken,
		completion,
		store.completedAt,
	)
}

func paymentCloseServiceAcquisition(
	t *testing.T,
	now time.Time,
) PaymentCloseJobAcquisition {
	t.Helper()
	ownerToken := uuid.New()
	job := acquiredPaymentCloseJob(t, now, ownerToken)
	order := Order{
		ID:                job.OrderID,
		TenantID:          job.TenantID,
		PrincipalID:       job.PrincipalID,
		PaymentStatus:     OrderStatusClosedUnpaid,
		PaymentAppID:      job.PaymentAppID,
		PaymentMerchantID: job.PaymentMerchantID,
		MerchantOrderNo:   job.OutTradeNo,
		PayableCents:      job.AmountCents,
	}
	queryRequest := ProviderPaymentQueryRequest{
		AppID:       job.PaymentAppID,
		MerchantID:  job.PaymentMerchantID,
		OutTradeNo:  job.OutTradeNo,
		AmountCents: job.AmountCents,
		Currency:    PaymentQueryCurrency,
	}
	closeRequest := ProviderPaymentCloseRequest{
		AppID:      job.PaymentAppID,
		MerchantID: job.PaymentMerchantID,
		OutTradeNo: job.OutTradeNo,
	}
	return PaymentCloseJobAcquisition{
		Found:           true,
		Job:             job,
		Order:           order,
		InvocationToken: ownerToken,
		QueryRequest:    &queryRequest,
		CloseRequest:    &closeRequest,
	}
}
