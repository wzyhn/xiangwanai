package coupon

import (
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestProjectMyCouponExposesSafeApplicabilityAndLedgerState(t *testing.T) {
	t.Parallel()

	instrument, history, at := lifecycleFixture(t)
	item, err := ProjectMyCoupon(instrument, history, at)
	if err != nil {
		t.Fatalf("ProjectMyCoupon() error = %v", err)
	}
	if item.CouponID != instrument.ID ||
		item.FaceValueCents != instrument.FaceValueCents ||
		item.Currency != MyCouponsCurrency ||
		item.State != MyCouponStateAvailable ||
		item.LedgerStatus != StatusAvailable ||
		!item.Usable || item.CorrectionRequired ||
		item.SortRank != MyCouponStateSortRank(MyCouponStateAvailable) ||
		item.LastEntryID != history[0].ID ||
		item.LastEntrySequence != 1 ||
		item.ApplicabilityTarget.ScopeType != ScopeTypeActivityType ||
		item.ApplicabilityTarget.ActivityType == nil ||
		*item.ApplicabilityTarget.ActivityType !=
			activity.ActivityTypeAIRoundtable ||
		item.ApplicabilityTarget.SeriesID != nil {
		t.Fatalf("My Coupon item = %+v", item)
	}

	*item.ApplicabilityTarget.ActivityType = activity.ActivityTypeCourse
	if *instrument.ScopeActivityType != activity.ActivityTypeAIRoundtable {
		t.Fatal("ProjectMyCoupon() aliased its applicability target")
	}
}

func TestProjectMyCouponPreservesHoldAndRefundAdjustmentFacts(t *testing.T) {
	t.Parallel()

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
		OriginalPriceCents: instrument.MinimumOrderCents,
		HoldExpiresAt:      at.Add(10 * time.Minute),
		At:                 at,
		RecordedAt:         at,
	})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	history = append(history, held)
	heldItem, err := ProjectMyCoupon(instrument, history, at)
	if err != nil {
		t.Fatalf("ProjectMyCoupon(held) error = %v", err)
	}
	if heldItem.State != MyCouponStateHeld || heldItem.Usable ||
		heldItem.ActiveOrderID == nil ||
		*heldItem.ActiveOrderID != orderID ||
		heldItem.ActiveRegistrationID == nil ||
		*heldItem.ActiveRegistrationID != registrationID {
		t.Fatalf("held My Coupon item = %+v", heldItem)
	}

	redeemed, err := Redeem(RedeemCommand{
		Instrument: instrument,
		History:    history,
		OrderID:    orderID,
		At:         at.Add(time.Minute),
		RecordedAt: at.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	history = append(history, redeemed)
	adjustment, err := ApplyRefundPolicy(ApplyRefundPolicyCommand{
		Instrument: instrument,
		History:    history,
		Input: RefundPolicyInput{
			TenantID:       instrument.TenantID,
			CouponID:       instrument.ID,
			OrderID:        orderID,
			RegistrationID: registrationID,
			Trigger:        RefundTriggerSettledZeroCancellation,
			Reason:         "registration cancelled",
			EvaluatedAt:    at.Add(2 * time.Minute),
		},
		Decision: RefundPolicyDecision{
			Configured:    true,
			PolicyVersion: "coupon-refund-v2",
			Disposition:   RefundDispositionRestore,
		},
		ActorID:    uuid.New(),
		RecordedAt: at.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ApplyRefundPolicy() error = %v", err)
	}
	history = append(history, adjustment)
	restoredItem, err := ProjectMyCoupon(
		instrument,
		history,
		at.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("ProjectMyCoupon(restored) error = %v", err)
	}
	if restoredItem.State != MyCouponStateAvailable ||
		!restoredItem.Usable || restoredItem.LatestAdjustment == nil ||
		restoredItem.LatestAdjustment.EntryID != adjustment.ID ||
		restoredItem.LatestAdjustment.EntryType != EntryTypeRestored ||
		restoredItem.LatestAdjustment.PolicyVersion != "coupon-refund-v2" ||
		restoredItem.ActiveOrderID != nil {
		t.Fatalf("restored My Coupon item = %+v", restoredItem)
	}
}

func TestProjectMyCouponSurfacesCorrectionBeforeUnderlyingState(t *testing.T) {
	t.Parallel()

	instrument, history, at := lifecycleFixture(t)
	corrected, err := CorrectForRevokedCheckin(CheckinCorrectionCommand{
		Instrument:            instrument,
		History:               history,
		RevokedCheckinEventID: uuid.New(),
		ActorID:               uuid.New(),
		Reason:                "attendance was revoked",
		At:                    at,
		RecordedAt:            at,
	})
	if err != nil {
		t.Fatalf("CorrectForRevokedCheckin() error = %v", err)
	}
	item, err := ProjectMyCoupon(instrument, append(history, corrected), at)
	if err != nil {
		t.Fatalf("ProjectMyCoupon(correction) error = %v", err)
	}
	if item.State != MyCouponStateInvalidated ||
		item.CorrectionRequired {
		t.Fatalf("unused correction item = %+v", item)
	}

	orderID := uuid.New()
	registrationID := uuid.New()
	held, err := Hold(HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            orderID,
		RegistrationID:     registrationID,
		SeriesID:           uuid.New(),
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: instrument.MinimumOrderCents,
		HoldExpiresAt:      at.Add(10 * time.Minute),
		At:                 at,
		RecordedAt:         at,
	})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	heldHistory := append(append([]Entry(nil), history...), held)
	manual, err := CorrectForRevokedCheckin(CheckinCorrectionCommand{
		Instrument:            instrument,
		History:               heldHistory,
		RevokedCheckinEventID: uuid.New(),
		ActorID:               uuid.New(),
		Reason:                "held benefit needs manual review",
		At:                    at.Add(time.Minute),
		RecordedAt:            at.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CorrectForRevokedCheckin(held) error = %v", err)
	}
	manualItem, err := ProjectMyCoupon(
		instrument,
		append(heldHistory, manual),
		at.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("ProjectMyCoupon(manual) error = %v", err)
	}
	if manualItem.State != MyCouponStateCorrectionRequired ||
		manualItem.LedgerStatus != StatusHeld ||
		!manualItem.CorrectionRequired || manualItem.Usable {
		t.Fatalf("manual-review My Coupon item = %+v", manualItem)
	}
}

func TestProjectMyCouponRejectsInvalidLedger(t *testing.T) {
	t.Parallel()

	instrument, history, at := lifecycleFixture(t)
	history[0].PrincipalID = uuid.New()
	if _, err := ProjectMyCoupon(
		instrument,
		history,
		at,
	); !errors.Is(err, ErrInvalidMyCouponFacts) {
		t.Fatalf("ProjectMyCoupon(invalid) error = %v", err)
	}
}

func TestMyCouponStateMetadata(t *testing.T) {
	t.Parallel()

	want := map[MyCouponState]int{
		MyCouponStateAvailable:          1,
		MyCouponStateHeld:               2,
		MyCouponStateCorrectionRequired: 3,
		MyCouponStateExpired:            4,
		MyCouponStateRedeemed:           5,
		MyCouponStateInvalidated:        6,
	}
	if !ValidMyCouponState(MyCouponStateAll) {
		t.Fatal("all filter is not valid")
	}
	for state, rank := range want {
		if !ValidMyCouponState(state) ||
			MyCouponStateSortRank(state) != rank {
			t.Fatalf("state %q rank = %d", state, MyCouponStateSortRank(state))
		}
	}
	if ValidMyCouponState("unknown") ||
		MyCouponStateSortRank(MyCouponStateAll) != 0 {
		t.Fatal("invalid state metadata accepted")
	}
}
