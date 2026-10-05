package paymentpostgres

import (
	"context"
	"errors"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
)

// ConfirmTrustedPaymentNotification binds a verified provider notification to
// the locally resolved Order inside the same serializable convergence path.
func (confirmer *PaymentConfirmer) ConfirmTrustedPaymentNotification(
	ctx context.Context,
	command payment.TrustedPaymentNotification,
) (payment.PaymentConvergence, error) {
	if payment.ValidateTrustedPaymentNotification(command) != nil {
		return payment.PaymentConvergence{}, payment.ErrInvalidPaymentNotification
	}
	transaction := command.Notification.Transaction
	result, err := confirmer.confirm(ctx, ConfirmPaymentCommand{
		TenantID:            command.TenantID,
		PaymentAppID:        transaction.AppID,
		PaymentMerchantID:   transaction.MerchantID,
		MerchantOrderNo:     transaction.OutTradeNo,
		WeChatTransactionID: transaction.TransactionID,
		ActualPaidCents:     transaction.AmountCents,
		PaidAt:              *transaction.SuccessAt,
	}, &command)
	if errors.Is(err, ErrInvalidPaymentConfirmationCommand) {
		return payment.PaymentConvergence{}, payment.ErrInvalidPaymentNotification
	}
	return projectPaymentConvergence(result, err)
}

// ConfirmTrustedPayment adapts the shared atomic PaymentConfirmer to the
// provider-query port while keeping the verified observation in the same
// serializable transaction as Order/Registration/Hold convergence.
func (confirmer *PaymentConfirmer) ConfirmTrustedPayment(
	ctx context.Context,
	command payment.TrustedPaymentConfirmation,
) (payment.PaymentConvergence, error) {
	observation := command.Observation
	result, err := confirmer.Confirm(ctx, ConfirmPaymentCommand{
		TenantID:            command.TenantID,
		PaymentAppID:        command.PaymentAppID,
		PaymentMerchantID:   command.PaymentMerchantID,
		MerchantOrderNo:     command.MerchantOrderNo,
		WeChatTransactionID: command.WeChatTransactionID,
		ActualPaidCents:     command.ActualPaidCents,
		PaidAt:              command.PaidAt,
		Observation:         &observation,
	})
	if err != nil {
		return payment.PaymentConvergence{}, err
	}
	return projectPaymentConvergence(result, nil)
}

func projectPaymentConvergence(
	result PaymentConfirmationResult,
	err error,
) (payment.PaymentConvergence, error) {
	if err != nil {
		return payment.PaymentConvergence{}, err
	}
	var disposition payment.PaymentConfirmationDisposition
	switch result.Disposition {
	case PaymentDispositionParticipationConfirmed:
		disposition = payment.PaymentConfirmationDispositionParticipationConfirmed
	case PaymentDispositionRefundRequired:
		disposition = payment.PaymentConfirmationDispositionRefundRequired
	default:
		return payment.PaymentConvergence{}, ErrPaymentConfirmationTransaction
	}
	return payment.PaymentConvergence{
		Order:       result.Order,
		Disposition: disposition,
	}, nil
}
