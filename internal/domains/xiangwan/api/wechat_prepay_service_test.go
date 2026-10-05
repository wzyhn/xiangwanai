package xiangwanapi

import (
	"context"
	"errors"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

func TestWeChatPrepayServiceBuildsServerScopedCommand(t *testing.T) {
	tenantID := uuid.New()
	generationID := uuid.New()
	principalID := uuid.New()
	orderID := uuid.New()
	idempotencyKey := uuid.New()
	creator := &fakeWeChatPrepayCreator{result: payment.PrepayAttemptResult{
		Attempt: payment.PaymentAttempt{ID: uuid.New()},
	}}
	service, err := NewWeChatPrepayService(tenantID, generationID, creator, true)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	result, err := service.Create(
		context.Background(),
		principalID,
		orderID,
		WeChatPrepayRequest{
			IdempotencyKey:       idempotencyKey,
			ExpectedOrderVersion: 3,
			ExpectedPayableCents: 19900,
		},
	)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if result.Attempt.ID != creator.result.Attempt.ID ||
		creator.command.TenantID != tenantID ||
		creator.command.GenerationID != generationID ||
		creator.command.PrincipalID != principalID ||
		creator.command.OrderID != orderID ||
		creator.command.IdempotencyKey != idempotencyKey ||
		creator.command.ExpectedOrderVersion != 3 ||
		creator.command.ExpectedPayableCents != 19900 {
		t.Fatalf("unexpected command/result: %+v %+v", creator.command, result)
	}
}

func TestWeChatPrepayServiceFailsClosedWhenDisabled(t *testing.T) {
	service, err := NewWeChatPrepayService(uuid.New(), uuid.New(), nil, false)
	if err != nil {
		t.Fatalf("new disabled service: %v", err)
	}
	_, err = service.Create(
		context.Background(),
		uuid.New(),
		uuid.New(),
		WeChatPrepayRequest{
			IdempotencyKey:       uuid.New(),
			ExpectedOrderVersion: 1,
			ExpectedPayableCents: 1,
		},
	)
	if !errors.Is(err, ErrWeChatPrepayDisabled) {
		t.Fatalf("expected disabled error, got %v", err)
	}
}

type fakeWeChatPrepayCreator struct {
	command payment.CreatePrepayAttemptCommand
	result  payment.PrepayAttemptResult
	err     error
}

func (creator *fakeWeChatPrepayCreator) Create(
	_ context.Context,
	command payment.CreatePrepayAttemptCommand,
) (payment.PrepayAttemptResult, error) {
	creator.command = command
	return creator.result, creator.err
}
