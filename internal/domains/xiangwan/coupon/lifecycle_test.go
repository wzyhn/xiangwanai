package coupon

import (
	"errors"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/google/uuid"
)

func TestCouponHoldReleaseAndRehold(t *testing.T) {
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
		OriginalPriceCents: 10000,
		HoldExpiresAt:      at.Add(10 * time.Minute),
		At:                 at,
		RecordedAt:         at,
	})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	history = append(history, held)
	projection, err := Project(instrument, history, at)
	if err != nil || projection.Status != StatusHeld ||
		projection.ActiveHold == nil ||
		projection.ActiveHold.ID != held.ID {
		t.Fatalf("Project(held) = %+v, %v", projection, err)
	}

	released, err := Release(ReleaseCommand{
		Instrument: instrument,
		History:    history,
		OrderID:    orderID,
		Reason:     "payment window closed",
		At:         at.Add(time.Minute),
		RecordedAt: at.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	history = append(history, released)
	projection, err = Project(instrument, history, at.Add(time.Minute))
	if err != nil || projection.Status != StatusAvailable {
		t.Fatalf("Project(released) = %+v, %v", projection, err)
	}

	secondOrderID := uuid.New()
	secondHold, err := Hold(HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            secondOrderID,
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: 10000,
		HoldExpiresAt:      at.Add(12 * time.Minute),
		At:                 at.Add(2 * time.Minute),
		RecordedAt:         at.Add(2 * time.Minute),
	})
	if err != nil || secondHold.EntrySequence != 4 {
		t.Fatalf("Hold(second) = %+v, %v", secondHold, err)
	}
}

func TestCouponRedeemIsTerminal(t *testing.T) {
	t.Parallel()

	instrument, history, at := lifecycleFixture(t)
	orderID := uuid.New()
	held, err := Hold(HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            orderID,
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: 10000,
		HoldExpiresAt:      at.Add(10 * time.Minute),
		At:                 at,
		RecordedAt:         at,
	})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
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
		t.Fatalf("Redeem() error = %v", err)
	}
	history = append(history, redeemed)
	projection, err := Project(instrument, history, at.Add(time.Minute))
	if err != nil || projection.Status != StatusRedeemed ||
		projection.TerminalEntry == nil ||
		projection.TerminalEntry.ID != redeemed.ID {
		t.Fatalf("Project(redeemed) = %+v, %v", projection, err)
	}
	if _, err := Hold(HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            uuid.New(),
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: 10000,
		HoldExpiresAt:      at.Add(20 * time.Minute),
		At:                 at.Add(2 * time.Minute),
		RecordedAt:         at.Add(2 * time.Minute),
	}); !errors.Is(err, ErrCouponUnavailable) {
		t.Fatalf("Hold(redeemed) error = %v", err)
	}
}

func TestCouponHoldValidatesScopeExpiryAndOrder(t *testing.T) {
	t.Parallel()

	instrument, history, at := lifecycleFixture(t)
	base := HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            uuid.New(),
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: 10000,
		HoldExpiresAt:      at.Add(10 * time.Minute),
		At:                 at,
		RecordedAt:         at,
	}
	tests := []struct {
		name   string
		mutate func(*HoldCommand)
		want   error
	}{
		{
			name: "activity scope",
			mutate: func(command *HoldCommand) {
				command.ActivityType = activity.ActivityTypeCourse
			},
			want: ErrCouponScopeMismatch,
		},
		{
			name: "minimum order",
			mutate: func(command *HoldCommand) {
				command.OriginalPriceCents = 4999
			},
			want: ErrCouponOrderMismatch,
		},
		{
			name: "hold crosses expiry",
			mutate: func(command *HoldCommand) {
				command.HoldExpiresAt = instrument.ExpiresAt.Add(time.Second)
			},
			want: ErrCouponOrderMismatch,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := base
			test.mutate(&command)
			if _, err := Hold(command); !errors.Is(err, test.want) {
				t.Fatalf("Hold() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCheckinCorrectionInvalidatesOrEscalates(t *testing.T) {
	t.Parallel()

	instrument, history, at := lifecycleFixture(t)
	command := CheckinCorrectionCommand{
		Instrument:            instrument,
		History:               history,
		RevokedCheckinEventID: uuid.New(),
		ActorID:               uuid.New(),
		Reason:                "incorrect invited-guest Checkin",
		At:                    at,
		RecordedAt:            at,
	}
	invalidated, err := CorrectForRevokedCheckin(command)
	if err != nil || invalidated.EntryType != EntryTypeInvalidated {
		t.Fatalf("CorrectForRevokedCheckin(available) = %+v, %v", invalidated, err)
	}
	projection, err := Project(
		instrument,
		append(history, invalidated),
		at,
	)
	if err != nil || projection.Status != StatusInvalidated {
		t.Fatalf("Project(invalidated) = %+v, %v", projection, err)
	}

	held, err := Hold(HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            uuid.New(),
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: 10000,
		HoldExpiresAt:      at.Add(10 * time.Minute),
		At:                 at,
		RecordedAt:         at,
	})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	command.History = append(history, held)
	escalated, err := CorrectForRevokedCheckin(command)
	if err != nil ||
		escalated.EntryType != EntryTypeCorrectionRequired {
		t.Fatalf("CorrectForRevokedCheckin(held) = %+v, %v", escalated, err)
	}
	projection, err = Project(
		instrument,
		append(command.History, escalated),
		at,
	)
	if err != nil || projection.Status != StatusHeld ||
		!projection.CorrectionRequired {
		t.Fatalf("Project(escalated) = %+v, %v", projection, err)
	}
	command.History = append(command.History, escalated)
	released, err := Release(ReleaseCommand{
		Instrument: instrument, History: command.History, OrderID: *held.OrderID,
		Reason: "Order closed after Checkin revocation",
		At:     at.Add(time.Minute), RecordedAt: at.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("release after revocation: %v", err)
	}
	command.History = append(command.History, released)
	command.RecordedAt = at.Add(2 * time.Minute)
	invalidated, err = CorrectForRevokedCheckin(command)
	if err != nil || invalidated.EntryType != EntryTypeInvalidated ||
		!invalidated.OccurredAt.Equal(command.At) {
		t.Fatalf("invalidate later-released Coupon = %+v, %v", invalidated, err)
	}
	projection, err = Project(instrument, append(command.History, invalidated), command.RecordedAt)
	if err != nil || projection.Status != StatusInvalidated {
		t.Fatalf("Project(later invalidation) = %+v, %v", projection, err)
	}
}

func TestProjectRejectsSequenceOrRelatedEntryDrift(t *testing.T) {
	t.Parallel()

	instrument, history, at := lifecycleFixture(t)
	held, err := Hold(HoldCommand{
		Instrument:         instrument,
		History:            history,
		OrderID:            uuid.New(),
		RegistrationID:     uuid.New(),
		SeriesID:           uuid.New(),
		ActivityType:       activity.ActivityTypeAIRoundtable,
		OriginalPriceCents: 10000,
		HoldExpiresAt:      at.Add(10 * time.Minute),
		At:                 at,
		RecordedAt:         at,
	})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	held.EntrySequence = 7
	if _, err := Project(
		instrument,
		append(history, held),
		at,
	); !errors.Is(err, ErrInvalidLedger) {
		t.Fatalf("Project(sequence drift) error = %v", err)
	}
}

func lifecycleFixture(
	t *testing.T,
) (Coupon, []Entry, time.Time) {
	t.Helper()
	grantedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	grant, err := NewInitialGuestGrant(InitialGuestGrantCommand{
		TenantID:    uuid.New(),
		PrincipalID: uuid.New(),
		Source: SourceFacts{
			PeopleProfileID: uuid.New(),
			PeopleBindingID: uuid.New(),
			RoleBindingID:   uuid.New(),
			CheckinID:       uuid.New(),
			CheckinEventID:  uuid.New(),
		},
		Policy:      configuredPolicy(),
		CheckedInAt: grantedAt,
		RecordedAt:  grantedAt,
	})
	if err != nil {
		t.Fatalf("NewInitialGuestGrant() error = %v", err)
	}
	return grant.Coupons[0],
		[]Entry{grant.Entries[0]},
		grantedAt.Add(time.Hour)
}
