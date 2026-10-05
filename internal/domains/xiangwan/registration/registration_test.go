package registration

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewRegistrationSeparatesFreeAndPaidParticipation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 12, 4, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	command := validNewRegistrationCommand(now)

	free, err := NewRegistration(command)
	if err != nil {
		t.Fatalf("NewRegistration(free) error = %v", err)
	}
	if free.ID == uuid.Nil ||
		free.ParticipationStatus != ParticipationStatusConfirmed ||
		free.ConfirmedAt == nil ||
		!free.ConfirmedAt.Equal(now.UTC()) ||
		free.Version != 1 ||
		free.CreatedAt.Location() != time.UTC {
		t.Fatalf("NewRegistration(free) = %+v", free)
	}

	command.RequiresPayment = true
	command.IdempotencyKey = "registration:paid:1"
	paid, err := NewRegistration(command)
	if err != nil {
		t.Fatalf("NewRegistration(paid) error = %v", err)
	}
	if paid.ParticipationStatus != ParticipationStatusPendingPayment || paid.ConfirmedAt != nil {
		t.Fatalf("NewRegistration(paid) = %+v", paid)
	}
}

func TestNewRegistrationRejectsMalformedIdentityAndIdempotency(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name   string
		mutate func(*NewRegistrationCommand)
	}{
		{name: "tenant", mutate: func(command *NewRegistrationCommand) { command.TenantID = uuid.Nil }},
		{name: "series", mutate: func(command *NewRegistrationCommand) { command.SeriesID = uuid.Nil }},
		{name: "instance", mutate: func(command *NewRegistrationCommand) { command.InstanceID = uuid.Nil }},
		{name: "session", mutate: func(command *NewRegistrationCommand) { command.SessionID = uuid.Nil }},
		{name: "principal", mutate: func(command *NewRegistrationCommand) { command.PrincipalID = uuid.Nil }},
		{name: "blank key", mutate: func(command *NewRegistrationCommand) { command.IdempotencyKey = " " }},
		{name: "oversize key", mutate: func(command *NewRegistrationCommand) { command.IdempotencyKey = strings.Repeat("a", 129) }},
		{name: "invalid key", mutate: func(command *NewRegistrationCommand) { command.IdempotencyKey = "key/one" }},
		{name: "time", mutate: func(command *NewRegistrationCommand) { command.Now = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := validNewRegistrationCommand(now)
			test.mutate(&command)
			if _, err := NewRegistration(command); !errors.Is(err, ErrInvalidRegistration) {
				t.Fatalf("NewRegistration() error = %v", err)
			}
		})
	}
}

func TestConfirmRegistrationIsIdempotentAndCannotReviveCancelled(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	command := validNewRegistrationCommand(now)
	command.RequiresPayment = true
	pending, err := NewRegistration(command)
	if err != nil {
		t.Fatalf("NewRegistration() error = %v", err)
	}

	confirmed, changed, err := ConfirmRegistration(pending, now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("ConfirmRegistration() = %+v, %v, %v", confirmed, changed, err)
	}
	if confirmed.ParticipationStatus != ParticipationStatusConfirmed ||
		confirmed.ConfirmedAt == nil ||
		confirmed.Version != 2 {
		t.Fatalf("confirmed Registration = %+v", confirmed)
	}
	retried, changed, err := ConfirmRegistration(confirmed, now.Add(2*time.Minute))
	if err != nil || changed || retried.ConfirmedAt == confirmed.ConfirmedAt || !retried.ConfirmedAt.Equal(*confirmed.ConfirmedAt) {
		t.Fatalf("ConfirmRegistration(retry) = %+v, %v, %v", retried, changed, err)
	}

	cancelled, _, err := CancelRegistration(confirmed, "user_cancelled", now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("CancelRegistration() error = %v", err)
	}
	if _, _, err := ConfirmRegistration(cancelled, now.Add(4*time.Minute)); !errors.Is(err, ErrRegistrationTerminal) {
		t.Fatalf("ConfirmRegistration(cancelled) error = %v", err)
	}
}

func TestCancelRegistrationRecordsOneImmutableFact(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	current, err := NewRegistration(validNewRegistrationCommand(now))
	if err != nil {
		t.Fatalf("NewRegistration() error = %v", err)
	}
	cancelled, changed, err := CancelRegistration(current, "  user_cancelled  ", now.Add(time.Minute))
	if err != nil || !changed {
		t.Fatalf("CancelRegistration() = %+v, %v, %v", cancelled, changed, err)
	}
	if cancelled.ParticipationStatus != ParticipationStatusCancelled ||
		cancelled.CancelledAt == nil ||
		cancelled.CancellationReason == nil ||
		*cancelled.CancellationReason != "user_cancelled" ||
		cancelled.Version != 2 {
		t.Fatalf("cancelled Registration = %+v", cancelled)
	}

	retried, changed, err := CancelRegistration(cancelled, "user_cancelled", now.Add(2*time.Minute))
	if err != nil || changed || retried.CancelledAt == cancelled.CancelledAt || !retried.CancelledAt.Equal(*cancelled.CancelledAt) {
		t.Fatalf("CancelRegistration(retry) = %+v, %v, %v", retried, changed, err)
	}
	if _, _, err := CancelRegistration(cancelled, "different_reason", now.Add(2*time.Minute)); !errors.Is(
		err,
		ErrRegistrationCancellationConflict,
	) {
		t.Fatalf("CancelRegistration(conflict) error = %v", err)
	}
	if _, _, err := CancelRegistration(current, "user_cancelled", now.Add(-time.Minute)); !errors.Is(
		err,
		ErrInvalidRegistration,
	) {
		t.Fatalf("CancelRegistration(early) error = %v", err)
	}
}

func validNewRegistrationCommand(now time.Time) NewRegistrationCommand {
	return NewRegistrationCommand{
		TenantID:       uuid.New(),
		SeriesID:       uuid.New(),
		InstanceID:     uuid.New(),
		SessionID:      uuid.New(),
		PrincipalID:    uuid.New(),
		IdempotencyKey: "registration:free:1",
		Now:            now,
	}
}
