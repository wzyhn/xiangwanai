package contribution

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestEarnAndReverseProduceAppendOnlyFacts(t *testing.T) {
	t.Parallel()

	earned := mustEarn(t, time.Now().UTC())
	reversedAt := earned.OccurredAt.Add(time.Hour)
	reversal, err := Reverse(ReverseCommand{
		Earned:         earned,
		CheckinEventID: uuid.New(),
		OccurredAt:     reversedAt,
		RecordedAt:     reversedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`Reverse() error = %v`, err)
	}
	if reversal.ID == earned.ID ||
		reversal.EntryKind != EntryKindReversed ||
		reversal.Units != -1 ||
		reversal.ReversalOfEntryID == nil ||
		*reversal.ReversalOfEntryID != earned.ID ||
		!reversal.OccurredAt.Equal(reversedAt) {
		t.Fatalf(`Reverse() = %+v`, reversal)
	}
	if earned.EntryKind != EntryKindEarned || earned.Units != 1 ||
		earned.ReversalOfEntryID != nil {
		t.Fatalf(`earned fact mutated = %+v`, earned)
	}
}

func TestBuildHistoryCountsActiveAndRetainsReversal(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	reversedEarned := mustEarn(t, now.Add(-2*time.Hour))
	reversal, err := Reverse(ReverseCommand{
		Earned:         reversedEarned,
		CheckinEventID: uuid.New(),
		OccurredAt:     now.Add(-time.Hour),
		RecordedAt:     now,
	})
	if err != nil {
		t.Fatalf(`Reverse() error = %v`, err)
	}
	active := mustEarn(t, now)
	active.TenantID = reversedEarned.TenantID
	active.PrincipalID = reversedEarned.PrincipalID
	active.InstanceID = uuid.New()
	active.CheckinID = uuid.New()
	active.CheckinEventID = uuid.New()
	active.RegistrationID = uuid.New()
	active.SessionID = uuid.New()
	active.RoleBindingID = uuid.New()

	history, err := BuildHistory([]Entry{reversal, active, reversedEarned})
	if err != nil {
		t.Fatalf(`BuildHistory() error = %v`, err)
	}
	if history.ActiveCount != 1 || len(history.Items) != 2 ||
		history.Items[0].InstanceID != active.InstanceID ||
		history.Items[0].State != StateActive ||
		history.Items[1].State != StateReversed ||
		history.Items[1].ReversedAt == nil {
		t.Fatalf(`BuildHistory() = %+v`, history)
	}
}

func TestBuildHistoryRejectsConflictingOrOrphanedFacts(t *testing.T) {
	t.Parallel()

	earned := mustEarn(t, time.Now().UTC())
	duplicateBusinessKey := earned
	duplicateBusinessKey.ID = uuid.New()
	duplicateBusinessKey.CheckinID = uuid.New()
	duplicateBusinessKey.CheckinEventID = uuid.New()
	orphaned, err := Reverse(ReverseCommand{
		Earned:         earned,
		CheckinEventID: uuid.New(),
		OccurredAt:     earned.OccurredAt.Add(time.Minute),
		RecordedAt:     earned.RecordedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`Reverse() error = %v`, err)
	}
	orphanID := uuid.New()
	orphaned.ReversalOfEntryID = &orphanID

	for name, entries := range map[string][]Entry{
		`duplicate id`:           {earned, earned},
		`duplicate business key`: {earned, duplicateBusinessKey},
		`orphaned reversal`:      {orphaned},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := BuildHistory(entries); !errors.Is(
				err,
				ErrInvalidLedger,
			) {
				t.Fatalf(`BuildHistory() error = %v`, err)
			}
		})
	}
}

func TestContributionCommandsRejectInvalidSourceFacts(t *testing.T) {
	t.Parallel()

	valid := earnCommand(time.Now().UTC())
	invalid := valid
	invalid.PeopleBindingID = uuid.Nil
	if _, err := Earn(invalid); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf(`Earn(invalid) error = %v`, err)
	}
	earned := mustEarn(t, valid.OccurredAt)
	if _, err := Reverse(ReverseCommand{
		Earned:         earned,
		CheckinEventID: earned.CheckinEventID,
		OccurredAt:     earned.OccurredAt,
		RecordedAt:     earned.RecordedAt,
	}); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf(`Reverse(same event) error = %v`, err)
	}
}

func mustEarn(t *testing.T, at time.Time) Entry {
	t.Helper()
	value, err := Earn(earnCommand(at))
	if err != nil {
		t.Fatalf(`Earn() error = %v`, err)
	}
	return value
}

func earnCommand(at time.Time) EarnCommand {
	return EarnCommand{
		TenantID:         uuid.New(),
		PeopleProfileID:  uuid.New(),
		PeopleBindingID:  uuid.New(),
		PrincipalID:      uuid.New(),
		SeriesID:         uuid.New(),
		InstanceID:       uuid.New(),
		RegistrationID:   uuid.New(),
		SessionID:        uuid.New(),
		CheckinID:        uuid.New(),
		CheckinEventID:   uuid.New(),
		RoleBindingID:    uuid.New(),
		ContributionType: TypeHostCheckin,
		OccurredAt:       at,
		RecordedAt:       at.Add(time.Minute),
	}
}
