package payment

import (
	"context"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/pkg/logx"
	"go.uber.org/zap"
)

const paymentQueryCompletionTimeout = 8 * time.Second

var ErrInvalidPaymentQueryService = errors.New("invalid xiangwan payment query service")

type PaymentQueryService struct {
	store     PaymentQueryStore
	provider  PaymentQueryProviderPort
	confirmer TrustedPaymentConfirmer
	now       func() time.Time
}

func NewPaymentQueryService(
	store PaymentQueryStore,
	provider PaymentQueryProviderPort,
	confirmer TrustedPaymentConfirmer,
	now func() time.Time,
) (*PaymentQueryService, error) {
	if store == nil || provider == nil || confirmer == nil {
		return nil, ErrInvalidPaymentQueryService
	}
	if now == nil {
		now = time.Now
	}
	return &PaymentQueryService{
		store: store, provider: provider, confirmer: confirmer, now: now,
	}, nil
}

func (service *PaymentQueryService) Query(
	ctx context.Context,
	command PaymentQueryCommand,
) (PaymentQueryResult, error) {
	if service == nil || service.store == nil || service.provider == nil ||
		service.confirmer == nil || service.now == nil || ctx == nil {
		return PaymentQueryResult{}, ErrInvalidPaymentQueryService
	}
	if err := ValidatePaymentQueryCommand(command); err != nil {
		return PaymentQueryResult{}, err
	}
	acquisition, err := service.store.AcquirePaymentQuery(ctx, command)
	if err != nil {
		return PaymentQueryResult{}, err
	}
	if acquisition.ProviderRequest == nil {
		return paymentQueryResultFromAcquisition(acquisition), nil
	}

	providerResult, providerErr := service.provider.QueryPaymentByOutTradeNo(
		ctx,
		*acquisition.ProviderRequest,
	)
	if providerErr != nil {
		failureClass, providerCode := ProviderFailureMetadata(providerErr)
		logx.FromContext(ctx).Warn(
			"xiangwan payment provider query failed",
			zap.String("order_id", command.OrderID.String()),
			zap.String("failure_class", failureClass),
			zap.String("provider_code", providerCode),
		)
	} else {
		logx.FromContext(ctx).Info(
			"xiangwan payment provider query result",
			zap.String("order_id", command.OrderID.String()),
			zap.String("trade_state", string(providerResult.TradeState)),
		)
	}
	if providerErr == nil {
		if validateErr := ValidateProviderPaymentQueryResult(
			providerResult,
			*acquisition.ProviderRequest,
		); validateErr != nil {
			providerErr = NewProviderFailure(
				ProviderFailureInvalidResponse,
				"",
				validateErr,
			)
		}
	}
	completedAt := service.now().UTC()
	if completedAt.IsZero() {
		return PaymentQueryResult{}, ErrInvalidPaymentQueryService
	}
	completionContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		paymentQueryCompletionTimeout,
	)
	defer cancel()
	if providerErr != nil {
		if providerOrderAbsent(providerErr) {
			_, closeErr := service.confirmer.CloseRejectedPrepay(completionContext, RejectedPrepayClosure{
				TenantID: acquisition.Order.TenantID, OrderID: acquisition.Order.ID, PrincipalID: acquisition.Order.PrincipalID,
			})
			if closeErr != nil && !errors.Is(closeErr, ErrPrepayRejectionUnproven) {
				logx.FromContext(ctx).Warn("xiangwan.payment_rejection_recovery_failed", zap.String("order_id", command.OrderID.String()))
			}
		}
		return service.store.CompleteFailedPaymentQuery(
			completionContext,
			acquisition,
			providerErr,
		)
	}
	observation, err := NewTransactionObservation(
		acquisition,
		providerResult,
		completedAt,
	)
	if err != nil {
		return service.store.CompleteFailedPaymentQuery(
			completionContext,
			acquisition,
			NewProviderFailure(ProviderFailureInvalidResponse, "", err),
		)
	}
	if IsProviderTerminalUnpaidState(providerResult.TradeState) {
		convergence, closeErr := service.confirmer.CloseTrustedUnpaidPayment(
			completionContext,
			TrustedUnpaidPaymentClosure{Observation: observation},
		)
		if closeErr != nil {
			return service.store.CompleteFailedPaymentQueryWithObservation(
				completionContext,
				acquisition,
				observation,
				paymentQueryFailure(closeErr),
			)
		}
		if !paymentQueryConvergenceMatchesOrder(
			convergence,
			acquisition.Order,
			PaymentQueryStatusClosed,
		) {
			return service.store.CompleteFailedPaymentQueryWithObservation(
				completionContext,
				acquisition,
				observation,
				NewProviderFailure(
					ProviderFailureInvalidResponse,
					"",
					ErrInvalidPaymentQuery,
				),
			)
		}
		convergence.ProviderRequestID = observation.ProviderRequestID
		return service.store.CompleteClosedPaymentQuery(
			completionContext,
			acquisition,
			observation,
			convergence,
		)
	}
	if providerResult.TradeState != ProviderTradeStateSuccess {
		return service.store.CompleteObservedPaymentQuery(
			completionContext,
			acquisition,
			observation,
		)
	}
	convergence, err := service.confirmer.ConfirmTrustedPayment(
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
	if err != nil {
		return service.store.CompleteFailedPaymentQueryWithObservation(
			completionContext,
			acquisition,
			observation,
			paymentQueryFailure(err),
		)
	}
	if !paymentQueryConvergenceMatchesOrder(
		convergence,
		acquisition.Order,
		PaymentQueryStatusConverged,
	) {
		return service.store.CompleteFailedPaymentQueryWithObservation(
			completionContext,
			acquisition,
			observation,
			NewProviderFailure(
				ProviderFailureInvalidResponse,
				"",
				ErrInvalidPaymentQuery,
			),
		)
	}
	convergence.ProviderRequestID = observation.ProviderRequestID
	return service.store.CompleteConvergedPaymentQuery(
		completionContext,
		acquisition,
		convergence,
	)
}

func paymentQueryFailure(err error) error {
	if err == nil {
		return NewProviderFailure(ProviderFailureAmbiguous, "", nil)
	}
	var providerFailure *ProviderFailure
	if errors.As(err, &providerFailure) && providerFailure != nil {
		return err
	}
	return NewProviderFailure(ProviderFailureAmbiguous, "", err)
}

func paymentQueryConvergenceMatchesOrder(
	convergence PaymentConvergence,
	order Order,
	wantStatus PaymentQueryStatus,
) bool {
	if convergence.Order.ID != order.ID ||
		convergence.Order.TenantID != order.TenantID {
		return false
	}
	switch wantStatus {
	case PaymentQueryStatusConverged:
		return convergence.Order.PaymentStatus == OrderStatusPaidConfirmed &&
			(convergence.Disposition == PaymentConfirmationDispositionParticipationConfirmed ||
				convergence.Disposition == PaymentConfirmationDispositionRefundRequired)
	case PaymentQueryStatusClosed:
		return convergence.Order.PaymentStatus == OrderStatusClosedUnpaid &&
			convergence.Disposition == PaymentConfirmationDispositionNone
	default:
		return false
	}
}

func paymentQueryResultFromAcquisition(
	acquisition PaymentQueryAcquisition,
) PaymentQueryResult {
	result := PaymentQueryResult{Order: cloneOrder(acquisition.Order)}
	if acquisition.Order.PaymentStatus == OrderStatusPaidConfirmed {
		result.QueryStatus = PaymentQueryStatusConverged
		return result
	}
	if acquisition.Order.PaymentStatus == OrderStatusClosedUnpaid {
		result.QueryStatus = PaymentQueryStatusClosed
		return result
	}
	result.QueryStatus = acquisition.Lease.QueryStatus
	result.NextQueryAt = cloneTimePointer(acquisition.Lease.NextQueryAt)
	// A cached NOTPAY observation predates the caller's last payment-sheet
	// invocation and cannot authorize another one. Only a newly completed
	// provider query may expose RetryPaymentAllowed.
	return result
}
