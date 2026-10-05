// Package people owns Xiangwan public people records, trusted Principal
// bindings, and Instance-scoped role history.
package people

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	MaxProfileDisplayNameRunes  = 120
	MaxProfileHeadlineRunes     = 200
	MaxProfileIntroductionRunes = 4000
)

type ProfileStatus string

const (
	ProfileStatusDraft     ProfileStatus = `draft`
	ProfileStatusPublished ProfileStatus = `published`
	ProfileStatusArchived  ProfileStatus = `archived`
)

type ModerationStatus string

const (
	ModerationStatusPending  ModerationStatus = `pending`
	ModerationStatusApproved ModerationStatus = `approved`
	ModerationStatusRejected ModerationStatus = `rejected`
)

type Profile struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	DisplayName      string
	Headline         *string
	Introduction     string
	ProfileStatus    ProfileStatus
	ModerationStatus ModerationStatus
	ModeratedBy      *uuid.UUID
	ModeratedAt      *time.Time
	CreatedBy        uuid.UUID
	UpdatedBy        uuid.UUID
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type NewProfileCommand struct {
	TenantID     uuid.UUID
	DisplayName  string
	Headline     string
	Introduction string
	ActorID      uuid.UUID
	At           time.Time
}

type ReviseProfileCommand struct {
	DisplayName  string
	Headline     string
	Introduction string
	ActorID      uuid.UUID
	At           time.Time
}

type ReviewProfileCommand struct {
	Decision ModerationStatus
	ActorID  uuid.UUID
	At       time.Time
}

var (
	ErrInvalidProfile       = errors.New(`invalid xiangwan PeopleProfile`)
	ErrProfileTerminal      = errors.New(`xiangwan PeopleProfile is terminal`)
	ErrInvalidProfileReview = errors.New(`invalid xiangwan PeopleProfile review`)
)

func NewProfile(command NewProfileCommand) (Profile, error) {
	displayName, headline, introduction, err := normalizeProfileContent(
		command.DisplayName,
		command.Headline,
		command.Introduction,
	)
	if err != nil ||
		command.TenantID == uuid.Nil ||
		command.ActorID == uuid.Nil ||
		command.At.IsZero() {
		return Profile{}, ErrInvalidProfile
	}
	at := command.At.UTC()
	value := Profile{
		ID:               uuid.New(),
		TenantID:         command.TenantID,
		DisplayName:      displayName,
		Headline:         headline,
		Introduction:     introduction,
		ProfileStatus:    ProfileStatusDraft,
		ModerationStatus: ModerationStatusPending,
		CreatedBy:        command.ActorID,
		UpdatedBy:        command.ActorID,
		Version:          1,
		CreatedAt:        at,
		UpdatedAt:        at,
	}
	if err := ValidateProfile(value); err != nil {
		return Profile{}, err
	}
	return value, nil
}

func ReviseProfile(
	current Profile,
	command ReviseProfileCommand,
) (Profile, error) {
	if err := ValidateProfile(current); err != nil {
		return Profile{}, err
	}
	if current.ProfileStatus == ProfileStatusArchived {
		return Profile{}, ErrProfileTerminal
	}
	displayName, headline, introduction, err := normalizeProfileContent(
		command.DisplayName,
		command.Headline,
		command.Introduction,
	)
	if err != nil ||
		command.ActorID == uuid.Nil ||
		command.At.IsZero() ||
		command.At.Before(current.UpdatedAt) {
		return Profile{}, ErrInvalidProfile
	}
	at := command.At.UTC()
	result := current
	result.DisplayName = displayName
	result.Headline = headline
	result.Introduction = introduction
	result.ProfileStatus = ProfileStatusDraft
	result.ModerationStatus = ModerationStatusPending
	result.ModeratedBy = nil
	result.ModeratedAt = nil
	result.UpdatedBy = command.ActorID
	result.Version++
	result.UpdatedAt = at
	return result, ValidateProfile(result)
}

func ReviewProfile(
	current Profile,
	command ReviewProfileCommand,
) (Profile, error) {
	if err := ValidateProfile(current); err != nil {
		return Profile{}, err
	}
	if current.ProfileStatus == ProfileStatusArchived {
		return Profile{}, ErrProfileTerminal
	}
	if current.ProfileStatus != ProfileStatusDraft ||
		current.ModerationStatus != ModerationStatusPending ||
		command.ActorID == uuid.Nil ||
		command.At.IsZero() ||
		command.At.Before(current.UpdatedAt) ||
		(command.Decision != ModerationStatusApproved &&
			command.Decision != ModerationStatusRejected) {
		return Profile{}, ErrInvalidProfileReview
	}
	at := command.At.UTC()
	actorID := command.ActorID
	result := current
	result.ModerationStatus = command.Decision
	result.ModeratedBy = &actorID
	result.ModeratedAt = &at
	result.UpdatedBy = command.ActorID
	result.Version++
	result.UpdatedAt = at
	if command.Decision == ModerationStatusApproved {
		result.ProfileStatus = ProfileStatusPublished
	} else {
		result.ProfileStatus = ProfileStatusDraft
	}
	return result, ValidateProfile(result)
}

func ArchiveProfile(
	current Profile,
	actorID uuid.UUID,
	at time.Time,
) (Profile, error) {
	if err := ValidateProfile(current); err != nil {
		return Profile{}, err
	}
	if current.ProfileStatus == ProfileStatusArchived {
		return Profile{}, ErrProfileTerminal
	}
	if actorID == uuid.Nil || at.IsZero() || at.Before(current.UpdatedAt) {
		return Profile{}, ErrInvalidProfile
	}
	result := current
	result.ProfileStatus = ProfileStatusArchived
	result.UpdatedBy = actorID
	result.Version++
	result.UpdatedAt = at.UTC()
	return result, ValidateProfile(result)
}

func ValidateProfile(value Profile) error {
	if value.ID == uuid.Nil ||
		value.TenantID == uuid.Nil ||
		value.CreatedBy == uuid.Nil ||
		value.UpdatedBy == uuid.Nil ||
		value.Version < 1 ||
		value.CreatedAt.IsZero() ||
		value.UpdatedAt.Before(value.CreatedAt) ||
		value.DisplayName != strings.TrimSpace(value.DisplayName) ||
		value.DisplayName == `` ||
		len([]rune(value.DisplayName)) > MaxProfileDisplayNameRunes ||
		value.Introduction != strings.TrimSpace(value.Introduction) ||
		len([]rune(value.Introduction)) > MaxProfileIntroductionRunes {
		return ErrInvalidProfile
	}
	if value.Headline != nil &&
		(*value.Headline == `` ||
			*value.Headline != strings.TrimSpace(*value.Headline) ||
			len([]rune(*value.Headline)) > MaxProfileHeadlineRunes) {
		return ErrInvalidProfile
	}
	switch value.ModerationStatus {
	case ModerationStatusPending:
		if value.ModeratedBy != nil || value.ModeratedAt != nil {
			return ErrInvalidProfile
		}
	case ModerationStatusApproved, ModerationStatusRejected:
		if value.ModeratedBy == nil ||
			*value.ModeratedBy == uuid.Nil ||
			value.ModeratedAt == nil ||
			value.ModeratedAt.Before(value.CreatedAt) ||
			value.ModeratedAt.After(value.UpdatedAt) {
			return ErrInvalidProfile
		}
	default:
		return ErrInvalidProfile
	}
	switch value.ProfileStatus {
	case ProfileStatusDraft:
		if value.ModerationStatus != ModerationStatusPending &&
			value.ModerationStatus != ModerationStatusRejected {
			return ErrInvalidProfile
		}
	case ProfileStatusPublished:
		if value.ModerationStatus != ModerationStatusApproved {
			return ErrInvalidProfile
		}
	case ProfileStatusArchived:
	default:
		return ErrInvalidProfile
	}
	return nil
}

func normalizeProfileContent(
	displayName string,
	headline string,
	introduction string,
) (string, *string, string, error) {
	normalizedName := strings.TrimSpace(displayName)
	normalizedHeadline := strings.TrimSpace(headline)
	normalizedIntroduction := strings.TrimSpace(introduction)
	if normalizedName == `` ||
		len([]rune(normalizedName)) > MaxProfileDisplayNameRunes ||
		len([]rune(normalizedHeadline)) > MaxProfileHeadlineRunes ||
		len([]rune(normalizedIntroduction)) > MaxProfileIntroductionRunes {
		return ``, nil, ``, ErrInvalidProfile
	}
	var headlinePointer *string
	if normalizedHeadline != `` {
		headlinePointer = &normalizedHeadline
	}
	return normalizedName, headlinePointer, normalizedIntroduction, nil
}
