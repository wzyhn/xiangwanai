package xiangwanapi

import (
	"context"
	"errors"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

var (
	ErrInvalidWeChatPaymentQuery  = errors.New("invalid xiangwan WeChat payment query")
	ErrWeChatPaymentQueryDisabled = errors.New(
		"xiangwan WeChat payment query is disabled",
	)
)

type weChatPaymentQuerier interface {
	Query(
		context.Context,
		payment.PaymentQueryCommand,
	) (payment.PaymentQueryResult, error)
}

type WeChatPaymentQueryService struct {
	tenantID     uuid.UUID
	generationID uuid.UUID
	querier      weChatPaymentQuerier
	enabled      bool
}

func NewWeChatPaymentQueryService(
	tenantID uuid.UUID,
	generationID uuid.UUID,
	querier weChatPaymentQuerier,
	enabled bool,
) (*WeChatPaymentQueryService, error) {
	if tenantID == uuid.Nil || generationID == uuid.Nil ||
		(enabled && querier == nil) || (!enabled && querier != nil) {
		return nil, ErrInvalidWeChatPaymentQuery
	}
	return &WeChatPaymentQueryService{
		tenantID: tenantID, generationID: generationID,
		querier: querier, enabled: enabled,
	}, nil
}

func (service *WeChatPaymentQueryService) Query(
	ctx context.Context,
	principalID uuid.UUID,
	orderID uuid.UUID,
) (payment.PaymentQueryResult, error) {
	if service == nil || service.tenantID == uuid.Nil ||
		service.generationID == uuid.Nil || ctx == nil ||
		principalID == uuid.Nil || orderID == uuid.Nil {
		return payment.PaymentQueryResult{}, ErrInvalidWeChatPaymentQuery
	}
	if !service.enabled || service.querier == nil {
		return payment.PaymentQueryResult{}, ErrWeChatPaymentQueryDisabled
	}
	return service.querier.Query(ctx, payment.PaymentQueryCommand{
		TenantID:     service.tenantID,
		GenerationID: service.generationID,
		OrderID:      orderID,
		PrincipalID:  principalID,
	})
}
