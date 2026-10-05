package payment

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

const (
	PaymentNotificationEventTransactionSuccess = "TRANSACTION.SUCCESS"
	PaymentNotificationResourceTransaction     = "encrypt-resource"
	PaymentNotificationOriginalTypeTransaction = "transaction"
)

var (
	ErrInvalidPaymentNotification = errors.New("invalid xiangwan payment notification")
	notificationIDPattern         = regexp.MustCompile(`^[0-9A-Za-z_-]{1,128}$`)
	payloadDigestPattern          = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// VerifiedPaymentNotification contains only the projection produced after the
// provider signature and encrypted resource have both been verified.
type VerifiedPaymentNotification struct {
	NotificationID string
	CreatedAt      time.Time
	EventType      string
	ResourceType   string
	OriginalType   string
	PayloadDigest  string
	Transaction    ProviderPaymentQueryResult
}

type TrustedPaymentNotification struct {
	TenantID      uuid.UUID
	ObservationID uuid.UUID
	ObservedAt    time.Time
	Notification  VerifiedPaymentNotification
}

type PaymentNotificationConfirmer interface {
	ConfirmTrustedPaymentNotification(
		context.Context,
		TrustedPaymentNotification,
	) (PaymentConvergence, error)
}

func ValidateVerifiedPaymentNotification(
	notification VerifiedPaymentNotification,
) error {
	transaction := notification.Transaction
	request := ProviderPaymentQueryRequest{
		AppID:       transaction.AppID,
		MerchantID:  transaction.MerchantID,
		OutTradeNo:  transaction.OutTradeNo,
		AmountCents: transaction.AmountCents,
		Currency:    transaction.Currency,
	}
	if !notificationIDPattern.MatchString(notification.NotificationID) ||
		notification.CreatedAt.IsZero() ||
		notification.EventType != PaymentNotificationEventTransactionSuccess ||
		notification.ResourceType != PaymentNotificationResourceTransaction ||
		notification.OriginalType != PaymentNotificationOriginalTypeTransaction ||
		!payloadDigestPattern.MatchString(notification.PayloadDigest) ||
		transaction.ProviderRequestID != "" ||
		transaction.TradeState != ProviderTradeStateSuccess ||
		ValidateProviderPaymentQueryResult(transaction, request) != nil {
		return ErrInvalidPaymentNotification
	}
	return nil
}

func ValidateTrustedPaymentNotification(command TrustedPaymentNotification) error {
	if command.TenantID == uuid.Nil || command.ObservationID == uuid.Nil ||
		command.ObservedAt.IsZero() ||
		ValidateVerifiedPaymentNotification(command.Notification) != nil ||
		command.Notification.CreatedAt.After(command.ObservedAt) ||
		command.Notification.Transaction.SuccessAt == nil ||
		command.Notification.Transaction.SuccessAt.After(command.ObservedAt) {
		return ErrInvalidPaymentNotification
	}
	return nil
}

func NewPaymentNotificationObservation(
	command TrustedPaymentNotification,
	orderID uuid.UUID,
	principalID uuid.UUID,
) (TransactionObservation, error) {
	if orderID == uuid.Nil || principalID == uuid.Nil ||
		ValidateTrustedPaymentNotification(command) != nil {
		return TransactionObservation{}, ErrInvalidPaymentNotification
	}
	transaction := command.Notification.Transaction
	observation := TransactionObservation{
		ID:                command.ObservationID,
		TenantID:          command.TenantID,
		OrderID:           orderID,
		PrincipalID:       principalID,
		ObservationSource: TransactionObservationSourcePaymentNotification,
		SourceKey:         command.Notification.NotificationID,
		PaymentAppID:      transaction.AppID,
		PaymentMerchantID: transaction.MerchantID,
		OutTradeNo:        transaction.OutTradeNo,
		TransactionID:     transaction.TransactionID,
		TradeType:         transaction.TradeType,
		TradeState:        transaction.TradeState,
		AmountCents:       transaction.AmountCents,
		Currency:          transaction.Currency,
		SuccessAt:         cloneTimePointer(transaction.SuccessAt),
		PayloadDigest:     command.Notification.PayloadDigest,
		ObservedAt:        command.ObservedAt.UTC(),
	}
	if ValidateTransactionObservation(observation) != nil {
		return TransactionObservation{}, ErrInvalidPaymentNotification
	}
	return observation, nil
}
