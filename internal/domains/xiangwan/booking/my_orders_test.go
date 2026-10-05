package booking

import (
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
)

func TestProjectMyOrderSeparatesPaymentAndRefundFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 6, 0, 0, 0, time.UTC)
	tests := []struct {
		name                string
		mutate              func(*MyRegistrationFacts)
		wantState           MyOrderState
		wantOutcome         MyOrderOutcome
		wantConfirming      bool
		wantContinuePayment bool
	}{
		{
			name: "pending payment",
			mutate: func(facts *MyRegistrationFacts) {
				makePendingPaymentFacts(facts, now)
			},
			wantState:           MyOrderStatePendingPayment,
			wantOutcome:         MyOrderOutcomePendingPayment,
			wantContinuePayment: true,
		},
		{
			name: "provider outcome unknown",
			mutate: func(facts *MyRegistrationFacts) {
				makePendingPaymentFacts(facts, now)
				facts.Order.PaymentStatus = payment.OrderStatusUnknown
			},
			wantState:      MyOrderStatePendingPayment,
			wantOutcome:    MyOrderOutcomePaymentConfirming,
			wantConfirming: true,
		},
		{
			name: "paid",
			mutate: func(facts *MyRegistrationFacts) {
				makePaidMyOrderFacts(facts, now)
			},
			wantState:   MyOrderStatePaid,
			wantOutcome: MyOrderOutcomePaidConfirmed,
		},
		{
			name: "settled by coupon without provider payment",
			mutate: func(facts *MyRegistrationFacts) {
				makeSettledZeroMyOrderFacts(facts, now)
			},
			wantState:   MyOrderStatePaid,
			wantOutcome: MyOrderOutcomeSettledZero,
		},
		{
			name: "closed unpaid",
			mutate: func(facts *MyRegistrationFacts) {
				makeClosedMyOrderFacts(facts, now)
			},
			wantState:   MyOrderStateClosed,
			wantOutcome: MyOrderOutcomeClosedUnpaid,
		},
		{
			name: "refund processing",
			mutate: func(facts *MyRegistrationFacts) {
				makeRefundFacts(facts, now, refund.StatusProcessing)
			},
			wantState:   MyOrderStateRefundProcessing,
			wantOutcome: MyOrderOutcomeRefundProcessing,
		},
		{
			name: "refund failed remains actionable",
			mutate: func(facts *MyRegistrationFacts) {
				makeRefundFacts(facts, now, refund.StatusFailed)
			},
			wantState:   MyOrderStateRefundProcessing,
			wantOutcome: MyOrderOutcomeRefundFailed,
		},
		{
			name: "refunded",
			mutate: func(facts *MyRegistrationFacts) {
				makeRefundFacts(facts, now, refund.StatusRefunded)
				facts.Refund.SuccessfulRefundCents =
					facts.Refund.RequestedRefundCents
				resolvedAt := now.Add(3 * time.Minute)
				facts.Refund.ResolvedAt = &resolvedAt
				facts.Refund.UpdatedAt = resolvedAt
			},
			wantState:   MyOrderStateRefunded,
			wantOutcome: MyOrderOutcomeRefunded,
		},
		{
			name: "rejected refund preserves paid fact",
			mutate: func(facts *MyRegistrationFacts) {
				makeRefundFacts(facts, now, refund.StatusRejected)
				resolvedAt := now.Add(3 * time.Minute)
				facts.Refund.ResolvedAt = &resolvedAt
				facts.Refund.UpdatedAt = resolvedAt
			},
			wantState:   MyOrderStatePaid,
			wantOutcome: MyOrderOutcomeRefundRejected,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			facts := myRegistrationFacts(now)
			test.mutate(&facts)
			reservation, err := ProjectMyRegistration(facts, now)
			if err != nil {
				t.Fatalf("ProjectMyRegistration() error = %v", err)
			}
			item, err := ProjectMyOrder(reservation)
			if err != nil {
				t.Fatalf("ProjectMyOrder() error = %v", err)
			}
			if item.State != test.wantState ||
				item.Outcome != test.wantOutcome ||
				item.PaymentConfirmationPending != test.wantConfirming ||
				item.CanContinuePayment != test.wantContinuePayment ||
				item.OrderID != facts.Order.ID ||
				item.RegistrationID != facts.Registration.ID ||
				item.SessionID != facts.Session.ID ||
				item.Currency != MyOrdersCurrency ||
				item.SortRank != MyOrderStateSortRank(test.wantState) ||
				!item.SortAt.Equal(item.LastBusinessAt) {
				t.Fatalf("My Order item = %+v", item)
			}
		})
	}
}

func TestProjectMyOrderUsesLatestCommerceFactAndClonesRefund(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 6, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	makeRefundFacts(&facts, now, refund.StatusPendingManual)
	reservation, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	item, err := ProjectMyOrder(reservation)
	if err != nil {
		t.Fatalf("ProjectMyOrder() error = %v", err)
	}
	if !item.LastBusinessAt.Equal(facts.Refund.UpdatedAt) ||
		item.Refund == nil ||
		item.Outcome != MyOrderOutcomeRefundPendingManual {
		t.Fatalf("My Order latest fact = %+v", item)
	}
	reservation.Refund.RefundStatus = refund.StatusRefunded
	if item.Refund.RefundStatus != refund.StatusPendingManual {
		t.Fatal("ProjectMyOrder() aliased its Refund input")
	}
}

func TestProjectMyOrderRejectsInvalidProjection(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 6, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	makePendingPaymentFacts(&facts, now)
	reservation, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*MyRegistrationItem)
	}{
		{
			name: "Order absent",
			mutate: func(value *MyRegistrationItem) {
				value.Order = nil
			},
		},
		{
			name: "hold version absent",
			mutate: func(value *MyRegistrationItem) {
				value.Order.HoldVersion = 0
			},
		},
		{
			name: "amount snapshot invalid",
			mutate: func(value *MyRegistrationItem) {
				value.Order.PayableCents++
			},
		},
		{
			name: "reservation identity absent",
			mutate: func(value *MyRegistrationItem) {
				value.SessionID = [16]byte{}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := reservation
			order := *reservation.Order
			value.Order = &order
			test.mutate(&value)
			if _, err := ProjectMyOrder(value); !errors.Is(
				err,
				ErrInvalidMyOrderFacts,
			) {
				t.Fatalf("ProjectMyOrder() error = %v", err)
			}
		})
	}
}

func TestMyOrderStateMetadata(t *testing.T) {
	t.Parallel()

	want := map[MyOrderState]int{
		MyOrderStatePendingPayment:   1,
		MyOrderStateRefundProcessing: 2,
		MyOrderStatePaid:             3,
		MyOrderStateRefunded:         4,
		MyOrderStateClosed:           5,
	}
	if !ValidMyOrderState(MyOrderStateAll) {
		t.Fatal("all filter is not valid")
	}
	for state, rank := range want {
		if !ValidMyOrderState(state) ||
			MyOrderStateSortRank(state) != rank {
			t.Fatalf("state %q rank = %d", state, MyOrderStateSortRank(state))
		}
	}
	if ValidMyOrderState("unknown") ||
		MyOrderStateSortRank(MyOrderStateAll) != 0 {
		t.Fatal("invalid state metadata accepted")
	}
}

func makePaidMyOrderFacts(facts *MyRegistrationFacts, now time.Time) {
	makePendingPaymentFacts(facts, now)
	confirmedAt := now
	facts.Registration.ParticipationStatus =
		registration.ParticipationStatusConfirmed
	facts.Registration.ConfirmedAt = &confirmedAt
	facts.Registration.Version = 2
	facts.Registration.UpdatedAt = confirmedAt
	actualPaidCents := facts.Order.PayableCents
	paidAt := now.Add(time.Minute)
	facts.Order.PaymentStatus = payment.OrderStatusPaidConfirmed
	facts.Order.ActualPaidCents = &actualPaidCents
	facts.Order.PaidAt = &paidAt
	facts.Order.Version = 2
	facts.Order.UpdatedAt = paidAt
	facts.Hold.HoldStatus = payment.CapacityHoldStatusConverted
	facts.Hold.Version = 2
	facts.Hold.UpdatedAt = paidAt
}

func makeClosedMyOrderFacts(facts *MyRegistrationFacts, now time.Time) {
	makePendingPaymentFacts(facts, now)
	closedAt := now.Add(time.Minute)
	cancelMyRegistrationFacts(facts, closedAt)
	facts.Order.PaymentStatus = payment.OrderStatusClosedUnpaid
	facts.Order.ClosedAt = &closedAt
	facts.Order.Version = 2
	facts.Order.UpdatedAt = closedAt
	facts.Hold.HoldStatus = payment.CapacityHoldStatusReleased
	facts.Hold.Version = 2
	facts.Hold.UpdatedAt = closedAt
}

func makeSettledZeroMyOrderFacts(
	facts *MyRegistrationFacts,
	now time.Time,
) {
	makePendingPaymentFacts(facts, now)
	confirmedAt := now
	facts.Registration.ParticipationStatus =
		registration.ParticipationStatusConfirmed
	facts.Registration.ConfirmedAt = &confirmedAt
	facts.Registration.Version = 2
	facts.Registration.UpdatedAt = confirmedAt
	facts.Order.PaymentStatus = payment.OrderStatusSettledZero
	facts.Order.DiscountCents = facts.Order.OriginalPriceCents
	facts.Order.PayableCents = 0
	facts.Order.Version = 2
	facts.Order.UpdatedAt = confirmedAt
	facts.Hold.HoldStatus = payment.CapacityHoldStatusConverted
	facts.Hold.Version = 2
	facts.Hold.UpdatedAt = confirmedAt
}
