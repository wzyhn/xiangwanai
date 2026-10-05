// Package contribution owns Xiangwan's append-only contribution facts.
package contribution

import (
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
)

type Type string

const TypeHostCheckin Type = `host_checkin`

type EntryKind string

const (
	EntryKindEarned   EntryKind = `earned`
	EntryKindReversed EntryKind = `reversed`
)

type State string

const (
	StateActive   State = `active`
	StateReversed State = `reversed`
)

type Entry struct {
	ID                uuid.UUID
	TenantID          uuid.UUID
	PeopleProfileID   uuid.UUID
	PeopleBindingID   uuid.UUID
	PrincipalID       uuid.UUID
	SeriesID          uuid.UUID
	InstanceID        uuid.UUID
	RegistrationID    uuid.UUID
	SessionID         uuid.UUID
	CheckinID         uuid.UUID
	CheckinEventID    uuid.UUID
	RoleBindingID     uuid.UUID
	ContributionType  Type
	EntryKind         EntryKind
	Units             int32
	ReversalOfEntryID *uuid.UUID
	OccurredAt        time.Time
	RecordedAt        time.Time
}

type EarnCommand struct {
	TenantID         uuid.UUID
	PeopleProfileID  uuid.UUID
	PeopleBindingID  uuid.UUID
	PrincipalID      uuid.UUID
	SeriesID         uuid.UUID
	InstanceID       uuid.UUID
	RegistrationID   uuid.UUID
	SessionID        uuid.UUID
	CheckinID        uuid.UUID
	CheckinEventID   uuid.UUID
	RoleBindingID    uuid.UUID
	ContributionType Type
	OccurredAt       time.Time
	RecordedAt       time.Time
}

type ReverseCommand struct {
	Earned         Entry
	CheckinEventID uuid.UUID
	OccurredAt     time.Time
	RecordedAt     time.Time
}

type HistoryItem struct {
	SeriesID         uuid.UUID
	InstanceID       uuid.UUID
	ContributionType Type
	State            State
	EarnedAt         time.Time
	ReversedAt       *time.Time
}

type History struct {
	ActiveCount int
	Items       []HistoryItem
}

var (
	ErrInvalidEntry  = errors.New(`invalid xiangwan Contribution entry`)
	ErrInvalidLedger = errors.New(`invalid xiangwan Contribution ledger`)
)

func Earn(command EarnCommand) (Entry, error) {
	value := Entry{
		ID:               uuid.New(),
		TenantID:         command.TenantID,
		PeopleProfileID:  command.PeopleProfileID,
		PeopleBindingID:  command.PeopleBindingID,
		PrincipalID:      command.PrincipalID,
		SeriesID:         command.SeriesID,
		InstanceID:       command.InstanceID,
		RegistrationID:   command.RegistrationID,
		SessionID:        command.SessionID,
		CheckinID:        command.CheckinID,
		CheckinEventID:   command.CheckinEventID,
		RoleBindingID:    command.RoleBindingID,
		ContributionType: command.ContributionType,
		EntryKind:        EntryKindEarned,
		Units:            1,
		OccurredAt:       command.OccurredAt.UTC(),
		RecordedAt:       command.RecordedAt.UTC(),
	}
	if err := Validate(value); err != nil {
		return Entry{}, err
	}
	return value, nil
}

func Reverse(command ReverseCommand) (Entry, error) {
	if Validate(command.Earned) != nil ||
		command.Earned.EntryKind != EntryKindEarned {
		return Entry{}, ErrInvalidEntry
	}
	reversalOf := command.Earned.ID
	value := command.Earned
	value.ID = uuid.New()
	value.CheckinEventID = command.CheckinEventID
	value.EntryKind = EntryKindReversed
	value.Units = -1
	value.ReversalOfEntryID = &reversalOf
	value.OccurredAt = command.OccurredAt.UTC()
	value.RecordedAt = command.RecordedAt.UTC()
	if value.CheckinEventID == command.Earned.CheckinEventID ||
		value.OccurredAt.Before(command.Earned.OccurredAt) {
		return Entry{}, ErrInvalidEntry
	}
	if err := Validate(value); err != nil {
		return Entry{}, err
	}
	return value, nil
}

func Validate(value Entry) error {
	if value.ID == uuid.Nil ||
		value.TenantID == uuid.Nil ||
		value.PeopleProfileID == uuid.Nil ||
		value.PeopleBindingID == uuid.Nil ||
		value.PrincipalID == uuid.Nil ||
		value.SeriesID == uuid.Nil ||
		value.InstanceID == uuid.Nil ||
		value.RegistrationID == uuid.Nil ||
		value.SessionID == uuid.Nil ||
		value.CheckinID == uuid.Nil ||
		value.CheckinEventID == uuid.Nil ||
		value.RoleBindingID == uuid.Nil ||
		value.ContributionType != TypeHostCheckin ||
		value.OccurredAt.IsZero() ||
		value.RecordedAt.IsZero() ||
		value.RecordedAt.Before(value.OccurredAt) {
		return ErrInvalidEntry
	}
	switch value.EntryKind {
	case EntryKindEarned:
		if value.Units != 1 || value.ReversalOfEntryID != nil {
			return ErrInvalidEntry
		}
	case EntryKindReversed:
		if value.Units != -1 ||
			value.ReversalOfEntryID == nil ||
			*value.ReversalOfEntryID == uuid.Nil ||
			*value.ReversalOfEntryID == value.ID {
			return ErrInvalidEntry
		}
	default:
		return ErrInvalidEntry
	}
	return nil
}

func BuildHistory(entries []Entry) (History, error) {
	result := History{Items: make([]HistoryItem, 0)}
	if len(entries) == 0 {
		return result, nil
	}
	entryIDs := make(map[uuid.UUID]struct{}, len(entries))
	eventIDs := make(map[uuid.UUID]struct{}, len(entries))
	earnedByID := make(map[uuid.UUID]Entry, len(entries))
	itemByEarnedID := make(map[uuid.UUID]int, len(entries))
	businessKeys := make(map[contributionKey]struct{}, len(entries))
	tenantID := entries[0].TenantID
	principalID := entries[0].PrincipalID

	for _, entry := range entries {
		if Validate(entry) != nil ||
			entry.TenantID != tenantID ||
			entry.PrincipalID != principalID {
			return History{}, ErrInvalidLedger
		}
		if _, duplicate := entryIDs[entry.ID]; duplicate {
			return History{}, ErrInvalidLedger
		}
		entryIDs[entry.ID] = struct{}{}
		if _, duplicate := eventIDs[entry.CheckinEventID]; duplicate {
			return History{}, ErrInvalidLedger
		}
		eventIDs[entry.CheckinEventID] = struct{}{}
		if entry.EntryKind != EntryKindEarned {
			continue
		}
		key := contributionKey{
			instanceID: entry.InstanceID,
			typeCode:   entry.ContributionType,
		}
		if _, duplicate := businessKeys[key]; duplicate {
			return History{}, ErrInvalidLedger
		}
		businessKeys[key] = struct{}{}
		earnedByID[entry.ID] = entry
		itemByEarnedID[entry.ID] = len(result.Items)
		result.Items = append(result.Items, HistoryItem{
			SeriesID:         entry.SeriesID,
			InstanceID:       entry.InstanceID,
			ContributionType: entry.ContributionType,
			State:            StateActive,
			EarnedAt:         entry.OccurredAt,
		})
	}

	reversed := make(map[uuid.UUID]struct{}, len(entries))
	for _, entry := range entries {
		if entry.EntryKind != EntryKindReversed {
			continue
		}
		earned, exists := earnedByID[*entry.ReversalOfEntryID]
		if !exists || !sameContributionIdentity(earned, entry) ||
			entry.CheckinID != earned.CheckinID ||
			entry.OccurredAt.Before(earned.OccurredAt) {
			return History{}, ErrInvalidLedger
		}
		if _, duplicate := reversed[earned.ID]; duplicate {
			return History{}, ErrInvalidLedger
		}
		reversed[earned.ID] = struct{}{}
		index := itemByEarnedID[earned.ID]
		reversedAt := entry.OccurredAt
		result.Items[index].State = StateReversed
		result.Items[index].ReversedAt = &reversedAt
	}
	for _, item := range result.Items {
		if item.State == StateActive {
			result.ActiveCount++
		}
	}
	sort.Slice(result.Items, func(left, right int) bool {
		if !result.Items[left].EarnedAt.Equal(result.Items[right].EarnedAt) {
			return result.Items[left].EarnedAt.After(result.Items[right].EarnedAt)
		}
		return result.Items[left].InstanceID.String() >
			result.Items[right].InstanceID.String()
	})
	return result, nil
}

type contributionKey struct {
	instanceID uuid.UUID
	typeCode   Type
}

func sameContributionIdentity(left Entry, right Entry) bool {
	return left.TenantID == right.TenantID &&
		left.PeopleProfileID == right.PeopleProfileID &&
		left.PeopleBindingID == right.PeopleBindingID &&
		left.PrincipalID == right.PrincipalID &&
		left.SeriesID == right.SeriesID &&
		left.InstanceID == right.InstanceID &&
		left.RegistrationID == right.RegistrationID &&
		left.SessionID == right.SessionID &&
		left.RoleBindingID == right.RoleBindingID &&
		left.ContributionType == right.ContributionType
}
