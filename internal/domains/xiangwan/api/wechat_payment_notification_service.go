package xiangwanapi

import (
	"context"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/google/uuid"
)

var (
	ErrInvalidWeChatPaymentNotification = errors.New(
		"invalid xiangwan WeChat payment notification",
	)
	ErrWeChatPaymentNotificationDisabled = errors.New(
		"xiangwan WeChat payment notification is disabled",
	)
)

type WeChatPaymentNotificationService struct {
	tenantID  uuid.UUID
	confirmer payment.PaymentNotificationConfirmer
	enabled   bool
	now       func() time.Time
	newID     func() uuid.UUID
}

func NewWeChatPaymentNotificationService(
	tenantID uuid.UUID,
	confirmer payment.PaymentNotificationConfirmer,
	enabled bool,
) (*WeChatPaymentNotificationService, error) {
	if tenantID == uuid.Nil || (enabled && confirmer == nil) ||
		(!enabled && confirmer != nil) {
		return nil, ErrInvalidWeChatPaymentNotification
	}
	return &WeChatPaymentNotificationService{
		tenantID:  tenantID,
		confirmer: confirmer,
		enabled:   enabled,
		now:       time.Now,
		newID:     uuid.New,
	}, nil
}

func (service *WeChatPaymentNotificationService) Process(
	ctx context.Context,
	notification payment.VerifiedPaymentNotification,
) (payment.PaymentConvergence, error) {
	if service == nil || service.tenantID == uuid.Nil || ctx == nil ||
		service.now == nil || service.newID == nil ||
		payment.ValidateVerifiedPaymentNotification(notification) != nil {
		return payment.PaymentConvergence{}, ErrInvalidWeChatPaymentNotification
	}
	if !service.enabled || service.confirmer == nil {
		return payment.PaymentConvergence{}, ErrWeChatPaymentNotificationDisabled
	}
	command := payment.TrustedPaymentNotification{
		TenantID:      service.tenantID,
		ObservationID: service.newID(),
		ObservedAt:    service.now().UTC(),
		Notification:  notification,
	}
	if payment.ValidateTrustedPaymentNotification(command) != nil {
		return payment.PaymentConvergence{}, ErrInvalidWeChatPaymentNotification
	}
	return service.confirmer.ConfirmTrustedPaymentNotification(ctx, command)
}
