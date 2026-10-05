package checkin

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewCreatesCheckedInFactInUTC(t *testing.T) {
	t.Parallel()

	location := time.FixedZone(`CST`, 8*60*60)
	at := time.Date(2026, time.September, 20, 18, 30, 0, 0, location)
	command := validNewCommand(at)

	got, err := New(command)
	if err != nil {
		t.Fatalf(`New() error = %v`, err)
	}
	if got.ID == uuid.Nil ||
		got.TenantID != command.TenantID ||
		got.RegistrationID != command.RegistrationID ||
		got.SeriesID != command.SeriesID ||
		got.InstanceID != command.InstanceID ||
		got.SessionID != command.SessionID ||
		got.PrincipalID != command.PrincipalID ||
		got.CheckedInBy != command.CheckedInBy ||
		got.CheckinStatus != StatusCheckedIn ||
		got.Version != 1 ||
		!got.CheckedInAt.Equal(at) ||
		got.CheckedInAt.Location() != time.UTC ||
		!got.CreatedAt.Equal(got.CheckedInAt) ||
		!got.UpdatedAt.Equal(got.CheckedInAt) ||
		got.RevokedBy != nil ||
		got.RevokedAt != nil ||
		got.RevocationReason != nil {
		t.Fatalf(`New() = %+v`, got)
	}
	if err := Validate(got); err != nil {
		t.Fatalf(`Validate(New()) error = %v`, err)
	}
}

func TestNewRejectsIncompleteIdentityAndZeroTime(t *testing.T) {
	t.Parallel()

	base := validNewCommand(time.Now().UTC())
	tests := []struct {
		name   string
		mutate func(*NewCommand)
	}{
		{`tenant`, func(value *NewCommand) { value.TenantID = uuid.Nil }},
		{`Registration`, func(value *NewCommand) { value.RegistrationID = uuid.Nil }},
		{`Series`, func(value *NewCommand) { value.SeriesID = uuid.Nil }},
		{`Instance`, func(value *NewCommand) { value.InstanceID = uuid.Nil }},
		{`Session`, func(value *NewCommand) { value.SessionID = uuid.Nil }},
		{`principal`, func(value *NewCommand) { value.PrincipalID = uuid.Nil }},
		{`actor`, func(value *NewCommand) { value.CheckedInBy = uuid.Nil }},
		{`time`, func(value *NewCommand) { value.At = time.Time{} }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			command := base
			test.mutate(&command)
			if _, err := New(command); !errors.Is(err, ErrInvalidCheckin) {
				t.Fatalf(`New() error = %v, want %v`, err, ErrInvalidCheckin)
			}
		})
	}
}

func TestRevokeCreatesOneTerminalRevocation(t *testing.T) {
	t.Parallel()

	current := mustNewCheckin(t, time.Date(2026, time.September, 20, 10, 0, 0, 0, time.UTC))
	actorID := uuid.New()
	at := current.UpdatedAt.Add(10 * time.Minute)

	got, err := Revoke(current, RevokeCommand{
		RevokedBy: actorID,
		Reason:    `  duplicate scan corrected  `,
		At:        at,
	})
	if err != nil {
		t.Fatalf(`Revoke() error = %v`, err)
	}
	if got.CheckinStatus != StatusRevoked ||
		got.Version != 2 ||
		got.RevokedBy == nil || *got.RevokedBy != actorID ||
		got.RevokedAt == nil || !got.RevokedAt.Equal(at) ||
		got.RevocationReason == nil || *got.RevocationReason != `duplicate scan corrected` ||
		!got.UpdatedAt.Equal(at) {
		t.Fatalf(`Revoke() = %+v`, got)
	}
	if current.CheckinStatus != StatusCheckedIn || current.Version != 1 || current.RevokedBy != nil {
		t.Fatalf(`Revoke() mutated source = %+v`, current)
	}
	if err := Validate(got); err != nil {
		t.Fatalf(`Validate(Revoke()) error = %v`, err)
	}
	if _, err := Revoke(got, RevokeCommand{
		RevokedBy: actorID,
		Reason:    `again`,
		At:        at.Add(time.Minute),
	}); !errors.Is(err, ErrCheckinTerminal) {
		t.Fatalf(`second Revoke() error = %v, want %v`, err, ErrCheckinTerminal)
	}
}

func TestRevokeRejectsInvalidCommand(t *testing.T) {
	t.Parallel()

	current := mustNewCheckin(t, time.Now().UTC())
	tests := []RevokeCommand{
		{Reason: `valid`, At: current.UpdatedAt},
		{RevokedBy: uuid.New(), Reason: `   `, At: current.UpdatedAt},
		{RevokedBy: uuid.New(), Reason: strings.Repeat(`界`, 501), At: current.UpdatedAt},
		{RevokedBy: uuid.New(), Reason: `valid`},
		{RevokedBy: uuid.New(), Reason: `valid`, At: current.UpdatedAt.Add(-time.Nanosecond)},
	}
	for index, command := range tests {
		if _, err := Revoke(current, command); !errors.Is(err, ErrInvalidCheckin) {
			t.Fatalf(`Revoke(case %d) error = %v, want %v`, index, err, ErrInvalidCheckin)
		}
	}
}

func TestNewEventCapturesCheckedInAndRevokedTransitions(t *testing.T) {
	t.Parallel()

	checkedIn := mustNewCheckin(t, time.Now().UTC())
	created, err := NewEvent(nil, checkedIn, `scan:create:001`)
	if err != nil {
		t.Fatalf(`NewEvent(create) error = %v`, err)
	}
	if created.ID == uuid.Nil ||
		created.TenantID != checkedIn.TenantID ||
		created.CheckinID != checkedIn.ID ||
		created.RegistrationID != checkedIn.RegistrationID ||
		created.SessionID != checkedIn.SessionID ||
		created.EventSequence != 1 ||
		created.EventType != EventTypeCheckedIn ||
		created.FromStatus != nil ||
		created.ToStatus != StatusCheckedIn ||
		created.ActorID != checkedIn.CheckedInBy ||
		created.Reason != nil ||
		!created.OccurredAt.Equal(checkedIn.UpdatedAt) ||
		created.ResultingCheckinVersion != 1 {
		t.Fatalf(`NewEvent(create) = %+v`, created)
	}

	revoked, err := Revoke(checkedIn, RevokeCommand{
		RevokedBy: uuid.New(),
		Reason:    `operator correction`,
		At:        checkedIn.UpdatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`Revoke() error = %v`, err)
	}
	revocation, err := NewEvent(&checkedIn, revoked, `scan:revoke:001`)
	if err != nil {
		t.Fatalf(`NewEvent(revoke) error = %v`, err)
	}
	if revocation.EventSequence != 2 ||
		revocation.EventType != EventTypeRevoked ||
		revocation.FromStatus == nil || *revocation.FromStatus != StatusCheckedIn ||
		revocation.ToStatus != StatusRevoked ||
		revocation.ActorID != *revoked.RevokedBy ||
		revocation.Reason == nil || *revocation.Reason != *revoked.RevocationReason ||
		revocation.ResultingCheckinVersion != 2 {
		t.Fatalf(`NewEvent(revoke) = %+v`, revocation)
	}
	if err := ValidateEvent(revocation); err != nil {
		t.Fatalf(`ValidateEvent() error = %v`, err)
	}
}

func TestNewEventRejectsMismatchedTransitionAndBadKey(t *testing.T) {
	t.Parallel()

	checkedIn := mustNewCheckin(t, time.Now().UTC())
	if _, err := NewEvent(nil, checkedIn, `contains spaces`); !errors.Is(err, ErrInvalidCheckinEvent) {
		t.Fatalf(`NewEvent(bad key) error = %v, want %v`, err, ErrInvalidCheckinEvent)
	}
	revoked, err := Revoke(checkedIn, RevokeCommand{
		RevokedBy: uuid.New(),
		Reason:    `correction`,
		At:        checkedIn.UpdatedAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf(`Revoke() error = %v`, err)
	}
	mismatched := checkedIn
	mismatched.ID = uuid.New()
	if _, err := NewEvent(&mismatched, revoked, `scan:revoke:002`); !errors.Is(err, ErrCheckinEventTransition) {
		t.Fatalf(`NewEvent(mismatch) error = %v, want %v`, err, ErrCheckinEventTransition)
	}
	if _, err := NewEvent(nil, revoked, `scan:create:002`); !errors.Is(err, ErrCheckinEventTransition) {
		t.Fatalf(`NewEvent(revoked create) error = %v, want %v`, err, ErrCheckinEventTransition)
	}
}

func TestValidateRejectsImpossibleShapes(t *testing.T) {
	t.Parallel()

	valid := mustNewCheckin(t, time.Now().UTC())
	tests := []struct {
		name   string
		mutate func(*Checkin)
	}{
		{`nil identity`, func(value *Checkin) { value.ID = uuid.Nil }},
		{`bad status`, func(value *Checkin) { value.CheckinStatus = Status(`unknown`) }},
		{`checked version`, func(value *Checkin) { value.Version = 2 }},
		{`created before check-in`, func(value *Checkin) { value.CreatedAt = value.CheckedInAt.Add(-time.Second) }},
		{`updated before created`, func(value *Checkin) { value.UpdatedAt = value.CreatedAt.Add(-time.Second) }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := valid
			test.mutate(&value)
			if err := Validate(value); !errors.Is(err, ErrInvalidCheckin) {
				t.Fatalf(`Validate() error = %v, want %v`, err, ErrInvalidCheckin)
			}
		})
	}
}

func validNewCommand(at time.Time) NewCommand {
	return NewCommand{
		TenantID:       uuid.New(),
		RegistrationID: uuid.New(),
		SeriesID:       uuid.New(),
		InstanceID:     uuid.New(),
		SessionID:      uuid.New(),
		PrincipalID:    uuid.New(),
		CheckedInBy:    uuid.New(),
		At:             at,
	}
}

func mustNewCheckin(t *testing.T, at time.Time) Checkin {
	t.Helper()
	value, err := New(validNewCommand(at))
	if err != nil {
		t.Fatalf(`New() error = %v`, err)
	}
	return value
}
