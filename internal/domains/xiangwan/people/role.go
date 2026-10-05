package people

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

type InstanceRoleCode string

const (
	InstanceRoleHost             InstanceRoleCode = `host`
	InstanceRoleInvitedGuest     InstanceRoleCode = `invited_guest`
	InstanceRoleCourseInstructor InstanceRoleCode = `course_instructor`
	InstanceRoleEventSpeaker     InstanceRoleCode = `event_speaker`
)

type RoleStatus string

const (
	RoleStatusActive  RoleStatus = `active`
	RoleStatusRevoked RoleStatus = `revoked`
)

type InstanceRoleBinding struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	SeriesID         uuid.UUID
	InstanceID       uuid.UUID
	PrincipalID      uuid.UUID
	RoleCode         InstanceRoleCode
	RoleStatus       RoleStatus
	GrantReason      string
	GrantedBy        uuid.UUID
	GrantedAt        time.Time
	RevokedBy        *uuid.UUID
	RevokedAt        *time.Time
	RevocationReason *string
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type NewInstanceRoleBindingCommand struct {
	TenantID    uuid.UUID
	SeriesID    uuid.UUID
	InstanceID  uuid.UUID
	PrincipalID uuid.UUID
	RoleCode    InstanceRoleCode
	GrantReason string
	ActorID     uuid.UUID
	At          time.Time
}

type RevokeInstanceRoleBindingCommand struct {
	ActorID uuid.UUID
	Reason  string
	At      time.Time
}

var (
	ErrInvalidInstanceRoleBinding = errors.New(
		`invalid xiangwan Instance role binding`,
	)
	ErrInstanceRoleBindingTerminal = errors.New(
		`xiangwan Instance role binding is terminal`,
	)
)

func NewInstanceRoleBinding(
	command NewInstanceRoleBindingCommand,
) (InstanceRoleBinding, error) {
	reason := strings.TrimSpace(command.GrantReason)
	if command.TenantID == uuid.Nil ||
		command.SeriesID == uuid.Nil ||
		command.InstanceID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		!validInstanceRoleCode(command.RoleCode) ||
		reason == `` ||
		len([]rune(reason)) > 500 ||
		command.ActorID == uuid.Nil ||
		command.At.IsZero() {
		return InstanceRoleBinding{}, ErrInvalidInstanceRoleBinding
	}
	at := command.At.UTC()
	value := InstanceRoleBinding{
		ID:          uuid.New(),
		TenantID:    command.TenantID,
		SeriesID:    command.SeriesID,
		InstanceID:  command.InstanceID,
		PrincipalID: command.PrincipalID,
		RoleCode:    command.RoleCode,
		RoleStatus:  RoleStatusActive,
		GrantReason: reason,
		GrantedBy:   command.ActorID,
		GrantedAt:   at,
		Version:     1,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
	if err := ValidateInstanceRoleBinding(value); err != nil {
		return InstanceRoleBinding{}, err
	}
	return value, nil
}

func RevokeInstanceRoleBinding(
	current InstanceRoleBinding,
	command RevokeInstanceRoleBindingCommand,
) (InstanceRoleBinding, error) {
	if err := ValidateInstanceRoleBinding(current); err != nil {
		return InstanceRoleBinding{}, err
	}
	if current.RoleStatus == RoleStatusRevoked {
		return InstanceRoleBinding{}, ErrInstanceRoleBindingTerminal
	}
	reason := strings.TrimSpace(command.Reason)
	if command.ActorID == uuid.Nil ||
		reason == `` ||
		len([]rune(reason)) > 500 ||
		command.At.IsZero() ||
		command.At.Before(current.UpdatedAt) {
		return InstanceRoleBinding{}, ErrInvalidInstanceRoleBinding
	}
	at := command.At.UTC()
	actorID := command.ActorID
	result := current
	result.RoleStatus = RoleStatusRevoked
	result.RevokedBy = &actorID
	result.RevokedAt = &at
	result.RevocationReason = &reason
	result.Version = 2
	result.UpdatedAt = at
	return result, ValidateInstanceRoleBinding(result)
}

func ValidateInstanceRoleBinding(value InstanceRoleBinding) error {
	if value.ID == uuid.Nil ||
		value.TenantID == uuid.Nil ||
		value.SeriesID == uuid.Nil ||
		value.InstanceID == uuid.Nil ||
		value.PrincipalID == uuid.Nil ||
		!validInstanceRoleCode(value.RoleCode) ||
		value.GrantReason == `` ||
		value.GrantReason != strings.TrimSpace(value.GrantReason) ||
		len([]rune(value.GrantReason)) > 500 ||
		value.GrantedBy == uuid.Nil ||
		value.GrantedAt.IsZero() ||
		!value.CreatedAt.Equal(value.GrantedAt) {
		return ErrInvalidInstanceRoleBinding
	}
	switch value.RoleStatus {
	case RoleStatusActive:
		if value.Version != 1 ||
			value.RevokedBy != nil ||
			value.RevokedAt != nil ||
			value.RevocationReason != nil ||
			!value.UpdatedAt.Equal(value.GrantedAt) {
			return ErrInvalidInstanceRoleBinding
		}
	case RoleStatusRevoked:
		if value.Version != 2 ||
			value.RevokedBy == nil ||
			*value.RevokedBy == uuid.Nil ||
			value.RevokedAt == nil ||
			value.RevokedAt.Before(value.GrantedAt) ||
			value.RevocationReason == nil ||
			*value.RevocationReason == `` ||
			*value.RevocationReason != strings.TrimSpace(
				*value.RevocationReason,
			) ||
			len([]rune(*value.RevocationReason)) > 500 ||
			!value.UpdatedAt.Equal(*value.RevokedAt) {
			return ErrInvalidInstanceRoleBinding
		}
	default:
		return ErrInvalidInstanceRoleBinding
	}
	return nil
}

func validInstanceRoleCode(value InstanceRoleCode) bool {
	switch value {
	case InstanceRoleHost,
		InstanceRoleInvitedGuest,
		InstanceRoleCourseInstructor,
		InstanceRoleEventSpeaker:
		return true
	default:
		return false
	}
}
