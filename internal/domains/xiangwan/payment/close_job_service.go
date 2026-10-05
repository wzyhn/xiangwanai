package payment

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

const paymentCloseCompletionTimeout = 20 * time.Second

var ErrInvalidPaymentCloseService = errors.New("invalid xiangwan payment close service")
var ErrPaymentCloseMerchantConfigGenerationMismatch = errors.New(
	"xiangwan payment close job merchant config generation does not match worker",
)
var ErrPaymentCloseMerchantIdentityMismatch = errors.New(
	"xiangwan payment close job merchant identity does not match worker",
)

type PaymentCloseService struct {
	store                      PaymentCloseJobStore
	provider                   PaymentCloseProviderPort
	confirmer                  TrustedPaymentConfirmer
	now                        func() time.Time
	merchantConfigGenerationID uuid.UUID
	paymentAppID               string
	paymentMerchantID          string
}

func NewPaymentCloseService(
	store PaymentCloseJobStore,
	provider PaymentCloseProviderPort,
	confirmer TrustedPaymentConfirmer,
	now func() time.Time,
) (*PaymentCloseService, error) {
	return newPaymentCloseService(
		store,
		provider,
		confirmer,
		now,
		uuid.Nil,
		"",
		"",
	)
}

// NewPaymentCloseServiceForMerchantConfig binds a long-lived close worker to
// the merchant configuration generation that built its provider. Legacy jobs
// with a NULL snapshot remain processable during the static-credential drain.
func NewPaymentCloseServiceForMerchantConfig(
	store PaymentCloseJobStore,
	provider PaymentCloseProviderPort,
	confirmer TrustedPaymentConfirmer,
	now func() time.Time,
	merchantConfigGenerationID uuid.UUID,
	paymentAppID string,
	paymentMerchantID string,
) (*PaymentCloseService, error) {
	if merchantConfigGenerationID == uuid.Nil || paymentAppID == "" ||
		paymentMerchantID == "" {
		return nil, ErrInvalidPaymentCloseService
	}
	return newPaymentCloseService(
		store,
		provider,
		confirmer,
		now,
		merchantConfigGenerationID,
		paymentAppID,
		paymentMerchantID,
	)
}

func newPaymentCloseService(
	store PaymentCloseJobStore,
	provider PaymentCloseProviderPort,
	confirmer TrustedPaymentConfirmer,
	now func() time.Time,
	merchantConfigGenerationID uuid.UUID,
	paymentAppID string,
	paymentMerchantID string,
) (*PaymentCloseService, error) {
	if store == nil || provider == nil || confirmer == nil {
		return nil, ErrInvalidPaymentCloseService
	}
	if now == nil {
		now = time.Now
	}
	return &PaymentCloseService{
		store:                      store,
		provider:                   provider,
		confirmer:                  confirmer,
		now:                        now,
		merchantConfigGenerationID: merchantConfigGenerationID,
		paymentAppID:               paymentAppID,
		paymentMerchantID:          paymentMerchantID,
	}, nil
}

func (service *PaymentCloseService) ProcessNext(
	ctx context.Context,
	tenantID uuid.UUID,
	generationID uuid.UUID,
) (PaymentCloseJob, bool, error) {
	if service == nil || service.store == nil || service.provider == nil ||
		service.confirmer == nil || service.now == nil || ctx == nil ||
		tenantID == uuid.Nil || generationID == uuid.Nil {
		return PaymentCloseJob{}, false, ErrInvalidPaymentCloseService
	}
	acquisition, err := service.store.AcquirePaymentCloseJob(
		ctx,
		tenantID,
		generationID,
		service.merchantConfigGenerationID,
		service.paymentAppID,
		service.paymentMerchantID,
	)
	if err != nil || !acquisition.Found {
		return PaymentCloseJob{}, false, err
	}
	if service.merchantConfigGenerationID != uuid.Nil &&
		acquisition.Job.MerchantConfigGenerationID != uuid.Nil &&
		acquisition.Job.MerchantConfigGenerationID != service.merchantConfigGenerationID {
		return acquisition.Job, true, ErrPaymentCloseMerchantConfigGenerationMismatch
	}
	if service.paymentAppID != "" &&
		(acquisition.Job.PaymentAppID != service.paymentAppID ||
			acquisition.Job.PaymentMerchantID != service.paymentMerchantID) {
		return acquisition.Job, true, ErrPaymentCloseMerchantIdentityMismatch
	}
	if acquisition.QueryRequest == nil {
		return acquisition.Job, true, nil
	}

	providerResult, providerErr := service.provider.QueryPaymentByOutTradeNo(
		ctx,
		*acquisition.QueryRequest,
	)
	if providerErr == nil {
		if validateErr := ValidateProviderPaymentQueryResult(
			providerResult,
			*acquisition.QueryRequest,
		); validateErr != nil {
			providerErr = NewProviderFailure(
				ProviderFailureInvalidResponse,
				"",
				validateErr,
			)
		}
	}
	observedAt := service.now().UTC()
	if observedAt.IsZero() {
		return PaymentCloseJob{}, true, ErrInvalidPaymentCloseService
	}
	completionContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		paymentCloseCompletionTimeout,
	)
	defer cancel()
	if providerErr != nil {
		resolution := PaymentCloseResolutionNone
		if providerOrderAbsent(providerErr) {
			resolution = PaymentCloseResolutionProviderAbsent
		}
		return service.complete(
			completionContext,
			acquisition,
			nil,
			PaymentCloseJobCompletion{
				Resolution: resolution,
				Failure:    providerErr,
			},
		)
	}

	observation, err := NewMerchantQueryObservation(
		MerchantQueryObservationContext{
			ObservationID: acquisition.InvocationToken,
			TenantID:      acquisition.Job.TenantID,
			OrderID:       acquisition.Job.OrderID,
			PrincipalID:   acquisition.Job.PrincipalID,
			Request:       *acquisition.QueryRequest,
		},
		providerResult,
		observedAt,
	)
	if err != nil {
		return service.complete(
			completionContext,
			acquisition,
			nil,
			PaymentCloseJobCompletion{Failure: NewProviderFailure(
				ProviderFailureInvalidResponse,
				"",
				err,
			)},
		)
	}
	tradeState := providerResult.TradeState
	completion := PaymentCloseJobCompletion{
		TradeState:        &tradeState,
		ProviderRequestID: providerResult.ProviderRequestID,
	}
	switch providerResult.TradeState {
	case ProviderTradeStateSuccess:
		convergence, confirmErr := service.confirmer.ConfirmTrustedPayment(
			completionContext,
			TrustedPaymentConfirmation{
				TenantID:            observation.TenantID,
				PaymentAppID:        observation.PaymentAppID,
				PaymentMerchantID:   observation.PaymentMerchantID,
				MerchantOrderNo:     observation.OutTradeNo,
				WeChatTransactionID: observation.TransactionID,
				ActualPaidCents:     observation.AmountCents,
				PaidAt:              *observation.SuccessAt,
				Observation:         observation,
			},
		)
		if confirmErr != nil {
			return service.retryAfterTrustedConvergenceFailure(
				completionContext,
				acquisition,
				&observation,
				confirmErr,
			)
		}
		if !paymentCloseConvergenceMatchesOrder(
			convergence,
			acquisition.Order,
			OrderStatusPaidConfirmed,
		) {
			return service.retryAfterTrustedConvergenceFailure(
				completionContext,
				acquisition,
				&observation,
				NewProviderFailure(
					ProviderFailureInvalidResponse,
					"",
					ErrInvalidPaymentCloseService,
				),
			)
		}
		completion.Resolution = PaymentCloseResolutionPaymentConverged
		return service.complete(
			completionContext,
			acquisition,
			nil,
			completion,
		)
	case ProviderTradeStateClosed, ProviderTradeStateRevoked,
		ProviderTradeStatePayError:
		convergence, closeErr := service.confirmer.CloseTrustedUnpaidPayment(
			completionContext,
			TrustedUnpaidPaymentClosure{Observation: observation},
		)
		if closeErr != nil {
			return service.retryAfterTrustedConvergenceFailure(
				completionContext,
				acquisition,
				&observation,
				closeErr,
			)
		}
		if !paymentCloseConvergenceMatchesOrder(
			convergence,
			acquisition.Order,
			OrderStatusClosedUnpaid,
		) {
			return service.retryAfterTrustedConvergenceFailure(
				completionContext,
				acquisition,
				&observation,
				NewProviderFailure(
					ProviderFailureInvalidResponse,
					"",
					ErrInvalidPaymentCloseService,
				),
			)
		}
		completion.Resolution = PaymentCloseResolutionProviderTerminal
		return service.complete(
			completionContext,
			acquisition,
			nil,
			completion,
		)
	case ProviderTradeStateRefund:
		completion.Resolution = PaymentCloseResolutionManualReview
		return service.complete(
			completionContext,
			acquisition,
			&observation,
			completion,
		)
	case ProviderTradeStateUserPaying:
		return service.complete(
			completionContext,
			acquisition,
			&observation,
			completion,
		)
	case ProviderTradeStateNotPay:
		return service.closeUnpaidProviderOrder(
			ctx,
			completionContext,
			acquisition,
			observation,
			completion,
		)
	default:
		return PaymentCloseJob{}, true, ErrInvalidPaymentCloseService
	}
}

func (service *PaymentCloseService) closeUnpaidProviderOrder(
	providerContext context.Context,
	completionContext context.Context,
	acquisition PaymentCloseJobAcquisition,
	observation TransactionObservation,
	completion PaymentCloseJobCompletion,
) (PaymentCloseJob, bool, error) {
	if acquisition.CloseRequest == nil {
		return PaymentCloseJob{}, true, ErrInvalidPaymentCloseService
	}
	closeResult, closeErr := service.provider.ClosePaymentByOutTradeNo(
		providerContext,
		*acquisition.CloseRequest,
	)
	if closeErr != nil {
		if providerOrderAlreadyClosed(closeErr) {
			completion.Resolution = PaymentCloseResolutionProviderClosed
			return service.complete(
				completionContext,
				acquisition,
				&observation,
				completion,
			)
		}
		completion.Failure = closeErr
		return service.complete(
			completionContext,
			acquisition,
			&observation,
			completion,
		)
	}
	if ValidateProviderPaymentCloseResult(closeResult) != nil {
		completion.Failure = NewProviderFailure(
			ProviderFailureInvalidResponse,
			"",
			ErrInvalidPaymentCloseJob,
		)
		return service.complete(
			completionContext,
			acquisition,
			&observation,
			completion,
		)
	}
	completion.Resolution = PaymentCloseResolutionProviderClosed
	if closeResult.ProviderRequestID != "" {
		completion.ProviderRequestID = closeResult.ProviderRequestID
	}
	return service.complete(
		completionContext,
		acquisition,
		&observation,
		completion,
	)
}

func (service *PaymentCloseService) complete(
	ctx context.Context,
	acquisition PaymentCloseJobAcquisition,
	observation *TransactionObservation,
	completion PaymentCloseJobCompletion,
) (PaymentCloseJob, bool, error) {
	job, err := service.store.CompletePaymentCloseJob(
		ctx,
		acquisition,
		observation,
		completion,
	)
	return job, true, err
}

func (service *PaymentCloseService) retryAfterTrustedConvergenceFailure(
	completionContext context.Context,
	acquisition PaymentCloseJobAcquisition,
	observation *TransactionObservation,
	failure error,
) (PaymentCloseJob, bool, error) {
	return service.complete(
		completionContext,
		acquisition,
		observation,
		PaymentCloseJobCompletion{Failure: paymentCloseFailure(failure)},
	)
}

func paymentCloseFailure(err error) error {
	if err == nil {
		return NewProviderFailure(ProviderFailureAmbiguous, "", nil)
	}
	var providerFailure *ProviderFailure
	if errors.As(err, &providerFailure) && providerFailure != nil {
		return err
	}
	return NewProviderFailure(ProviderFailureAmbiguous, "", err)
}

func paymentCloseConvergenceMatchesOrder(
	convergence PaymentConvergence,
	order Order,
	wantStatus OrderStatus,
) bool {
	if convergence.Order.ID != order.ID ||
		convergence.Order.TenantID != order.TenantID ||
		convergence.Order.PaymentStatus != wantStatus {
		return false
	}
	if wantStatus == OrderStatusPaidConfirmed {
		return convergence.Disposition == PaymentConfirmationDispositionParticipationConfirmed ||
			convergence.Disposition == PaymentConfirmationDispositionRefundRequired
	}
	return convergence.Disposition == PaymentConfirmationDispositionNone
}

func providerOrderAbsent(err error) bool {
	_, code := ProviderFailureMetadata(err)
	return code == "ORDER_NOT_EXIST" || code == "ORDERNOTEXIST"
}

func providerOrderAlreadyClosed(err error) bool {
	_, code := ProviderFailureMetadata(err)
	return code == "ORDER_CLOSED" || code == "ORDERCLOSED"
}
