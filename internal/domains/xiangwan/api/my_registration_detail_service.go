package xiangwanapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyRegistrationDetailService = errors.New(
		"invalid xiangwan My Registration detail service",
	)
	ErrInvalidMyRegistrationDetailRequest = errors.New(
		"invalid xiangwan My Registration detail request",
	)
)

type myRegistrationDetailReader interface {
	GetRegistrationDetail(
		context.Context,
		uuid.UUID,
		uuid.UUID,
		uuid.UUID,
		time.Time,
	) (booking.MyRegistrationDetail, error)
}

type MyRegistrationDetailService struct {
	tenantID uuid.UUID
	reader   myRegistrationDetailReader
	policy   registration.SelfCancellationPolicy
	clock    func() time.Time
}

func NewMyRegistrationDetailService(
	tenantID uuid.UUID,
	reader myRegistrationDetailReader,
	clock func() time.Time,
) (*MyRegistrationDetailService, error) {
	return newMyRegistrationDetailService(tenantID, reader, nil, clock)
}

func NewMyRegistrationDetailServiceWithCancellationPolicy(
	tenantID uuid.UUID,
	reader myRegistrationDetailReader,
	policy registration.SelfCancellationPolicy,
	clock func() time.Time,
) (*MyRegistrationDetailService, error) {
	return newMyRegistrationDetailService(tenantID, reader, policy, clock)
}

func newMyRegistrationDetailService(
	tenantID uuid.UUID,
	reader myRegistrationDetailReader,
	policy registration.SelfCancellationPolicy,
	clock func() time.Time,
) (*MyRegistrationDetailService, error) {
	if tenantID == uuid.Nil || reader == nil || clock == nil {
		return nil, ErrInvalidMyRegistrationDetailService
	}
	return &MyRegistrationDetailService{
		tenantID: tenantID,
		reader:   reader,
		policy:   policy,
		clock:    clock,
	}, nil
}

func (service *MyRegistrationDetailService) Read(
	ctx context.Context,
	principalID uuid.UUID,
	registrationID uuid.UUID,
) (booking.MyRegistrationDetail, error) {
	if service == nil || service.tenantID == uuid.Nil || service.reader == nil ||
		service.clock == nil {
		return booking.MyRegistrationDetail{},
			ErrInvalidMyRegistrationDetailService
	}
	if ctx == nil || principalID == uuid.Nil || registrationID == uuid.Nil {
		return booking.MyRegistrationDetail{},
			ErrInvalidMyRegistrationDetailRequest
	}
	asOf := service.clock().UTC()
	if asOf.IsZero() {
		return booking.MyRegistrationDetail{},
			ErrInvalidMyRegistrationDetailService
	}
	detail, err := service.reader.GetRegistrationDetail(
		ctx,
		service.tenantID,
		principalID,
		registrationID,
		asOf,
	)
	if err != nil {
		return booking.MyRegistrationDetail{}, fmt.Errorf(
			"read xiangwan My Registration detail: %w",
			err,
		)
	}
	detail, err = booking.ApplyMyRegistrationSelfCancellationPolicy(
		ctx,
		service.tenantID,
		principalID,
		detail,
		service.policy,
	)
	if err != nil {
		return booking.MyRegistrationDetail{}, fmt.Errorf(
			"apply xiangwan My Registration cancellation policy: %w",
			err,
		)
	}
	return service.applyPaidCancellationPolicy(ctx, principalID, registrationID, detail, asOf)
}

// The financial read capability uses the same composite policy as the locked
// cancellation command. A generic cutoff policy alone cannot authorize refunds.
func (service *MyRegistrationDetailService) applyPaidCancellationPolicy(
	ctx context.Context,
	principalID, registrationID uuid.UUID,
	detail booking.MyRegistrationDetail,
	asOf time.Time,
) (booking.MyRegistrationDetail, error) {
	item := detail.Item
	paidPolicy, configured := service.policy.(registrationpostgres.PaidSelfCancellationPolicy)
	if !configured || !detail.CancellationPolicyRequired || item.Order == nil ||
		item.RegistrationID != registrationID ||
		(item.State != booking.MyRegistrationStateRegistered &&
			item.State != booking.MyRegistrationStatePendingPayment) {
		return detail, nil
	}
	// Coupon restoration needs its own published policy and ledger facts. Do not
	// advertise financial cancellation for those orders without that decision.
	if item.Order.DiscountCents != 0 || item.Order.PayableCents <= 0 ||
		(item.Order.PaymentStatus != payment.OrderStatusPaidConfirmed &&
			item.Order.PaymentStatus != payment.OrderStatusUnknown) {
		return detail, nil
	}
	cutoff, err := service.policy.EvaluateSelfCancellation(ctx, registration.SelfCancellationPolicyInput{
		TenantID: service.tenantID, PrincipalID: principalID,
		RegistrationID: registrationID, SessionID: item.SessionID,
		SessionStartAt: item.SessionStartAt, EvaluatedAt: asOf,
	})
	if err != nil || registration.ValidateSelfCancellationPolicyDecision(cutoff) != nil {
		return booking.MyRegistrationDetail{}, booking.ErrMyRegistrationCancellationPolicy
	}
	if !cutoff.Allowed {
		return detail, nil
	}
	decision, err := paidPolicy.EvaluatePaidSelfCancellation(ctx, registrationpostgres.PaidSelfCancellationPolicyInput{
		TenantID: service.tenantID, PrincipalID: principalID,
		RegistrationID: registrationID, SessionID: item.SessionID,
		SessionStartAt: item.SessionStartAt, EvaluatedAt: asOf,
		OrderStatus: item.Order.PaymentStatus,
	})
	if err != nil {
		return booking.MyRegistrationDetail{}, booking.ErrMyRegistrationCancellationPolicy
	}
	if !decision.Allowed {
		return detail, nil
	}
	if !decision.FullRefund || decision.PolicyVersion == "" ||
		decision.PolicyVersion != cutoff.PolicyVersion ||
		registration.ValidateSelfCancellationPolicyDecision(registration.SelfCancellationPolicyDecision{
			Allowed: decision.Allowed, PolicyVersion: decision.PolicyVersion,
		}) != nil {
		return booking.MyRegistrationDetail{}, booking.ErrMyRegistrationCancellationPolicy
	}
	detail.CancellationAction = booking.MyRegistrationCancellationActionAvailable
	detail.CancellationPolicyRequired = false
	return detail, nil
}
