package xiangwanapi

import (
	"context"
	"errors"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

var (
	ErrInvalidWeChatPrepayRequest = errors.New("invalid xiangwan WeChat prepay request")
	ErrWeChatPrepayDisabled       = errors.New("xiangwan WeChat prepay is disabled")
)

type WeChatPrepayRequest struct {
	IdempotencyKey       uuid.UUID
	ExpectedOrderVersion int64
	ExpectedPayableCents int64
}

type weChatPrepayCreator interface {
	Create(
		context.Context,
		payment.CreatePrepayAttemptCommand,
	) (payment.PrepayAttemptResult, error)
}

type WeChatPrepayService struct {
	tenantID     uuid.UUID
	generationID uuid.UUID
	creator      weChatPrepayCreator
	enabled      bool
}

func NewWeChatPrepayService(
	tenantID uuid.UUID,
	generationID uuid.UUID,
	creator weChatPrepayCreator,
	enabled bool,
) (*WeChatPrepayService, error) {
	if tenantID == uuid.Nil || generationID == uuid.Nil ||
		(enabled && creator == nil) || (!enabled && creator != nil) {
		return nil, ErrInvalidWeChatPrepayRequest
	}
	return &WeChatPrepayService{
		tenantID:     tenantID,
		generationID: generationID,
		creator:      creator,
		enabled:      enabled,
	}, nil
}

func (service *WeChatPrepayService) Create(
	ctx context.Context,
	principalID uuid.UUID,
	orderID uuid.UUID,
	request WeChatPrepayRequest,
) (payment.PrepayAttemptResult, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.generationID == uuid.Nil || ctx == nil ||
		principalID == uuid.Nil || orderID == uuid.Nil ||
		request.IdempotencyKey == uuid.Nil ||
		request.IdempotencyKey.Version() != 4 ||
		request.IdempotencyKey.Variant() != uuid.RFC4122 ||
		request.ExpectedOrderVersion < 1 || request.ExpectedPayableCents <= 0 {
		return payment.PrepayAttemptResult{}, ErrInvalidWeChatPrepayRequest
	}
	if !service.enabled || service.creator == nil {
		return payment.PrepayAttemptResult{}, ErrWeChatPrepayDisabled
	}
	return service.creator.Create(ctx, payment.CreatePrepayAttemptCommand{
		TenantID:             service.tenantID,
		GenerationID:         service.generationID,
		OrderID:              orderID,
		PrincipalID:          principalID,
		IdempotencyKey:       request.IdempotencyKey,
		ExpectedOrderVersion: request.ExpectedOrderVersion,
		ExpectedPayableCents: request.ExpectedPayableCents,
	})
}
