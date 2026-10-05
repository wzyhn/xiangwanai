package xiangwanruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/coupon"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
	"github.com/google/uuid"
)

func TestRuntimePaidCancellationPolicyUsesExactVersionedCutoff(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 8, 0, 0, 0, time.UTC)
	policy, err := newRuntimePaidCancellationPolicy("cancel:v3", 24)
	if err != nil || policy == nil {
		t.Fatalf("newRuntimePaidCancellationPolicy() = %v, %v", policy, err)
	}
	input := registrationpostgres.PaidSelfCancellationPolicyInput{
		TenantID:       uuid.New(),
		RegistrationID: uuid.New(),
		PrincipalID:    uuid.New(),
		SessionID:      uuid.New(),
		SessionStartAt: now.Add(24 * time.Hour),
		OrderStatus:    payment.OrderStatusPaidConfirmed,
		EvaluatedAt:    now,
	}
	decision, err := policy.EvaluatePaidSelfCancellation(context.Background(), input)
	if err != nil || !decision.Allowed || !decision.FullRefund ||
		decision.PolicyVersion != "cancel:v3" {
		t.Fatalf("EvaluatePaidSelfCancellation(cutoff) = %+v, %v", decision, err)
	}
	input.SessionStartAt = input.SessionStartAt.Add(-time.Nanosecond)
	decision, err = policy.EvaluatePaidSelfCancellation(context.Background(), input)
	if err != nil || decision.Allowed || !decision.FullRefund ||
		decision.PolicyVersion != "cancel:v3" {
		t.Fatalf("EvaluatePaidSelfCancellation(late) = %+v, %v", decision, err)
	}
	unconfigured, err := newRuntimePaidCancellationPolicy("", 0)
	if err != nil || unconfigured != nil {
		t.Fatalf("newRuntimePaidCancellationPolicy(unconfigured) = %v, %v", unconfigured, err)
	}
}

func TestRuntimePaidCancellationPolicyRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	policy, err := newRuntimePaidCancellationPolicy("cancel-v3", 24)
	if err != nil || policy == nil {
		t.Fatalf("newRuntimePaidCancellationPolicy() = %v, %v", policy, err)
	}
	_, err = policy.EvaluatePaidSelfCancellation(
		context.Background(),
		registrationpostgres.PaidSelfCancellationPolicyInput{},
	)
	if !errors.Is(err, ErrInvalidCancellationPolicy) {
		t.Fatalf("EvaluatePaidSelfCancellation(invalid) error = %v", err)
	}
	for _, invalid := range []struct {
		version string
		cutoff  int
	}{
		{version: "bad version", cutoff: 24},
		{version: "cancel-v3", cutoff: -1},
		{version: "cancel-v3", cutoff: maximumCancellationCutoffHours + 1},
		{version: "", cutoff: 1},
	} {
		invalidPolicy, invalidErr := newRuntimePaidCancellationPolicy(
			invalid.version,
			invalid.cutoff,
		)
		if invalidPolicy != nil || !errors.Is(invalidErr, ErrInvalidCancellationPolicy) {
			t.Fatalf(
				"newRuntimePaidCancellationPolicy(%q, %d) = %v, %v",
				invalid.version,
				invalid.cutoff,
				invalidPolicy,
				invalidErr,
			)
		}
	}
}

func TestRuntimeCancellationPolicyUsesSameCutoffForFreeRegistration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 8, 0, 0, 0, time.UTC)
	policy, err := newRuntimePaidCancellationPolicy("cancel-v3", 24)
	if err != nil || policy == nil {
		t.Fatalf("newRuntimePaidCancellationPolicy() = %v, %v", policy, err)
	}
	decision, err := policy.EvaluateSelfCancellation(
		context.Background(),
		registration.SelfCancellationPolicyInput{
			TenantID:       uuid.New(),
			RegistrationID: uuid.New(),
			PrincipalID:    uuid.New(),
			SessionID:      uuid.New(),
			SessionStartAt: now.Add(12 * time.Hour),
			EvaluatedAt:    now,
		},
	)
	if err != nil || decision.Allowed || decision.PolicyVersion != "cancel-v3" {
		t.Fatalf("EvaluateSelfCancellation(free after cutoff) = %+v, %v", decision, err)
	}
}

func TestRuntimeCouponRefundPolicyReturnsConfiguredDisposition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 20, 8, 0, 0, 0, time.UTC)
	policy := newRuntimeCouponRefundPolicy(
		"coupon-refund-v2",
		coupon.RefundDispositionRestore,
	)
	if policy == nil {
		t.Fatal("configured Coupon refund policy is nil")
	}
	decision, err := policy.EvaluateCouponRefund(
		context.Background(),
		coupon.RefundPolicyInput{
			TenantID:       uuid.New(),
			CouponID:       uuid.New(),
			OrderID:        uuid.New(),
			RegistrationID: uuid.New(),
			Trigger:        coupon.RefundTriggerSettledZeroCancellation,
			Reason:         "Registration cancellation settled-zero Coupon",
			EvaluatedAt:    now,
		},
	)
	if err != nil || !decision.Configured ||
		decision.PolicyVersion != "coupon-refund-v2" ||
		decision.Disposition != coupon.RefundDispositionRestore {
		t.Fatalf("EvaluateCouponRefund() = %+v, %v", decision, err)
	}
	if newRuntimeCouponRefundPolicy("", "") != nil {
		t.Fatal("unconfigured Coupon policy did not fail closed")
	}
}
