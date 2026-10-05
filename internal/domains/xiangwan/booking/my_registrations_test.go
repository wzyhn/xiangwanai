package booking

import (
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/registration"
	"github.com/google/uuid"
)

func TestProjectMyRegistrationSeparatesBusinessAxes(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name                string
		mutate              func(*MyRegistrationFacts)
		wantState           MyRegistrationState
		wantContinuePayment bool
		wantActiveAccess    bool
		wantCheckin         CheckinStatus
	}{
		{
			name:             "confirmed upcoming participation",
			wantState:        MyRegistrationStateRegistered,
			wantActiveAccess: true,
			wantCheckin:      CheckinStatusNotRecorded,
		},
		{
			name: "pending payment with live hold",
			mutate: func(facts *MyRegistrationFacts) {
				makePendingPaymentFacts(facts, now)
			},
			wantState:           MyRegistrationStatePendingPayment,
			wantContinuePayment: true,
			wantCheckin:         CheckinStatusNotRecorded,
		},
		{
			name: "unknown payment cannot be restarted",
			mutate: func(facts *MyRegistrationFacts) {
				makePendingPaymentFacts(facts, now)
				facts.Order.PaymentStatus = payment.OrderStatusUnknown
			},
			wantState:   MyRegistrationStatePendingPayment,
			wantCheckin: CheckinStatusNotRecorded,
		},
		{
			name: "coupon zero settlement is registered",
			mutate: func(facts *MyRegistrationFacts) {
				makeSettledZeroMyOrderFacts(facts, now)
			},
			wantState:        MyRegistrationStateRegistered,
			wantActiveAccess: true,
			wantCheckin:      CheckinStatusNotRecorded,
		},
		{
			name: "cancelled participation",
			mutate: func(facts *MyRegistrationFacts) {
				cancelMyRegistrationFacts(facts, now.Add(time.Minute))
			},
			wantState:   MyRegistrationStateCancelled,
			wantCheckin: CheckinStatusNotRecorded,
		},
		{
			name: "manual refund pending",
			mutate: func(facts *MyRegistrationFacts) {
				makeRefundFacts(facts, now, refund.StatusPendingManual)
			},
			wantState:   MyRegistrationStateRefundProcessing,
			wantCheckin: CheckinStatusNotRecorded,
		},
		{
			name: "failed refund remains processing work",
			mutate: func(facts *MyRegistrationFacts) {
				makeRefundFacts(facts, now, refund.StatusFailed)
			},
			wantState:   MyRegistrationStateRefundProcessing,
			wantCheckin: CheckinStatusNotRecorded,
		},
		{
			name: "completed refund",
			mutate: func(facts *MyRegistrationFacts) {
				makeRefundFacts(facts, now, refund.StatusRefunded)
				facts.Refund.SuccessfulRefundCents =
					facts.Refund.RequestedRefundCents
				resolvedAt := now.Add(3 * time.Minute)
				facts.Refund.ResolvedAt = &resolvedAt
				facts.Refund.UpdatedAt = resolvedAt
			},
			wantState:   MyRegistrationStateRefunded,
			wantCheckin: CheckinStatusNotRecorded,
		},
		{
			name: "rejected refund preserves cancelled classification",
			mutate: func(facts *MyRegistrationFacts) {
				makeRefundFacts(facts, now, refund.StatusRejected)
			},
			wantState:   MyRegistrationStateCancelled,
			wantCheckin: CheckinStatusNotRecorded,
		},
		{
			name: "ended and independently checked in",
			mutate: func(facts *MyRegistrationFacts) {
				start := now.Add(-2 * time.Hour)
				end := now.Add(-time.Hour)
				facts.Session.SessionStartAt = &start
				facts.Session.SessionEndAt = &end
				facts.Session.Status = activity.SessionStatusEnded
				checkedInAt := start.Add(10 * time.Minute)
				facts.Checkin = CheckinSummary{
					Status:      CheckinStatusCheckedIn,
					CheckedInAt: &checkedInAt,
				}
			},
			wantState:        MyRegistrationStateEnded,
			wantCheckin:      CheckinStatusCheckedIn,
			wantActiveAccess: false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			facts := myRegistrationFacts(now)
			if test.mutate != nil {
				test.mutate(&facts)
			}
			item, err := ProjectMyRegistration(facts, now)
			if err != nil {
				t.Fatalf("ProjectMyRegistration() error = %v", err)
			}
			if item.State != test.wantState ||
				item.CanContinuePayment != test.wantContinuePayment ||
				item.HasActiveAccess != test.wantActiveAccess ||
				item.Checkin.Status != test.wantCheckin ||
				item.RegistrationID != facts.Registration.ID ||
				item.SessionID != facts.Session.ID ||
				item.InstanceID != facts.Instance.ID ||
				item.SeriesID != facts.Registration.SeriesID {
				t.Fatalf("My Registration item = %+v", item)
			}
			if MyRegistrationStateSortsForward(item.State) {
				if !item.SortAt.Equal(*facts.Session.SessionStartAt) {
					t.Fatalf("forward SortAt = %s", item.SortAt)
				}
			} else if !item.SortAt.Equal(item.LastBusinessAt) {
				t.Fatalf(
					"terminal SortAt = %s, last business = %s",
					item.SortAt,
					item.LastBusinessAt,
				)
			}
		})
	}
}

func TestProjectMyRegistrationCancellationPrecedesElapsedTime(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 4, 0, 0, 0, time.UTC)
	facts := myRegistrationFacts(now)
	cancelMyRegistrationFacts(&facts, now.Add(-2*time.Hour))
	start := now.Add(-4 * time.Hour)
	end := now.Add(-3 * time.Hour)
	facts.Session.SessionStartAt = &start
	facts.Session.SessionEndAt = &end

	item, err := ProjectMyRegistration(facts, now)
	if err != nil {
		t.Fatalf("ProjectMyRegistration() error = %v", err)
	}
	if item.State != MyRegistrationStateCancelled {
		t.Fatalf("State = %q, want cancelled", item.State)
	}
}

func TestProjectMyRegistrationRejectsCrossPrincipalFacts(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 13, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*MyRegistrationFacts)
	}{
		{
			name: "wrong Session",
			mutate: func(facts *MyRegistrationFacts) {
				facts.Session.ID = uuid.New()
			},
		},
		{
			name: "Order from another principal",
			mutate: func(facts *MyRegistrationFacts) {
				makePendingPaymentFacts(facts, now)
				facts.Order.PrincipalID = uuid.New()
			},
		},
		{
			name: "Refund from another Registration",
			mutate: func(facts *MyRegistrationFacts) {
				makeRefundFacts(facts, now, refund.StatusPendingManual)
				facts.Refund.RegistrationID = uuid.New()
			},
		},
		{
			name: "invalid checkin timeline",
			mutate: func(facts *MyRegistrationFacts) {
				checkedInAt := now
				revokedAt := now.Add(-time.Minute)
				facts.Checkin = CheckinSummary{
					Status:      CheckinStatusRevoked,
					CheckedInAt: &checkedInAt,
					RevokedAt:   &revokedAt,
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			facts := myRegistrationFacts(now)
			test.mutate(&facts)
			if _, err := ProjectMyRegistration(facts, now); !errors.Is(
				err,
				ErrInvalidMyRegistrationFacts,
			) {
				t.Fatalf("ProjectMyRegistration() error = %v", err)
			}
		})
	}
}

func TestMyRegistrationStateMetadata(t *testing.T) {
	t.Parallel()

	want := map[MyRegistrationState]int{
		MyRegistrationStatePendingPayment:   1,
		MyRegistrationStateRegistered:       2,
		MyRegistrationStateRefundProcessing: 3,
		MyRegistrationStateRefunded:         4,
		MyRegistrationStateCancelled:        5,
		MyRegistrationStateEnded:            6,
	}
	if !ValidMyRegistrationState(MyRegistrationStateAll) {
		t.Fatal("all filter is not valid")
	}
	for state, rank := range want {
		if !ValidMyRegistrationState(state) ||
			MyRegistrationStateSortRank(state) != rank {
			t.Fatalf("state %q rank = %d", state, MyRegistrationStateSortRank(state))
		}
	}
	if ValidMyRegistrationState("unknown") ||
		MyRegistrationStateSortRank(MyRegistrationStateAll) != 0 {
		t.Fatal("invalid state metadata accepted")
	}
}

func myRegistrationFacts(now time.Time) MyRegistrationFacts {
	tenantID := uuid.New()
	seriesID := uuid.New()
	instanceID := uuid.New()
	sessionID := uuid.New()
	principalID := uuid.New()
	start := now.Add(2 * time.Hour)
	end := now.Add(4 * time.Hour)
	deliveryMode := activity.DeliveryModeOffline
	area := activity.AreaCodeHeping
	venue := "Tianjin AI Hub"
	address := "Innovation Road 1"
	confirmedAt := now.Add(-time.Hour)
	return MyRegistrationFacts{
		Registration: registration.Registration{
			ID:                  uuid.New(),
			TenantID:            tenantID,
			SeriesID:            seriesID,
			InstanceID:          instanceID,
			SessionID:           sessionID,
			PrincipalID:         principalID,
			ParticipationStatus: registration.ParticipationStatusConfirmed,
			IdempotencyKey:      "registration:my-list",
			ConfirmedAt:         &confirmedAt,
			Version:             2,
			CreatedAt:           confirmedAt,
			UpdatedAt:           confirmedAt,
		},
		SeriesTitle: "Tianjin AI Gathering",
		Instance: activity.Instance{
			ID:        instanceID,
			TenantID:  tenantID,
			SeriesID:  seriesID,
			Title:     "September Gathering",
			Status:    activity.InstanceStatusPublished,
			Version:   3,
			CreatedAt: confirmedAt.Add(-time.Hour),
			UpdatedAt: confirmedAt,
		},
		Session: activity.Session{
			ID:             sessionID,
			TenantID:       tenantID,
			InstanceID:     instanceID,
			Title:          "AI Roundtable",
			Status:         activity.SessionStatusPublished,
			SessionStartAt: &start,
			SessionEndAt:   &end,
			DeliveryMode:   &deliveryMode,
			Area:           &area,
			VenueName:      &venue,
			Address:        &address,
			Version:        3,
			CreatedAt:      confirmedAt.Add(-time.Hour),
			UpdatedAt:      confirmedAt,
		},
	}
}

func makePendingPaymentFacts(facts *MyRegistrationFacts, now time.Time) {
	facts.Registration.ParticipationStatus =
		registration.ParticipationStatusPendingPayment
	facts.Registration.ConfirmedAt = nil
	facts.Registration.Version = 1
	orderID := uuid.New()
	facts.Order = &payment.Order{
		ID:                 orderID,
		TenantID:           facts.Registration.TenantID,
		RegistrationID:     facts.Registration.ID,
		SeriesID:           facts.Registration.SeriesID,
		InstanceID:         facts.Registration.InstanceID,
		SessionID:          facts.Registration.SessionID,
		PrincipalID:        facts.Registration.PrincipalID,
		PaymentStatus:      payment.OrderStatusPending,
		IdempotencyKey:     "order:my-list",
		MerchantOrderNo:    "merchant-my-list",
		PaymentAppID:       "wx-app-test",
		PaymentMerchantID:  "wx-merchant-test",
		OriginalPriceCents: 10_000,
		DiscountCents:      1_000,
		PayableCents:       9_000,
		Version:            1,
		CreatedAt:          now.Add(-time.Minute),
		UpdatedAt:          now.Add(-time.Minute),
	}
	facts.Hold = &payment.CapacityHold{
		ID:             uuid.New(),
		TenantID:       facts.Registration.TenantID,
		OrderID:        orderID,
		RegistrationID: facts.Registration.ID,
		SessionID:      facts.Registration.SessionID,
		HoldStatus:     payment.CapacityHoldStatusActive,
		ExpiresAt:      now.Add(9 * time.Minute),
		Version:        1,
		CreatedAt:      now.Add(-time.Minute),
		UpdatedAt:      now.Add(-time.Minute),
	}
}

func makeRefundFacts(
	facts *MyRegistrationFacts,
	now time.Time,
	status refund.Status,
) {
	cancelMyRegistrationFacts(facts, now.Add(time.Minute))
	makePendingPaymentFacts(facts, now)
	facts.Registration.ParticipationStatus =
		registration.ParticipationStatusCancelled
	actualPaid := int64(9_000)
	paidAt := now.Add(-time.Minute)
	facts.Order.PaymentStatus = payment.OrderStatusPaidConfirmed
	facts.Order.ActualPaidCents = &actualPaid
	facts.Order.PaidAt = &paidAt
	facts.Order.Version = 2
	facts.Order.UpdatedAt = paidAt
	facts.Hold.HoldStatus = payment.CapacityHoldStatusConverted
	facts.Hold.Version = 2
	facts.Hold.UpdatedAt = paidAt
	facts.Refund = &refund.Case{
		ID:                   uuid.New(),
		TenantID:             facts.Registration.TenantID,
		OrderID:              facts.Order.ID,
		RegistrationID:       facts.Registration.ID,
		SeriesID:             facts.Registration.SeriesID,
		InstanceID:           facts.Registration.InstanceID,
		SessionID:            facts.Registration.SessionID,
		PrincipalID:          facts.Registration.PrincipalID,
		RefundStatus:         status,
		ReasonCode:           refund.ReasonUserCancelled,
		IdempotencyKey:       "refund:my-list",
		RequestedRefundCents: actualPaid,
		Version:              1,
		CreatedAt:            now.Add(2 * time.Minute),
		UpdatedAt:            now.Add(2 * time.Minute),
	}
}

func cancelMyRegistrationFacts(facts *MyRegistrationFacts, at time.Time) {
	reason := "user_cancelled"
	facts.Registration.ParticipationStatus =
		registration.ParticipationStatusCancelled
	facts.Registration.CancelledAt = &at
	facts.Registration.CancellationReason = &reason
	facts.Registration.Version++
	facts.Registration.UpdatedAt = at
}
