package payment

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewPaymentContextCreatesExactSnapshotAndTenMinuteHold(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 10, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	context, err := NewPaymentContext(validNewPaymentCommand(now))
	if err != nil {
		t.Fatalf("NewPaymentContext() error = %v", err)
	}

	if context.Order.PaymentStatus != OrderStatusPending {
		t.Fatalf("Order.PaymentStatus = %q, want %q", context.Order.PaymentStatus, OrderStatusPending)
	}
	if context.Order.OriginalPriceCents != 10_000 ||
		context.Order.DiscountCents != 1_000 ||
		context.Order.PayableCents != 9_000 {
		t.Fatalf("unexpected price snapshot: %+v", context.Order)
	}
	if !context.Order.CreatedAt.Equal(now.UTC()) || !context.Hold.CreatedAt.Equal(now.UTC()) {
		t.Fatalf("timestamps were not normalized to UTC")
	}
	if context.Hold.HoldStatus != CapacityHoldStatusActive {
		t.Fatalf("HoldStatus = %q, want %q", context.Hold.HoldStatus, CapacityHoldStatusActive)
	}
	if context.Hold.OrderID != context.Order.ID ||
		context.Hold.RegistrationID != context.Order.RegistrationID ||
		context.Hold.SessionID != context.Order.SessionID {
		t.Fatalf("hold identity is not bound to Order: %+v", context.Hold)
	}
	if got := context.Hold.ExpiresAt.Sub(context.Hold.CreatedAt); got != CapacityHoldDuration {
		t.Fatalf("hold duration = %v, want %v", got, CapacityHoldDuration)
	}
}

func TestNewPaymentContextRejectsInvalidFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 2, 30, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*NewPaymentContextCommand)
	}{
		{name: "free context", mutate: func(command *NewPaymentContextCommand) {
			command.OriginalPriceCents = 0
		}},
		{name: "excessive discount", mutate: func(command *NewPaymentContextCommand) {
			command.DiscountCents = command.OriginalPriceCents + 1
		}},
		{name: "invalid idempotency", mutate: func(command *NewPaymentContextCommand) {
			command.IdempotencyKey = "contains whitespace"
		}},
		{name: "surrounding merchant whitespace", mutate: func(command *NewPaymentContextCommand) {
			command.PaymentMerchantID = " merchant "
		}},
		{name: "missing registration", mutate: func(command *NewPaymentContextCommand) {
			command.RegistrationID = uuid.Nil
		}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validNewPaymentCommand(now)
			test.mutate(&command)
			if _, err := NewPaymentContext(command); !errors.Is(err, ErrInvalidPaymentContext) {
				t.Fatalf("NewPaymentContext() error = %v, want ErrInvalidPaymentContext", err)
			}
		})
	}
}

func TestSettleOrderZeroRecordsNoProviderPaymentFact(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 2, 30, 0, 0, time.UTC)
	command := validNewPaymentCommand(now)
	command.DiscountCents = command.OriginalPriceCents
	context, err := NewPaymentContext(command)
	if err != nil {
		t.Fatalf("NewPaymentContext() error = %v", err)
	}
	settled, changed, err := SettleOrderZero(context.Order, now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("SettleOrderZero() = changed %v, error %v", changed, err)
	}
	if settled.PaymentStatus != OrderStatusSettledZero ||
		settled.PayableCents != 0 || settled.ActualPaidCents != nil ||
		settled.WeChatTransactionID != nil || settled.PaidAt != nil {
		t.Fatalf("SettleOrderZero() = %+v", settled)
	}
	replayed, changed, err := SettleOrderZero(settled, now.Add(time.Minute))
	if err != nil || changed || replayed.PaymentStatus != OrderStatusSettledZero {
		t.Fatalf("SettleOrderZero(replay) = %+v, changed %v, error %v", replayed, changed, err)
	}
	if _, _, err := ConfirmOrderPayment(settled, PaymentConfirmation{
		ActualPaidCents:     1,
		WeChatTransactionID: "must-not-exist",
		PaidAt:              now.Add(time.Minute),
	}); err == nil {
		t.Fatal("ConfirmOrderPayment(settled_zero) error = nil")
	}
}

func TestOrderTransitionsAndLatePayment(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 2, 30, 0, 0, time.UTC)
	context, err := NewPaymentContext(validNewPaymentCommand(now))
	if err != nil {
		t.Fatalf("NewPaymentContext() error = %v", err)
	}

	unknown, changed, err := MarkOrderUnknown(context.Order, now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("MarkOrderUnknown() = changed %v, error %v", changed, err)
	}
	closed, changed, err := CloseOrderUnpaid(unknown, now.Add(2*time.Minute))
	if err != nil || !changed || closed.ClosedAt == nil {
		t.Fatalf("CloseOrderUnpaid() = %#v, changed %v, error %v", closed, changed, err)
	}

	confirmation := PaymentConfirmation{
		ActualPaidCents:     9_000,
		WeChatTransactionID: "wx-transaction-1",
		PaidAt:              now.Add(90 * time.Second),
	}
	paid, changed, err := ConfirmOrderPayment(closed, confirmation)
	if err != nil || !changed {
		t.Fatalf("ConfirmOrderPayment() = changed %v, error %v", changed, err)
	}
	if paid.PaymentStatus != OrderStatusPaidConfirmed || paid.ClosedAt == nil {
		t.Fatalf("late payment did not preserve closed fact: %+v", paid)
	}
	if paid.UpdatedAt.Before(closed.UpdatedAt) {
		t.Fatalf("late payment regressed updated_at: paid=%v closed=%v", paid.UpdatedAt, closed.UpdatedAt)
	}

	replayed, changed, err := ConfirmOrderPayment(paid, confirmation)
	if err != nil || changed {
		t.Fatalf("ConfirmOrderPayment(replay) = changed %v, error %v", changed, err)
	}
	if replayed.ActualPaidCents == paid.ActualPaidCents {
		t.Fatalf("idempotent result aliases recorded amount pointer")
	}

	conflicting := confirmation
	conflicting.WeChatTransactionID = "wx-transaction-2"
	if _, _, err := ConfirmOrderPayment(paid, conflicting); !errors.Is(err, ErrPaymentConfirmation) {
		t.Fatalf("ConfirmOrderPayment(conflict) error = %v, want ErrPaymentConfirmation", err)
	}
}

func TestConfirmOrderPaymentRejectsAmountMismatch(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 2, 30, 0, 0, time.UTC)
	context, err := NewPaymentContext(validNewPaymentCommand(now))
	if err != nil {
		t.Fatalf("NewPaymentContext() error = %v", err)
	}
	confirmation := PaymentConfirmation{
		ActualPaidCents:     8_999,
		WeChatTransactionID: "wx-transaction-1",
		PaidAt:              now.Add(time.Minute),
	}
	if _, _, err := ConfirmOrderPayment(context.Order, confirmation); !errors.Is(err, ErrPaymentConfirmation) {
		t.Fatalf("ConfirmOrderPayment() error = %v, want ErrPaymentConfirmation", err)
	}
}

func TestCapacityHoldTransitionsAreTerminal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 2, 30, 0, 0, time.UTC)
	context, err := NewPaymentContext(validNewPaymentCommand(now))
	if err != nil {
		t.Fatalf("NewPaymentContext() error = %v", err)
	}

	converted, changed, err := ConvertCapacityHold(context.Hold, now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("ConvertCapacityHold() = changed %v, error %v", changed, err)
	}
	if _, _, err := ReleaseCapacityHold(converted, "not needed", now.Add(2*time.Minute)); !errors.Is(err, ErrCapacityHoldTerminal) {
		t.Fatalf("ReleaseCapacityHold(converted) error = %v, want ErrCapacityHoldTerminal", err)
	}

	released, changed, err := ReleaseCapacityHold(context.Hold, "provider rejected", now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("ReleaseCapacityHold() = changed %v, error %v", changed, err)
	}
	replayed, changed, err := ReleaseCapacityHold(released, "provider rejected", now.Add(time.Minute))
	if err != nil || changed {
		t.Fatalf("ReleaseCapacityHold(replay) = changed %v, error %v", changed, err)
	}
	if replayed.ReleaseReason == released.ReleaseReason {
		t.Fatalf("idempotent result aliases recorded reason pointer")
	}
	if _, _, err := ConvertCapacityHold(released, now.Add(2*time.Minute)); !errors.Is(err, ErrCapacityHoldTerminal) {
		t.Fatalf("ConvertCapacityHold(released) error = %v, want ErrCapacityHoldTerminal", err)
	}
}

func TestCapacityHoldExpiryUsesHalfOpenBoundary(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 2, 30, 0, 0, time.UTC)
	context, err := NewPaymentContext(validNewPaymentCommand(now))
	if err != nil {
		t.Fatalf("NewPaymentContext() error = %v", err)
	}

	if _, _, err := ConvertCapacityHold(context.Hold, context.Hold.ExpiresAt); !errors.Is(err, ErrCapacityHoldTerminal) {
		t.Fatalf("ConvertCapacityHold(at expiry) error = %v, want ErrCapacityHoldTerminal", err)
	}
	if _, _, err := ReleaseCapacityHold(context.Hold, "timeout", context.Hold.ExpiresAt); !errors.Is(err, ErrInvalidPaymentContext) {
		t.Fatalf("ReleaseCapacityHold(at expiry) error = %v, want ErrInvalidPaymentContext", err)
	}
	if _, _, err := ExpireCapacityHold(context.Hold, "timeout", context.Hold.ExpiresAt.Add(-time.Nanosecond)); !errors.Is(err, ErrInvalidPaymentContext) {
		t.Fatalf("ExpireCapacityHold(before expiry) error = %v, want ErrInvalidPaymentContext", err)
	}
	expired, changed, err := ExpireCapacityHold(context.Hold, "timeout", context.Hold.ExpiresAt)
	if err != nil || !changed {
		t.Fatalf("ExpireCapacityHold(at expiry) = changed %v, error %v", changed, err)
	}
	if expired.HoldStatus != CapacityHoldStatusExpired {
		t.Fatalf("HoldStatus = %q, want %q", expired.HoldStatus, CapacityHoldStatusExpired)
	}
}

func validNewPaymentCommand(now time.Time) NewPaymentContextCommand {
	return NewPaymentContextCommand{
		TenantID:           uuid.New(),
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		InstanceID:         uuid.New(),
		SessionID:          uuid.New(),
		PrincipalID:        uuid.New(),
		IdempotencyKey:     "payment-command-1",
		MerchantOrderNo:    "merchant-order-1",
		PaymentAppID:       "wx-app-1",
		PaymentMerchantID:  "wx-merchant-1",
		OriginalPriceCents: 10_000,
		DiscountCents:      1_000,
		Now:                now,
	}
}
