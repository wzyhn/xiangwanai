package xiangwanapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

func TestWeChatPaymentNotificationServiceBindsServerTenantAndObservation(t *testing.T) {
	t.Parallel()

	tenantID := uuid.New()
	observationID := uuid.New()
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	confirmer := &fakePaymentNotificationConfirmer{
		result: payment.PaymentConvergence{
			Disposition: payment.PaymentConfirmationDispositionParticipationConfirmed,
		},
	}
	service, err := NewWeChatPaymentNotificationService(tenantID, confirmer, true)
	if err != nil {
		t.Fatalf("NewWeChatPaymentNotificationService() error = %v", err)
	}
	service.now = func() time.Time { return now }
	service.newID = func() uuid.UUID { return observationID }
	notification := validAPIPaymentNotification(now)
	result, err := service.Process(context.Background(), notification)
	if err != nil || confirmer.calls != 1 ||
		confirmer.command.TenantID != tenantID ||
		confirmer.command.ObservationID != observationID ||
		!confirmer.command.ObservedAt.Equal(now) ||
		confirmer.command.Notification.NotificationID != notification.NotificationID ||
		result.Disposition != payment.PaymentConfirmationDispositionParticipationConfirmed {
		t.Fatalf("Process() result=%+v err=%v confirmer=%+v", result, err, confirmer)
	}
}

func TestWeChatPaymentNotificationServiceFailsClosedWhenDisabled(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	service, err := NewWeChatPaymentNotificationService(uuid.New(), nil, false)
	if err != nil {
		t.Fatalf("NewWeChatPaymentNotificationService() error = %v", err)
	}
	_, err = service.Process(context.Background(), validAPIPaymentNotification(now))
	if !errors.Is(err, ErrWeChatPaymentNotificationDisabled) {
		t.Fatalf("Process(disabled) error = %v", err)
	}
}

type fakePaymentNotificationConfirmer struct {
	result  payment.PaymentConvergence
	err     error
	calls   int
	command payment.TrustedPaymentNotification
}

func (fake *fakePaymentNotificationConfirmer) ConfirmTrustedPaymentNotification(
	_ context.Context,
	command payment.TrustedPaymentNotification,
) (payment.PaymentConvergence, error) {
	fake.calls++
	fake.command = command
	return fake.result, fake.err
}

func validAPIPaymentNotification(now time.Time) payment.VerifiedPaymentNotification {
	successAt := now.Add(-2 * time.Minute)
	return payment.VerifiedPaymentNotification{
		NotificationID: "notify-payment-success-api-01",
		CreatedAt:      now.Add(-time.Minute),
		EventType:      payment.PaymentNotificationEventTransactionSuccess,
		ResourceType:   payment.PaymentNotificationResourceTransaction,
		OriginalType:   payment.PaymentNotificationOriginalTypeTransaction,
		PayloadDigest:  strings.Repeat("b", 64),
		Transaction: payment.ProviderPaymentQueryResult{
			AppID:         "wx1234567890abcdef",
			MerchantID:    "1900000109",
			OutTradeNo:    "XIANGWAN-ORDER-API-01",
			TransactionID: "wechat-transaction-api-01",
			TradeType:     payment.PaymentQueryTradeType,
			TradeState:    payment.ProviderTradeStateSuccess,
			AmountCents:   9_900,
			Currency:      payment.PaymentQueryCurrency,
			SuccessAt:     &successAt,
		},
	}
}
