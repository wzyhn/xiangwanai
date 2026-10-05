package coupon

import (
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestApplyRefundPolicyRestoresCouponForReuse(t *testing.T) {
	t.Parallel()

	instrument, history, at, orderID, registrationID := redeemedCouponFixture(t)
	refundCaseID := uuid.New()
	adjustment, err := ApplyRefundPolicy(ApplyRefundPolicyCommand{
		Instrument: instrument,
		History:    history,
		Input: RefundPolicyInput{
			TenantID:       instrument.TenantID,
			CouponID:       instrument.ID,
			OrderID:        orderID,
			RegistrationID: registrationID,
			RefundCaseID:   &refundCaseID,
			Trigger:        RefundTriggerFullCashRefund,
			Reason:         `full cash refund completed`,
			EvaluatedAt:    at,
		},
		Decision: RefundPolicyDecision{
			Configured:    true,
			PolicyVersion: `coupon-refund-v1`,
			Disposition:   RefundDispositionRestore,
		},
		ActorID:    uuid.New(),
		RecordedAt: at,
	})
	if err != nil {
		t.Fatalf(`ApplyRefundPolicy() error = %v`, err)
	}
	if adjustment.EntryType != EntryTypeRestored ||
		adjustment.RefundCaseID == nil ||
		*adjustment.RefundCaseID != refundCaseID ||
		adjustment.RefundPolicyVersion == nil ||
		*adjustment.RefundPolicyVersion != `coupon-refund-v1` {
		t.Fatalf(`adjustment = %+v`, adjustment)
	}
	history = append(history, adjustment)
	projection, err := Project(instrument, history, at)
	if err != nil || projection.Status != StatusAvailable ||
		projection.RefundAdjustment == nil ||
		projection.RefundAdjustment.ID != adjustment.ID {
		t.Fatalf(`Project(restored) = %+v, %v`, projection, err)
	}

	secondOrderID := uuid.New()
	secondHold, err := Hold(HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            secondOrderID,
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: 10_000,
		HoldExpiresAt:      at.Add(5 * time.Minute),
		At:                 at.Add(time.Minute),
		RecordedAt:         at.Add(time.Minute),
	})
	if err != nil || secondHold.OrderID == nil ||
		*secondHold.OrderID != secondOrderID {
		t.Fatalf(`Hold(restored) = %+v, %v`, secondHold, err)
	}
	history = append(history, secondHold)
	state, err := InspectRefundPolicyOrder(
		instrument,
		history,
		orderID,
		registrationID,
	)
	if err != nil || state.Redemption.ID == uuid.Nil ||
		state.Adjustment == nil || state.Adjustment.ID != adjustment.ID {
		t.Fatalf(`InspectRefundPolicyOrder(reused) = %+v, %v`, state, err)
	}
}

func TestApplyRefundPolicyForfeitsCouponAndRejectsMissingPolicy(t *testing.T) {
	t.Parallel()

	instrument, history, at, orderID, registrationID := redeemedCouponFixture(t)
	input := RefundPolicyInput{
		TenantID:       instrument.TenantID,
		CouponID:       instrument.ID,
		OrderID:        orderID,
		RegistrationID: registrationID,
		Trigger:        RefundTriggerSettledZeroCancellation,
		Reason:         `zero-settled Registration cancelled`,
		EvaluatedAt:    at,
	}
	command := ApplyRefundPolicyCommand{
		Instrument: instrument,
		History:    history,
		Input:      input,
		Decision: RefundPolicyDecision{
			Configured:    true,
			PolicyVersion: `coupon-refund-v2`,
			Disposition:   RefundDispositionForfeit,
		},
		ActorID:    uuid.New(),
		RecordedAt: at,
	}
	forfeited, err := ApplyRefundPolicy(command)
	if err != nil {
		t.Fatalf(`ApplyRefundPolicy(forfeit) error = %v`, err)
	}
	projection, err := Project(instrument, append(history, forfeited), at)
	if err != nil || projection.Status != StatusRedeemed ||
		projection.TerminalEntry == nil ||
		projection.TerminalEntry.EntryType != EntryTypeForfeited {
		t.Fatalf(`Project(forfeited) = %+v, %v`, projection, err)
	}

	command.Decision.Configured = false
	if _, err := ApplyRefundPolicy(command); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf(`ApplyRefundPolicy(unconfigured) error = %v`, err)
	}
}

func redeemedCouponFixture(
	t *testing.T,
) (Coupon, []Entry, time.Time, uuid.UUID, uuid.UUID) {
	t.Helper()
	instrument, history, at := lifecycleFixture(t)
	orderID := uuid.New()
	registrationID := uuid.New()
	held, err := Hold(HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            orderID,
		RegistrationID:     registrationID,
		SeriesID:           uuid.New(),
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: 10_000,
		HoldExpiresAt:      at.Add(10 * time.Minute),
		At:                 at,
		RecordedAt:         at,
	})
	if err != nil {
		t.Fatalf(`Hold() error = %v`, err)
	}
	history = append(history, held)
	redeemed, err := Redeem(RedeemCommand{
		Instrument: instrument,
		History:    history,
		OrderID:    orderID,
		At:         at.Add(time.Minute),
		RecordedAt: at.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`Redeem() error = %v`, err)
	}
	return instrument, append(history, redeemed), at.Add(2 * time.Minute),
		orderID, registrationID
}
