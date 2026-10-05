package payment

import (
	"context"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/pkg/logx"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

var ErrInvalidPrepayService = errors.New("invalid xiangwan prepay service")

const prepayCompletionTimeout = 5 * time.Second

type PrepayAcquisition struct {
	Attempt         PaymentAttempt
	HoldExpiresAt   time.Time
	InvocationToken uuid.UUID
	ProviderRequest *ProviderPrepayRequest
}

type PrepayAttemptStore interface {
	AcquirePrepayAttempt(
		context.Context,
		CreatePrepayAttemptCommand,
	) (PrepayAcquisition, error)
	CompletePrepayAttempt(
		context.Context,
		PrepayAcquisition,
		ProviderPrepayResult,
		error,
	) (PrepayAttemptResult, error)
}

type PrepayService struct {
	store    PrepayAttemptStore
	provider PrepayProviderPort
	closer   RejectedPrepayCloser
}

func NewPrepayService(
	store PrepayAttemptStore,
	provider PrepayProviderPort,
	closer RejectedPrepayCloser,
) (*PrepayService, error) {
	if store == nil || provider == nil || closer == nil {
		return nil, ErrInvalidPrepayService
	}
	return &PrepayService{store: store, provider: provider, closer: closer}, nil
}

func (service *PrepayService) Create(
	ctx context.Context,
	command CreatePrepayAttemptCommand,
) (PrepayAttemptResult, error) {
	if service == nil || service.store == nil || service.provider == nil || service.closer == nil || ctx == nil {
		return PrepayAttemptResult{}, ErrInvalidPrepayService
	}
	if err := ValidateCreatePrepayAttemptCommand(command); err != nil {
		return PrepayAttemptResult{}, err
	}
	acquisition, err := service.store.AcquirePrepayAttempt(ctx, command)
	if err != nil {
		return PrepayAttemptResult{}, err
	}
	if acquisition.ProviderRequest == nil {
		return resultFromAcquisition(acquisition), nil
	}
	providerResult, providerErr := service.provider.CreateMiniProgramPrepay(
		ctx,
		*acquisition.ProviderRequest,
	)
	completionContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		prepayCompletionTimeout,
	)
	defer cancel()
	result, err := service.store.CompletePrepayAttempt(
		completionContext,
		acquisition,
		providerResult,
		providerErr,
	)
	if err != nil {
		return PrepayAttemptResult{}, err
	}
	if providerErr != nil {
		class, code := ProviderFailureMetadata(providerErr)
		logx.FromContext(ctx).Warn("xiangwan.payment_prepay_failed", zap.String("order_id", command.OrderID.String()), zap.String("failure_class", class), zap.String("provider_code", code))
		if class == ProviderFailureRejected && IsDefinitivePrepayRejectionCode(code) {
			closed, closeErr := service.closer.CloseRejectedPrepay(completionContext, RejectedPrepayClosure{
				TenantID: command.TenantID, OrderID: command.OrderID, PrincipalID: command.PrincipalID,
			})
			if closeErr == nil {
				if closed.Order.ID != command.OrderID || closed.Order.TenantID != command.TenantID || closed.Order.PrincipalID != command.PrincipalID || closed.Order.PaymentStatus != OrderStatusClosedUnpaid {
					return PrepayAttemptResult{}, ErrInvalidPrepayService
				}
				return result, ErrPrepayRejected
			}
			if !errors.Is(closeErr, ErrPrepayRejectionUnproven) {
				return PrepayAttemptResult{}, closeErr
			}
		}
	}
	return result, nil
}

func resultFromAcquisition(acquisition PrepayAcquisition) PrepayAttemptResult {
	result := PrepayAttemptResult{
		Attempt:       clonePaymentAttempt(acquisition.Attempt),
		HoldExpiresAt: acquisition.HoldExpiresAt,
	}
	if parameters, available := acquisition.Attempt.PaymentParameters(); available {
		result.Parameters = parameters
	}
	return result
}
