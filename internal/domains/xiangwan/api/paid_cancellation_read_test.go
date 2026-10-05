package xiangwanapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/booking"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	registrationpostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration/postgres"
)

func TestPaidRegistrationCancellationReadUsesFinancialDecision(t *testing.T) {
	t.Parallel()
	asOf := time.Date(2026, 10, 4, 8, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name                                                    string
		status                                                  payment.OrderStatus
		state                                                   booking.MyRegistrationState
		startOffset                                             time.Duration
		discount                                                int64
		decision                                                registrationpostgres.PaidSelfCancellationPolicyDecision
		policyError                                             error
		onlyCutoff, wrongRegistration, wantAvailable, wantError bool
	}{
		{name: "paid cash before deadline", wantAvailable: true},
		{name: "exact deadline", startOffset: -time.Hour, wantAvailable: true},
		{name: "after deadline", startOffset: -time.Hour - time.Nanosecond},
		{name: "uncertain payment may release participation", status: payment.OrderStatusUnknown, wantAvailable: true},
		{name: "cancelled registration", state: booking.MyRegistrationStateCancelled},
		{name: "coupon policy absent", discount: 50},
		{name: "settled zero needs coupon facts", status: payment.OrderStatusSettledZero},
		{name: "cutoff alone cannot grant refund", onlyCutoff: true},
		{name: "wrong registration never grants capability", wrongRegistration: true},
		{name: "refund denied", decision: registrationpostgres.PaidSelfCancellationPolicyDecision{FullRefund: true, PolicyVersion: "cancel-v1"}},
		{name: "partial refund unsupported", decision: registrationpostgres.PaidSelfCancellationPolicyDecision{Allowed: true, PolicyVersion: "cancel-v1"}, wantError: true},
		{name: "unversioned grant rejected", decision: registrationpostgres.PaidSelfCancellationPolicyDecision{Allowed: true, FullRefund: true}, wantError: true},
		{name: "mismatched versions rejected", decision: registrationpostgres.PaidSelfCancellationPolicyDecision{Allowed: true, FullRefund: true, PolicyVersion: "cancel-v2"}, wantError: true},
		{name: "policy failure", policyError: errors.New("unavailable"), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			cutoff, err := registration.NewSelfCancellationPolicy("cancel-v1", 0)
			if err != nil {
				t.Fatal(err)
			}
			policy := &fakePaidReadPolicy{SelfCancellationPolicy: cutoff, decision: test.decision, err: test.policyError}
			if test.decision == (registrationpostgres.PaidSelfCancellationPolicyDecision{}) {
				policy.decision = registrationpostgres.PaidSelfCancellationPolicyDecision{Allowed: true, FullRefund: true, PolicyVersion: "cancel-v1"}
			}
			status := test.status
			if status == "" {
				status = payment.OrderStatusPaidConfirmed
			}
			state := test.state
			if state == "" {
				state = booking.MyRegistrationStateRegistered
			}
			reader := &fakeMyRegistrationDetailReader{detail: booking.MyRegistrationDetail{
				Item: booking.MyRegistrationItem{
					RegistrationID: apiUUID(153), SessionID: apiUUID(154), State: state,
					SessionStartAt: asOf.Add(time.Hour + test.startOffset),
					Order:          &booking.MyRegistrationOrderSummary{PaymentStatus: status, PayableCents: 100, DiscountCents: test.discount},
				},
				CancellationPolicyRequired: true, CancellationAction: booking.MyRegistrationCancellationActionUnavailable, AsOf: asOf,
			}}
			var selected registration.SelfCancellationPolicy = policy
			if test.onlyCutoff {
				selected = cutoff
			}
			requestedID := apiUUID(153)
			if test.wrongRegistration {
				requestedID = apiUUID(155)
			}
			service, err := NewMyRegistrationDetailServiceWithCancellationPolicy(apiUUID(151), reader, selected, func() time.Time { return asOf })
			if err != nil {
				t.Fatal(err)
			}
			got, err := service.Read(context.Background(), apiUUID(152), requestedID)
			if test.wantError {
				if !errors.Is(err, booking.ErrMyRegistrationCancellationPolicy) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			available := got.CancellationAction == booking.MyRegistrationCancellationActionAvailable && !got.CancellationPolicyRequired
			if available != test.wantAvailable {
				t.Fatalf("capability = %+v", got)
			}
			if test.wantAvailable {
				input := policy.input
				if input.TenantID != apiUUID(151) || input.PrincipalID != apiUUID(152) || input.RegistrationID != requestedID || input.SessionID != apiUUID(154) || input.OrderStatus != status || !input.EvaluatedAt.Equal(asOf) {
					t.Fatalf("policy scope = %+v", input)
				}
			}
			if reader.detail.CancellationAction != booking.MyRegistrationCancellationActionUnavailable || !reader.detail.CancellationPolicyRequired {
				t.Fatal("read mutated repository financial state")
			}
		})
	}
}

type fakePaidReadPolicy struct {
	registration.SelfCancellationPolicy
	decision registrationpostgres.PaidSelfCancellationPolicyDecision
	input    registrationpostgres.PaidSelfCancellationPolicyInput
	err      error
}

func (policy *fakePaidReadPolicy) EvaluatePaidSelfCancellation(_ context.Context, input registrationpostgres.PaidSelfCancellationPolicyInput) (registrationpostgres.PaidSelfCancellationPolicyDecision, error) {
	policy.input = input
	return policy.decision, policy.err
}
