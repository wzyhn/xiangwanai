package activity

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	DefaultPastActivitiesLimit = 20
	MaxPastActivitiesLimit     = 50
	MaxPastActivitiesCursorAge = 30 * time.Minute
	PastActivitiesFutureSkew   = 5 * time.Second
)

type PastActivitiesFilter struct {
	TenantID     uuid.UUID
	ActivityType ActivityType
	Limit        int
	Cursor       string
	At           time.Time
}

type PastActivityItem struct {
	SeriesID                         uuid.UUID
	SeriesTitle                      string
	SuccessfulPublishedInstanceCount int
	HistoricalRegistrationCount      int64
	InstanceID                       uuid.UUID
	InstanceTitle                    string
	InstanceStatus                   InstanceStatus
	ActivityType                     ActivityType
	CoverImageURL                    string
	PublicationVersion               int64
	PublishedAt                      time.Time
	CompletedAt                      time.Time
}

type PastActivitiesPage struct {
	Items              []PastActivityItem
	ActiveActivityType ActivityType
	AsOf               time.Time
	NextCursor         string
}

var (
	ErrInvalidPastActivitiesFilter = errors.New(
		"invalid xiangwan past activities filter",
	)
	ErrInvalidPastActivitiesCursor = errors.New(
		"invalid xiangwan past activities cursor",
	)
	ErrStalePastActivitiesCursor = errors.New(
		"stale xiangwan past activities cursor",
	)
	ErrInvalidPastActivityFacts = errors.New(
		"invalid xiangwan past activity facts",
	)
	ErrPastActivityNotFound = errors.New(
		"xiangwan past activity not found",
	)
)

func ValidPastActivityType(value ActivityType) bool {
	switch value {
	case ActivityTypeAll,
		ActivityTypeAIRoundtable,
		ActivityTypeSpecialEvent,
		ActivityTypeCourse,
		ActivityTypeCompetition,
		ActivityTypeCustom:
		return true
	default:
		return false
	}
}

func ValidatePastActivityItem(value PastActivityItem) error {
	if value.SeriesID == uuid.Nil || value.InstanceID == uuid.Nil ||
		strings.TrimSpace(value.SeriesTitle) == "" ||
		strings.TrimSpace(value.InstanceTitle) == "" ||
		value.SeriesTitle != strings.TrimSpace(value.SeriesTitle) ||
		value.InstanceTitle != strings.TrimSpace(value.InstanceTitle) ||
		(value.InstanceStatus != InstanceStatusCompleted &&
			value.InstanceStatus != InstanceStatusArchived) ||
		!ValidPastActivityType(value.ActivityType) ||
		value.ActivityType == ActivityTypeAll ||
		value.SuccessfulPublishedInstanceCount < 1 ||
		value.HistoricalRegistrationCount < 0 ||
		value.PublicationVersion < 1 || value.PublishedAt.IsZero() ||
		value.CompletedAt.IsZero() || value.PublishedAt.After(value.CompletedAt) {
		return ErrInvalidPastActivityFacts
	}
	return nil
}
