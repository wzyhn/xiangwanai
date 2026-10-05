// Package checkin owns Xiangwan attendance facts independently from
// Registration participation, Order payment, and Refund state.
package checkin

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Status string

const (
	StatusCheckedIn Status = "checked_in"
	StatusRevoked   Status = "revoked"
)

type EventType string

const (
	EventTypeCheckedIn EventType = "checked_in"
	EventTypeRevoked   EventType = "revoked"
)

type Checkin struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	RegistrationID   uuid.UUID
	SeriesID         uuid.UUID
	InstanceID       uuid.UUID
	SessionID        uuid.UUID
	PrincipalID      uuid.UUID
	CheckinStatus    Status
	CheckedInBy      uuid.UUID
	CheckedInAt      time.Time
	RevokedBy        *uuid.UUID
	RevokedAt        *time.Time
	RevocationReason *string
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type NewCommand struct {
	TenantID       uuid.UUID
	RegistrationID uuid.UUID
	SeriesID       uuid.UUID
	InstanceID     uuid.UUID
	SessionID      uuid.UUID
	PrincipalID    uuid.UUID
	CheckedInBy    uuid.UUID
	At             time.Time
}

type RevokeCommand struct {
	RevokedBy uuid.UUID
	Reason    string
	At        time.Time
}

type Event struct {
	ID                      uuid.UUID
	TenantID                uuid.UUID
	CheckinID               uuid.UUID
	RegistrationID          uuid.UUID
	SessionID               uuid.UUID
	EventSequence           int64
	EventType               EventType
	IdempotencyKey          string
	FromStatus              *Status
	ToStatus                Status
	ActorID                 uuid.UUID
	Reason                  *string
	OccurredAt              time.Time
	ResultingCheckinVersion int64
	CreatedAt               time.Time
}

var (
	ErrInvalidCheckin  = errors.New("invalid xiangwan Checkin")
	ErrCheckinTerminal = errors.New(
		"xiangwan Checkin is terminal",
	)
	ErrInvalidCheckinEvent = errors.New(
		"invalid xiangwan Checkin event",
	)
	ErrCheckinEventTransition = errors.New(
		"invalid xiangwan Checkin event transition",
	)
)

var idempotencyKeyPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`,
)

func New(command NewCommand) (Checkin, error) {
	if command.TenantID == uuid.Nil ||
		command.RegistrationID == uuid.Nil ||
		command.SeriesID == uuid.Nil ||
		command.InstanceID == uuid.Nil ||
		command.SessionID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		command.CheckedInBy == uuid.Nil ||
		command.At.IsZero() {
		return Checkin{}, ErrInvalidCheckin
	}
	at := command.At.UTC()
	return Checkin{
		ID:             uuid.New(),
		TenantID:       command.TenantID,
		RegistrationID: command.RegistrationID,
		SeriesID:       command.SeriesID,
		InstanceID:     command.InstanceID,
		SessionID:      command.SessionID,
		PrincipalID:    command.PrincipalID,
		CheckinStatus:  StatusCheckedIn,
		CheckedInBy:    command.CheckedInBy,
		CheckedInAt:    at,
		Version:        1,
		CreatedAt:      at,
		UpdatedAt:      at,
	}, nil
}

func Revoke(
	current Checkin,
	command RevokeCommand,
) (Checkin, error) {
	reason := strings.TrimSpace(command.Reason)
	if err := Validate(current); err != nil ||
		command.RevokedBy == uuid.Nil ||
		reason == "" ||
		len([]rune(reason)) > 500 ||
		command.At.IsZero() ||
		command.At.Before(current.UpdatedAt) {
		return Checkin{}, ErrInvalidCheckin
	}
	if current.CheckinStatus == StatusRevoked {
		return Checkin{}, ErrCheckinTerminal
	}
	at := command.At.UTC()
	result := current
	result.CheckinStatus = StatusRevoked
	result.RevokedBy = cloneUUID(&command.RevokedBy)
	result.RevokedAt = &at
	result.RevocationReason = &reason
	result.Version++
	result.UpdatedAt = at
	return result, nil
}

func NewEvent(
	before *Checkin,
	after Checkin,
	idempotencyKey string,
) (Event, error) {
	if !idempotencyKeyPattern.MatchString(idempotencyKey) ||
		Validate(after) != nil {
		return Event{}, ErrInvalidCheckinEvent
	}
	event := Event{
		ID:                      uuid.New(),
		TenantID:                after.TenantID,
		CheckinID:               after.ID,
		RegistrationID:          after.RegistrationID,
		SessionID:               after.SessionID,
		EventSequence:           after.Version,
		IdempotencyKey:          idempotencyKey,
		ToStatus:                after.CheckinStatus,
		OccurredAt:              after.UpdatedAt,
		ResultingCheckinVersion: after.Version,
		CreatedAt:               after.UpdatedAt,
	}
	if before == nil {
		if after.CheckinStatus != StatusCheckedIn ||
			after.Version != 1 {
			return Event{}, ErrCheckinEventTransition
		}
		event.EventType = EventTypeCheckedIn
		event.ActorID = after.CheckedInBy
	} else {
		if Validate(*before) != nil ||
			!sameCheckinIdentity(*before, after) ||
			before.CheckinStatus != StatusCheckedIn ||
			after.CheckinStatus != StatusRevoked ||
			after.Version != before.Version+1 ||
			after.CheckedInBy != before.CheckedInBy ||
			!after.CheckedInAt.Equal(before.CheckedInAt) ||
			!after.CreatedAt.Equal(before.CreatedAt) ||
			after.RevokedBy == nil ||
			after.RevocationReason == nil {
			return Event{}, ErrCheckinEventTransition
		}
		fromStatus := before.CheckinStatus
		event.EventType = EventTypeRevoked
		event.FromStatus = &fromStatus
		event.ActorID = *after.RevokedBy
		event.Reason = cloneString(after.RevocationReason)
	}
	if err := ValidateEvent(event); err != nil {
		return Event{}, err
	}
	return event, nil
}

func Validate(value Checkin) error {
	if value.ID == uuid.Nil ||
		value.TenantID == uuid.Nil ||
		value.RegistrationID == uuid.Nil ||
		value.SeriesID == uuid.Nil ||
		value.InstanceID == uuid.Nil ||
		value.SessionID == uuid.Nil ||
		value.PrincipalID == uuid.Nil ||
		value.CheckedInBy == uuid.Nil ||
		value.CheckedInAt.IsZero() ||
		value.Version < 1 ||
		value.CreatedAt.IsZero() ||
		value.UpdatedAt.IsZero() ||
		!value.CreatedAt.Equal(value.CheckedInAt) ||
		value.UpdatedAt.Before(value.CreatedAt) {
		return ErrInvalidCheckin
	}
	switch value.CheckinStatus {
	case StatusCheckedIn:
		if value.Version != 1 ||
			value.RevokedBy != nil ||
			value.RevokedAt != nil ||
			value.RevocationReason != nil {
			return ErrInvalidCheckin
		}
	case StatusRevoked:
		if value.Version != 2 ||
			value.RevokedBy == nil ||
			*value.RevokedBy == uuid.Nil ||
			value.RevokedAt == nil ||
			value.RevocationReason == nil ||
			*value.RevocationReason != strings.TrimSpace(*value.RevocationReason) ||
			*value.RevocationReason == "" ||
			len([]rune(*value.RevocationReason)) > 500 ||
			value.RevokedAt.Before(value.CheckedInAt) ||
			!value.UpdatedAt.Equal(*value.RevokedAt) {
			return ErrInvalidCheckin
		}
	default:
		return ErrInvalidCheckin
	}
	return nil
}

func ValidateEvent(value Event) error {
	if value.ID == uuid.Nil ||
		value.TenantID == uuid.Nil ||
		value.CheckinID == uuid.Nil ||
		value.RegistrationID == uuid.Nil ||
		value.SessionID == uuid.Nil ||
		value.EventSequence < 1 ||
		!idempotencyKeyPattern.MatchString(value.IdempotencyKey) ||
		value.ActorID == uuid.Nil ||
		value.OccurredAt.IsZero() ||
		value.CreatedAt.IsZero() ||
		value.CreatedAt.Before(value.OccurredAt) ||
		value.ResultingCheckinVersion != value.EventSequence {
		return ErrInvalidCheckinEvent
	}
	switch value.EventType {
	case EventTypeCheckedIn:
		if value.EventSequence != 1 ||
			value.FromStatus != nil ||
			value.ToStatus != StatusCheckedIn ||
			value.Reason != nil {
			return ErrInvalidCheckinEvent
		}
	case EventTypeRevoked:
		if value.EventSequence != 2 ||
			value.FromStatus == nil ||
			*value.FromStatus != StatusCheckedIn ||
			value.ToStatus != StatusRevoked ||
			value.Reason == nil ||
			*value.Reason != strings.TrimSpace(*value.Reason) ||
			*value.Reason == "" ||
			len([]rune(*value.Reason)) > 500 {
			return ErrInvalidCheckinEvent
		}
	default:
		return ErrInvalidCheckinEvent
	}
	return nil
}

func sameCheckinIdentity(left Checkin, right Checkin) bool {
	return left.ID == right.ID &&
		left.TenantID == right.TenantID &&
		left.RegistrationID == right.RegistrationID &&
		left.SeriesID == right.SeriesID &&
		left.InstanceID == right.InstanceID &&
		left.SessionID == right.SessionID &&
		left.PrincipalID == right.PrincipalID
}

func cloneUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
