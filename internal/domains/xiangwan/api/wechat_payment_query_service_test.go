package xiangwanapi

import (
	"context"
	"errors"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

func TestWeChatPaymentQueryServiceBindsServerScope(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	generationID := uuid.New()
	principalID := uuid.New()
	orderID := uuid.New()
	querier := &fakeScopedPaymentQuerier{result: payment.PaymentQueryResult{
		Order: payment.Order{ID: orderID},
	}}
	service, err := NewWeChatPaymentQueryService(
		tenantID,
		generationID,
		querier,
		true,
	)
	if err != nil {
		t.Fatalf("NewWeChatPaymentQueryService() error = %v", err)
	}
	result, err := service.Query(context.Background(), principalID, orderID)
	if err != nil || result.Order.ID != orderID || querier.calls != 1 ||
		querier.command.TenantID != tenantID ||
		querier.command.GenerationID != generationID ||
		querier.command.PrincipalID != principalID ||
		querier.command.OrderID != orderID {
		t.Fatalf("Query() result=%+v err=%v querier=%+v", result, err, querier)
	}
}

func TestWeChatPaymentQueryServiceFailsClosedWhenDisabled(t *testing.T) {
	t.Parallel()

	service, err := NewWeChatPaymentQueryService(
		uuid.New(),
		uuid.New(),
		nil,
		false,
	)
	if err != nil {
		t.Fatalf("NewWeChatPaymentQueryService() error = %v", err)
	}
	_, err = service.Query(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrWeChatPaymentQueryDisabled) {
		t.Fatalf("disabled Query() error = %v", err)
	}
}

type fakeScopedPaymentQuerier struct {
	result  payment.PaymentQueryResult
	err     error
	calls   int
	command payment.PaymentQueryCommand
}

func (querier *fakeScopedPaymentQuerier) Query(
	_ context.Context,
	command payment.PaymentQueryCommand,
) (payment.PaymentQueryResult, error) {
	querier.calls++
	querier.command = command
	return querier.result, querier.err
}
