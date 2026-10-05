package people

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	MaxHostApplicationIntroductionRunes = 4000
	MaxHostApplicationExperienceRunes   = 4000
	MaxHostApplicationAvailabilityRunes = 1000
	MaxHostApplicationContactRunes      = 500
	MaxHostApplicationReviewRunes       = 2000
	MaxHostApplicationReferenceBytes    = 100
)

var hostApplicationReferencePattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$`,
)

type HostApplicationStatus string

const (
	HostApplicationStatusPending   HostApplicationStatus = `pending`
	HostApplicationStatusApproved  HostApplicationStatus = `approved`
	HostApplicationStatusRejected  HostApplicationStatus = `rejected`
	HostApplicationStatusWithdrawn HostApplicationStatus = `withdrawn`
)

type HostApplication struct {
	ID                   uuid.UUID
	TenantID             uuid.UUID
	PrincipalID          uuid.UUID
	ApplicationCycle     string
	PolicyVersion        string
	PersonalIntroduction string
	RelevantExperience   string
	Availability         string
	ContactMethod        string
	ApplicationStatus    HostApplicationStatus
	ReviewedBy           *uuid.UUID
	ReviewedAt           *time.Time
	ReviewComment        *string
	WithdrawnBy          *uuid.UUID
	WithdrawnAt          *time.Time
	Version              int64
	SubmittedAt          time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type NewHostApplicationCommand struct {
	TenantID             uuid.UUID
	PrincipalID          uuid.UUID
	ApplicationCycle     string
	PolicyVersion        string
	PersonalIntroduction string
	RelevantExperience   string
	Availability         string
	ContactMethod        string
	SubmittedAt          time.Time
}

type ReviewHostApplicationCommand struct {
	Decision HostApplicationStatus
	ActorID  uuid.UUID
	Comment  string
	At       time.Time
}

func (HostApplication) String() string {
	return `xiangwan HostApplication{sensitive_fields:[REDACTED]}`
}

func (value HostApplication) GoString() string {
	return value.String()
}

func (NewHostApplicationCommand) String() string {
	return `xiangwan NewHostApplicationCommand{sensitive_fields:[REDACTED]}`
}

func (value NewHostApplicationCommand) GoString() string {
	return value.String()
}

func (ReviewHostApplicationCommand) String() string {
	return `xiangwan ReviewHostApplicationCommand{sensitive_fields:[REDACTED]}`
}

func (value ReviewHostApplicationCommand) GoString() string {
	return value.String()
}

var (
	ErrInvalidHostApplication = errors.New(
		`invalid xiangwan HostApplication`,
	)
	ErrInvalidHostApplicationReview = errors.New(
		`invalid xiangwan HostApplication review`,
	)
	ErrHostApplicationTerminal = errors.New(
		`xiangwan HostApplication is terminal`,
	)
)

func NewHostApplication(
	command NewHostApplicationCommand,
) (HostApplication, error) {
	introduction, experience, availability, contact, err :=
		normalizeHostApplicationContent(
			command.PersonalIntroduction,
			command.RelevantExperience,
			command.Availability,
			command.ContactMethod,
		)
	cycle := strings.TrimSpace(command.ApplicationCycle)
	policyVersion := strings.TrimSpace(command.PolicyVersion)
	if err != nil ||
		command.TenantID == uuid.Nil ||
		command.PrincipalID == uuid.Nil ||
		!validHostApplicationReference(cycle) ||
		!validHostApplicationReference(policyVersion) ||
		command.SubmittedAt.IsZero() {
		return HostApplication{}, ErrInvalidHostApplication
	}
	submittedAt := command.SubmittedAt.UTC()
	value := HostApplication{
		ID:                   uuid.New(),
		TenantID:             command.TenantID,
		PrincipalID:          command.PrincipalID,
		ApplicationCycle:     cycle,
		PolicyVersion:        policyVersion,
		PersonalIntroduction: introduction,
		RelevantExperience:   experience,
		Availability:         availability,
		ContactMethod:        contact,
		ApplicationStatus:    HostApplicationStatusPending,
		Version:              1,
		SubmittedAt:          submittedAt,
		CreatedAt:            submittedAt,
		UpdatedAt:            submittedAt,
	}
	if err := ValidateHostApplication(value); err != nil {
		return HostApplication{}, err
	}
	return value, nil
}

func ReviewHostApplication(
	current HostApplication,
	command ReviewHostApplicationCommand,
) (HostApplication, bool, error) {
	if err := ValidateHostApplication(current); err != nil {
		return HostApplication{}, false, err
	}
	comment := strings.TrimSpace(command.Comment)
	if command.ActorID == uuid.Nil ||
		(command.Decision != HostApplicationStatusApproved &&
			command.Decision != HostApplicationStatusRejected) ||
		comment == `` ||
		len([]rune(comment)) > MaxHostApplicationReviewRunes ||
		command.At.IsZero() {
		return HostApplication{}, false, ErrInvalidHostApplicationReview
	}
	if current.ApplicationStatus != HostApplicationStatusPending {
		if (current.ApplicationStatus == HostApplicationStatusApproved ||
			current.ApplicationStatus == HostApplicationStatusRejected) &&
			current.ApplicationStatus == command.Decision &&
			current.ReviewedBy != nil &&
			*current.ReviewedBy == command.ActorID &&
			current.ReviewComment != nil &&
			*current.ReviewComment == comment {
			return current, true, nil
		}
		return HostApplication{}, false, ErrHostApplicationTerminal
	}
	if command.At.Before(current.UpdatedAt) {
		return HostApplication{}, false, ErrInvalidHostApplicationReview
	}

	at := command.At.UTC()
	actorID := command.ActorID
	result := current
	result.ApplicationStatus = command.Decision
	result.ReviewedBy = &actorID
	result.ReviewedAt = &at
	result.ReviewComment = &comment
	result.Version++
	result.UpdatedAt = at
	if err := ValidateHostApplication(result); err != nil {
		return HostApplication{}, false, err
	}
	return result, false, nil
}

func WithdrawHostApplication(
	current HostApplication,
	actorID uuid.UUID,
	at time.Time,
) (HostApplication, bool, error) {
	if err := ValidateHostApplication(current); err != nil {
		return HostApplication{}, false, err
	}
	if actorID == uuid.Nil || actorID != current.PrincipalID || at.IsZero() {
		return HostApplication{}, false, ErrInvalidHostApplication
	}
	if current.ApplicationStatus != HostApplicationStatusPending {
		if current.ApplicationStatus == HostApplicationStatusWithdrawn &&
			current.WithdrawnBy != nil &&
			*current.WithdrawnBy == actorID {
			return current, true, nil
		}
		return HostApplication{}, false, ErrHostApplicationTerminal
	}
	if at.Before(current.UpdatedAt) {
		return HostApplication{}, false, ErrInvalidHostApplication
	}

	withdrawnAt := at.UTC()
	result := current
	result.ApplicationStatus = HostApplicationStatusWithdrawn
	result.WithdrawnBy = &actorID
	result.WithdrawnAt = &withdrawnAt
	result.Version++
	result.UpdatedAt = withdrawnAt
	if err := ValidateHostApplication(result); err != nil {
		return HostApplication{}, false, err
	}
	return result, false, nil
}

func ValidateHostApplication(value HostApplication) error {
	if value.ID == uuid.Nil ||
		value.TenantID == uuid.Nil ||
		value.PrincipalID == uuid.Nil ||
		!validHostApplicationReference(value.ApplicationCycle) ||
		!validHostApplicationReference(value.PolicyVersion) ||
		value.PersonalIntroduction == `` ||
		value.PersonalIntroduction != strings.TrimSpace(
			value.PersonalIntroduction,
		) ||
		len([]rune(value.PersonalIntroduction)) >
			MaxHostApplicationIntroductionRunes ||
		value.RelevantExperience == `` ||
		value.RelevantExperience != strings.TrimSpace(
			value.RelevantExperience,
		) ||
		len([]rune(value.RelevantExperience)) >
			MaxHostApplicationExperienceRunes ||
		value.Availability == `` ||
		value.Availability != strings.TrimSpace(value.Availability) ||
		len([]rune(value.Availability)) >
			MaxHostApplicationAvailabilityRunes ||
		value.ContactMethod == `` ||
		value.ContactMethod != strings.TrimSpace(value.ContactMethod) ||
		len([]rune(value.ContactMethod)) > MaxHostApplicationContactRunes ||
		value.Version < 1 ||
		value.SubmittedAt.IsZero() ||
		!value.CreatedAt.Equal(value.SubmittedAt) ||
		value.UpdatedAt.Before(value.SubmittedAt) {
		return ErrInvalidHostApplication
	}

	switch value.ApplicationStatus {
	case HostApplicationStatusPending:
		if value.Version != 1 ||
			value.ReviewedBy != nil ||
			value.ReviewedAt != nil ||
			value.ReviewComment != nil ||
			value.WithdrawnBy != nil ||
			value.WithdrawnAt != nil ||
			!value.UpdatedAt.Equal(value.SubmittedAt) {
			return ErrInvalidHostApplication
		}
	case HostApplicationStatusApproved, HostApplicationStatusRejected:
		if value.Version != 2 ||
			value.ReviewedBy == nil ||
			*value.ReviewedBy == uuid.Nil ||
			value.ReviewedAt == nil ||
			value.ReviewedAt.Before(value.SubmittedAt) ||
			value.ReviewComment == nil ||
			*value.ReviewComment == `` ||
			*value.ReviewComment != strings.TrimSpace(*value.ReviewComment) ||
			len([]rune(*value.ReviewComment)) >
				MaxHostApplicationReviewRunes ||
			value.WithdrawnBy != nil ||
			value.WithdrawnAt != nil ||
			!value.UpdatedAt.Equal(*value.ReviewedAt) {
			return ErrInvalidHostApplication
		}
	case HostApplicationStatusWithdrawn:
		if value.Version != 2 ||
			value.ReviewedBy != nil ||
			value.ReviewedAt != nil ||
			value.ReviewComment != nil ||
			value.WithdrawnBy == nil ||
			*value.WithdrawnBy != value.PrincipalID ||
			value.WithdrawnAt == nil ||
			value.WithdrawnAt.Before(value.SubmittedAt) ||
			!value.UpdatedAt.Equal(*value.WithdrawnAt) {
			return ErrInvalidHostApplication
		}
	default:
		return ErrInvalidHostApplication
	}
	return nil
}

func normalizeHostApplicationContent(
	introduction string,
	experience string,
	availability string,
	contact string,
) (string, string, string, string, error) {
	introduction = strings.TrimSpace(introduction)
	experience = strings.TrimSpace(experience)
	availability = strings.TrimSpace(availability)
	contact = strings.TrimSpace(contact)
	if introduction == `` ||
		len([]rune(introduction)) > MaxHostApplicationIntroductionRunes ||
		experience == `` ||
		len([]rune(experience)) > MaxHostApplicationExperienceRunes ||
		availability == `` ||
		len([]rune(availability)) > MaxHostApplicationAvailabilityRunes ||
		contact == `` ||
		len([]rune(contact)) > MaxHostApplicationContactRunes {
		return ``, ``, ``, ``, ErrInvalidHostApplication
	}
	return introduction, experience, availability, contact, nil
}

func validHostApplicationReference(value string) bool {
	return len(value) <= MaxHostApplicationReferenceBytes &&
		hostApplicationReferencePattern.MatchString(value)
}
