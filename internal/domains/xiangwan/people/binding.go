package people

import (
	"crypto/sha256"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type EvidenceDigest [sha256.Size]byte

type BindingStatus string

const (
	BindingStatusActive  BindingStatus = `active`
	BindingStatusRevoked BindingStatus = `revoked`
)

type Binding struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	PeopleProfileID  uuid.UUID
	PrincipalID      uuid.UUID
	EvidenceDigest   EvidenceDigest
	BindingStatus    BindingStatus
	BoundBy          uuid.UUID
	BoundAt          time.Time
	RevokedBy        *uuid.UUID
	RevokedAt        *time.Time
	RevocationReason *string
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type NewBindingCommand struct {
	TenantID        uuid.UUID
	PeopleProfileID uuid.UUID
	PrincipalID     uuid.UUID
	EvidenceDigest  EvidenceDigest
	ActorID         uuid.UUID
	At              time.Time
}

type RevokeBindingCommand struct {
	ActorID uuid.UUID
	Reason  string
	At      time.Time
}

var (
	ErrInvalidBinding  = errors.New(`invalid xiangwan People binding`)
	ErrBindingTerminal = errors.New(`xiangwan People binding is terminal`)
)

func NewBinding(command NewBindingCommand) (Binding, error) {
	if command.TenantID == uuid.Nil ||
		command.PeopleProfileID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		command.EvidenceDigest == (EvidenceDigest{}) ||
		command.ActorID == uuid.Nil ||
		command.At.IsZero() {
		return Binding{}, ErrInvalidBinding
	}
	at := command.At.UTC()
	value := Binding{
		ID:              uuid.New(),
		TenantID:        command.TenantID,
		PeopleProfileID: command.PeopleProfileID,
		PrincipalID:     command.PrincipalID,
		EvidenceDigest:  command.EvidenceDigest,
		BindingStatus:   BindingStatusActive,
		BoundBy:         command.ActorID,
		BoundAt:         at,
		Version:         1,
		CreatedAt:       at,
		UpdatedAt:       at,
	}
	if err := ValidateBinding(value); err != nil {
		return Binding{}, err
	}
	return value, nil
}

func RevokeBinding(
	current Binding,
	command RevokeBindingCommand,
) (Binding, error) {
	if err := ValidateBinding(current); err != nil {
		return Binding{}, err
	}
	if current.BindingStatus == BindingStatusRevoked {
		return Binding{}, ErrBindingTerminal
	}
	reason := strings.TrimSpace(command.Reason)
	if command.ActorID == uuid.Nil ||
		reason == `` ||
		len([]rune(reason)) > 500 ||
		command.At.IsZero() ||
		command.At.Before(current.UpdatedAt) {
		return Binding{}, ErrInvalidBinding
	}
	at := command.At.UTC()
	actorID := command.ActorID
	result := current
	result.BindingStatus = BindingStatusRevoked
	result.RevokedBy = &actorID
	result.RevokedAt = &at
	result.RevocationReason = &reason
	result.Version = 2
	result.UpdatedAt = at
	return result, ValidateBinding(result)
}

func ValidateBinding(value Binding) error {
	if value.ID == uuid.Nil ||
		value.TenantID == uuid.Nil ||
		value.PeopleProfileID == uuid.Nil ||
		value.PrincipalID == uuid.Nil ||
		value.EvidenceDigest == (EvidenceDigest{}) ||
		value.BoundBy == uuid.Nil ||
		value.BoundAt.IsZero() ||
		!value.CreatedAt.Equal(value.BoundAt) {
		return ErrInvalidBinding
	}
	switch value.BindingStatus {
	case BindingStatusActive:
		if value.Version != 1 ||
			value.RevokedBy != nil ||
			value.RevokedAt != nil ||
			value.RevocationReason != nil ||
			!value.UpdatedAt.Equal(value.BoundAt) {
			return ErrInvalidBinding
		}
	case BindingStatusRevoked:
		if value.Version != 2 ||
			value.RevokedBy == nil ||
			*value.RevokedBy == uuid.Nil ||
			value.RevokedAt == nil ||
			value.RevokedAt.Before(value.BoundAt) ||
			value.RevocationReason == nil ||
			*value.RevocationReason == `` ||
			*value.RevocationReason != strings.TrimSpace(
				*value.RevocationReason,
			) ||
			len([]rune(*value.RevocationReason)) > 500 ||
			!value.UpdatedAt.Equal(*value.RevokedAt) {
			return ErrInvalidBinding
		}
	default:
		return ErrInvalidBinding
	}
	return nil
}
