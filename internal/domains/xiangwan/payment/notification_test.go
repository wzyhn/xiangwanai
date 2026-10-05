package payment

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPaymentNotificationBuildsProviderKeyedObservation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	command := validTrustedPaymentNotification(now)
	orderID := uuid.New()
	principalID := uuid.New()
	observation, err := NewPaymentNotificationObservation(
		command,
		orderID,
		principalID,
	)
	if err != nil {
		t.Fatalf("NewPaymentNotificationObservation() error = %v", err)
	}
	if observation.ID != command.ObservationID ||
		observation.TenantID != command.TenantID ||
		observation.OrderID != orderID || observation.PrincipalID != principalID ||
		observation.ObservationSource != TransactionObservationSourcePaymentNotification ||
		observation.SourceKey != command.Notification.NotificationID ||
		observation.PayloadDigest != command.Notification.PayloadDigest ||
		observation.TransactionID != command.Notification.Transaction.TransactionID ||
		ValidateTransactionObservation(observation) != nil {
		t.Fatalf("notification observation = %+v", observation)
	}
}

func TestPaymentNotificationRejectsUntrustedOrFutureFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*TrustedPaymentNotification)
	}{
		{name: "invalid notification id", mutate: func(value *TrustedPaymentNotification) {
			value.Notification.NotificationID = "notification/id"
		}},
		{name: "wrong event", mutate: func(value *TrustedPaymentNotification) {
			value.Notification.EventType = "TRANSACTION.CLOSED"
		}},
		{name: "wrong resource", mutate: func(value *TrustedPaymentNotification) {
			value.Notification.ResourceType = "plaintext"
		}},
		{name: "wrong original type", mutate: func(value *TrustedPaymentNotification) {
			value.Notification.OriginalType = "refund"
		}},
		{name: "invalid digest", mutate: func(value *TrustedPaymentNotification) {
			value.Notification.PayloadDigest = strings.Repeat("g", 64)
		}},
		{name: "non-success state", mutate: func(value *TrustedPaymentNotification) {
			value.Notification.Transaction.TradeState = ProviderTradeStateNotPay
		}},
		{name: "provider request projection", mutate: func(value *TrustedPaymentNotification) {
			value.Notification.Transaction.ProviderRequestID = "not-a-callback-fact"
		}},
		{name: "future envelope", mutate: func(value *TrustedPaymentNotification) {
			value.Notification.CreatedAt = value.ObservedAt.Add(time.Second)
		}},
		{name: "future payment", mutate: func(value *TrustedPaymentNotification) {
			future := value.ObservedAt.Add(time.Second)
			value.Notification.Transaction.SuccessAt = &future
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := validTrustedPaymentNotification(now)
			test.mutate(&value)
			if ValidateTrustedPaymentNotification(value) == nil {
				t.Fatalf("ValidateTrustedPaymentNotification(%s) accepted", test.name)
			}
		})
	}
}

func TestPaymentNotificationConfirmerPortUsesTrustedCommand(t *testing.T) {
	t.Parallel()

	var _ PaymentNotificationConfirmer = notificationConfirmerStub{}
}

type notificationConfirmerStub struct{}

func (notificationConfirmerStub) ConfirmTrustedPaymentNotification(
	context.Context,
	TrustedPaymentNotification,
) (PaymentConvergence, error) {
	return PaymentConvergence{}, nil
}

func validTrustedPaymentNotification(now time.Time) TrustedPaymentNotification {
	successAt := now.Add(-2 * time.Minute)
	return TrustedPaymentNotification{
		TenantID:      uuid.New(),
		ObservationID: uuid.New(),
		ObservedAt:    now,
		Notification: VerifiedPaymentNotification{
			NotificationID: "notify-payment-success-01",
			CreatedAt:      now.Add(-time.Minute),
			EventType:      PaymentNotificationEventTransactionSuccess,
			ResourceType:   PaymentNotificationResourceTransaction,
			OriginalType:   PaymentNotificationOriginalTypeTransaction,
			PayloadDigest:  strings.Repeat("a", 64),
			Transaction: ProviderPaymentQueryResult{
				AppID:         "wx1234567890abcdef",
				MerchantID:    "1900000109",
				OutTradeNo:    "XIANGWAN-ORDER-01",
				TransactionID: "wechat-transaction-01",
				TradeType:     PaymentQueryTradeType,
				TradeState:    ProviderTradeStateSuccess,
				AmountCents:   9_900,
				Currency:      PaymentQueryCurrency,
				SuccessAt:     &successAt,
			},
		},
	}
}
