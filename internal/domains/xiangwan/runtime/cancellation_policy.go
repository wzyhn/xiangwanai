package xiangwanruntime

import (
	"context"
	"errors"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
)

var ErrInvalidCancellationPolicy = errors.New(
	"invalid xiangwan Runtime cancellation policy",
)

type runtimePaidCancellationPolicy struct {
	version    string
	selfPolicy registration.SelfCancellationPolicy
}

type runtimeCancellationPolicy interface {
	registration.SelfCancellationPolicy
	registrationpostgres.PaidSelfCancellationPolicy
}

func newRuntimePaidCancellationPolicy(
	version string,
	cutoffHours int,
) (runtimeCancellationPolicy, error) {
	if version == "" {
		if cutoffHours != 0 {
			return nil, ErrInvalidCancellationPolicy
		}
		return nil, nil
	}
	if !runtimePolicyVersionPattern.MatchString(version) ||
		cutoffHours < 0 || cutoffHours > maximumCancellationCutoffHours {
		return nil, ErrInvalidCancellationPolicy
	}
	selfPolicy, err := registration.NewSelfCancellationPolicy(
		version,
		time.Duration(cutoffHours)*time.Hour,
	)
	if err != nil {
		return nil, ErrInvalidCancellationPolicy
	}
	return &runtimePaidCancellationPolicy{
		version:    version,
		selfPolicy: selfPolicy,
	}, nil
}

func (policy *runtimePaidCancellationPolicy) EvaluateSelfCancellation(
	ctx context.Context,
	input registration.SelfCancellationPolicyInput,
) (registration.SelfCancellationPolicyDecision, error) {
	if policy == nil || policy.selfPolicy == nil ||
		!runtimePolicyVersionPattern.MatchString(policy.version) {
		return registration.SelfCancellationPolicyDecision{},
			ErrInvalidCancellationPolicy
	}
	decision, err := policy.selfPolicy.EvaluateSelfCancellation(ctx, input)
	if err != nil ||
		registration.ValidateSelfCancellationPolicyDecision(decision) != nil ||
		decision.PolicyVersion != policy.version {
		return registration.SelfCancellationPolicyDecision{},
			ErrInvalidCancellationPolicy
	}
	return decision, nil
}

func (policy *runtimePaidCancellationPolicy) EvaluatePaidSelfCancellation(
	ctx context.Context,
	input registrationpostgres.PaidSelfCancellationPolicyInput,
) (registrationpostgres.PaidSelfCancellationPolicyDecision, error) {
	if policy == nil || ctx == nil || ctx.Err() != nil ||
		!runtimePolicyVersionPattern.MatchString(policy.version) ||
		input.TenantID == uuid.Nil || input.RegistrationID == uuid.Nil ||
		input.PrincipalID == uuid.Nil || input.SessionID == uuid.Nil ||
		input.SessionStartAt.IsZero() || input.EvaluatedAt.IsZero() {
		return registrationpostgres.PaidSelfCancellationPolicyDecision{},
			ErrInvalidCancellationPolicy
	}
	switch input.OrderStatus {
	case payment.OrderStatusPaidConfirmed,
		payment.OrderStatusUnknown,
		payment.OrderStatusSettledZero:
	default:
		return registrationpostgres.PaidSelfCancellationPolicyDecision{},
			ErrInvalidCancellationPolicy
	}
	cutoffDecision, err := policy.EvaluateSelfCancellation(
		ctx,
		registration.SelfCancellationPolicyInput{
			TenantID:       input.TenantID,
			RegistrationID: input.RegistrationID,
			PrincipalID:    input.PrincipalID,
			SessionID:      input.SessionID,
			SessionStartAt: input.SessionStartAt,
			EvaluatedAt:    input.EvaluatedAt,
		},
	)
	if err != nil {
		return registrationpostgres.PaidSelfCancellationPolicyDecision{},
			ErrInvalidCancellationPolicy
	}
	return registrationpostgres.PaidSelfCancellationPolicyDecision{
		Allowed:       cutoffDecision.Allowed,
		FullRefund:    true,
		PolicyVersion: cutoffDecision.PolicyVersion,
	}, nil
}

type runtimeCouponRefundPolicy struct {
	version     string
	disposition coupon.RefundDisposition
}

func newRuntimeCouponRefundPolicy(
	version string,
	disposition coupon.RefundDisposition,
) coupon.RefundPolicyEvaluator {
	if version == "" {
		return nil
	}
	return runtimeCouponRefundPolicy{
		version:     version,
		disposition: disposition,
	}
}

func (policy runtimeCouponRefundPolicy) EvaluateCouponRefund(
	ctx context.Context,
	input coupon.RefundPolicyInput,
) (coupon.RefundPolicyDecision, error) {
	decision := coupon.RefundPolicyDecision{
		Configured:    true,
		PolicyVersion: policy.version,
		Disposition:   policy.disposition,
	}
	if ctx == nil || ctx.Err() != nil ||
		coupon.ValidateRefundPolicyInput(input) != nil ||
		coupon.ValidateRefundPolicyDecision(decision) != nil {
		return coupon.RefundPolicyDecision{}, ErrInvalidCancellationPolicy
	}
	return decision, nil
}
